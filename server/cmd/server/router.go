package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/featureflagdispatch"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/integrations/slack"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/service"
	selfoptsvc "github.com/multica-ai/multica/server/internal/service/agent_self_optimization"
	agent_trust "github.com/multica-ai/multica/server/internal/service/agent_trust"
	mythossvc "github.com/multica-ai/multica/server/internal/service/mythos"
	swarmsvc "github.com/multica-ai/multica/server/internal/service/swarm"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

var defaultOrigins = []string{
	"http://localhost:3000", // Next.js dev
	"http://localhost:5173", // electron-vite dev
	"http://localhost:5174", // electron-vite dev (fallback port)
}

func allowedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	}
	if raw == "" {
		return defaultOrigins
	}

	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return defaultOrigins
	}
	return origins
}

// appURLFromEnv resolves the user-facing web app URL. It prefers
// MULTICA_APP_URL and falls back to FRONTEND_ORIGIN, matching how the backend
// resolves the app URL elsewhere (handler.daemonSetupURLsFromEnv) and the CLI
// login flow (cmd/multica tryResolveAppURL). Empty when neither is set.
func appURLFromEnv() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_APP_URL")), "/"); v != "" {
		return v
	}
	return strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN")), "/")
}

// parseTrustedProxies parses a comma-separated list of CIDR prefixes from the
// MULTICA_TRUSTED_PROXIES env var. Invalid entries are dropped with a single
// warn-line per entry rather than crashing the server — a typo in one CIDR
// shouldn't take the whole API down. Returns nil for empty input, which the
// rate limiter treats as "trust no proxy headers, use RemoteAddr only".
func parseTrustedProxies(raw string) []netip.Prefix {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			slog.Warn("MULTICA_TRUSTED_PROXIES: ignoring invalid CIDR",
				"value", s, "error", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

// NewRouter creates the fully-configured Chi router with all middleware and routes.
// rdb is optional: when non-nil the runtime local-skill request stores are
// swapped for Redis-backed implementations so multiple API nodes share the
// same pending queue (required for multi-node prod). This should be a request
// path Redis client, not the realtime relay's blocking read client. A nil rdb
// keeps the default in-memory stores which are fine for single-node dev and
// tests.
func NewRouter(pool *pgxpool.Pool, hub *realtime.Hub, bus *events.Bus, analyticsClient analytics.Client, rdb *redis.Client) chi.Router {
	r, _ := NewRouterWithOptions(pool, hub, bus, analyticsClient, rdb, RouterOptions{})
	return r
}

type RouterOptions struct {
	HTTPMetrics     *obsmetrics.HTTPMetrics
	BusinessMetrics *obsmetrics.BusinessMetrics
	DaemonHub       *daemonws.Hub
	DaemonWakeup    service.TaskWakeupNotifier
	FeatureFlags    *featureflag.Service
	// HeartbeatScheduler, when non-nil, replaces the default synchronous
	// passthrough scheduler on the constructed Handler. main.go injects a
	// BatchedHeartbeatScheduler here so the caller can also drive Run/Stop;
	// tests leave this nil and get the legacy synchronous behavior.
	HeartbeatScheduler handler.HeartbeatScheduler
}

// NewRouterWithOptions builds the fully-configured Chi router and
// returns the *handler.Handler it was constructed from. Callers that
// need to drive background lifecycle on services attached to the
// handler (e.g. starting the Lark inbound Hub under a long-running
// context, calling Wait on shutdown) use the returned handler;
// callers that only need the HTTP handler (tests, the simple
// NewRouter shim) discard the second value.
func NewRouterWithOptions(pool *pgxpool.Pool, hub *realtime.Hub, bus *events.Bus, analyticsClient analytics.Client, rdb *redis.Client, opts RouterOptions) (chi.Router, *handler.Handler) {
	queries := db.New(pool)
	emailSvc := service.NewEmailService()
	daemonHub := opts.DaemonHub
	if daemonHub == nil {
		daemonHub = daemonws.NewHub()
	}

	// Initialize storage with S3 as primary, fallback to local
	var store storage.Storage
	s3 := storage.NewS3StorageFromEnv()
	if s3 != nil {
		store = s3
	} else {
		local := storage.NewLocalStorageFromEnv()
		if local != nil {
			store = local
		}
	}

	origins := allowedOrigins()

	signupConfig := handler.Config{
		AllowSignup:              os.Getenv("ALLOW_SIGNUP") != "false",
		AllowedEmails:            splitAndTrim(os.Getenv("ALLOWED_EMAILS")),
		AllowedEmailDomains:      splitAndTrim(os.Getenv("ALLOWED_EMAIL_DOMAINS")),
		DisableWorkspaceCreation: os.Getenv("DISABLE_WORKSPACE_CREATION") == "true",
		PublicURL:                strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_PUBLIC_URL")), "/"),
		TrustedProxies:           parseTrustedProxies(os.Getenv("MULTICA_TRUSTED_PROXIES")),
		AttachmentDownloadMode:   os.Getenv("ATTACHMENT_DOWNLOAD_MODE"),
		AttachmentDownloadURLTTL: envDuration("ATTACHMENT_DOWNLOAD_URL_TTL", 30*time.Minute),
		AttachmentFrameAncestors: origins,
	}
	h := handler.New(queries, pool, hub, bus, emailSvc, store, analyticsClient, signupConfig, daemonHub)
	h.Metrics = opts.BusinessMetrics
	if opts.FeatureFlags != nil {
		h.DaemonFeatureFlags = featureflagdispatch.NewEvaluator(opts.FeatureFlags)
		h.FeatureFlags = opts.FeatureFlags
	}
	h.TaskService.Metrics = opts.BusinessMetrics
	// Trust gate (0.5.2): wire the agent_self_optimization trust service so
	// completed sub-agent tasks go through score-gated self-review. Always
	// on (the reviewer itself fail-opens when no provider CLI exists).
	h.TaskService.Trust = agent_trust.NewService()
	h.TrustService = agent_trust.NewService()
	h.IssueService.Metrics = opts.BusinessMetrics
	if opts.DaemonWakeup != nil {
		h.TaskService.Wakeup = opts.DaemonWakeup
		if notifier, ok := opts.DaemonWakeup.(handler.RuntimeProfileRefreshNotifier); ok {
			h.DaemonProfileRefresh = notifier
		}
	}
	if rdb != nil {
		h.UpdateStore = handler.NewRedisUpdateStore(rdb)
		h.ModelListStore = handler.NewRedisModelListStore(rdb)
		h.LocalSkillListStore = handler.NewRedisLocalSkillListStore(rdb)
		h.LocalSkillImportStore = handler.NewRedisLocalSkillImportStore(rdb)
		h.LivenessStore = handler.NewRedisLivenessStore(rdb)
		h.WebhookRateLimiter = handler.NewRedisWebhookRateLimiter(rdb, handler.DefaultWebhookRateLimit())
		h.WebhookIPRateLimiter = handler.NewRedisWebhookIPRateLimiter(rdb, handler.DefaultWebhookIPRateLimit())
	}

	// Channel engine (MUL-3620): the platform-agnostic inbound runtime.
	// Built UNCONDITIONALLY — it drives any channel.Channel, not just
	// Feishu, so it must not depend on the Lark master key (a future
	// Slack-only deployment has no Lark key). Platform adapters register a
	// Factory + ResolverSet into it below; the Supervisor enumerates active
	// installations across ALL channel types and routes each to its
	// registered platform's Factory. With no platform registered the store
	// still lists any active installation rows, but Registry.Build returns
	// ErrUnknownType for them, so the supervisor logs and backs off without
	// opening a connection (the normal state is simply that no rows exist
	// for an unregistered platform). The Router is the single shared inbound
	// handler injected into every Channel.
	channelRegistry := channel.NewRegistry()
	channelRouter := engine.NewRouter(h.IssueService, h.TaskService, queries, engine.RouterConfig{Logger: slog.Default()})
	// Debounce the per-session run trigger so a burst of messages collapses
	// into one agent run instead of one per message (MUL-2968).
	channelRouter.EnableRunBatching(engine.DefaultChatRunBatchWindow)
	h.ChannelRouter = channelRouter
	h.ChannelSupervisor = engine.NewSupervisor(
		lark.NewChannelInstallationStore(queries),
		channelRegistry,
		channelRouter.Handle,
		engine.Config{},
	)

	// Lark integration. Only wired when MULTICA_LARK_SECRET_KEY is set:
	// the InstallationService refuses to fall back to plaintext storage
	// for app_secret, and the BindingTokenService cannot mint usable
	// tokens without it either. When the key is absent the Lark
	// handlers return 503 with a clear message; the rest of the server
	// continues to start so self-host deployments that have not opted
	// in to Lark are unaffected. Feishu registers its Factory + ResolverSet
	// into the channel engine above.
	if larkKey, err := secretbox.LoadKey("MULTICA_LARK_SECRET_KEY"); err == nil {
		box, err := secretbox.New(larkKey)
		if err != nil {
			slog.Error("lark: secretbox.New failed; lark integration disabled", "error", err)
		} else {
			installSvc, err := lark.NewInstallationService(queries, box)
			if err != nil {
				slog.Error("lark: InstallationService init failed; lark integration disabled", "error", err)
			} else {
				h.LarkInstallations = installSvc
				h.LarkBindingTokens = lark.NewBindingTokenService(queries, pool)
				slog.Info("lark integration enabled")

				// APIClient: wire the real Lark Open Platform HTTP client
				// (IM v1 send/patch + binding-prompt + bot info). Setting
				// MULTICA_LARK_SECRET_KEY is the operator's opt-in for
				// the integration as a whole; we don't expose a separate
				// "HTTP enabled" knob because the inbound dispatcher
				// without outbound replies is not a useful production
				// state, and CI / integration tests that want to avoid
				// real Lark traffic can point MULTICA_LARK_HTTP_BASE_URL
				// at a mock server.
				//
				// MULTICA_LARK_HTTP_BASE_URL is an OPTIONAL deployment-wide
				// override. Normal operation leaves it empty: each call then
				// resolves its open-platform host from the installation's
				// region (open.feishu.cn vs open.larksuite.com), so one
				// deployment serves both clouds. Set it only to force every
				// installation onto one host — a proxy, a mock for tests, or
				// a single-cloud staging setup.
				larkClient := lark.NewHTTPAPIClient(lark.HTTPClientConfig{
					BaseURL: strings.TrimSpace(os.Getenv("MULTICA_LARK_HTTP_BASE_URL")),
					Logger:  slog.Default(),
				})
				h.LarkAPIClient = larkClient

				// Channel-backed store: routes the lark package's DB seams
				// onto the channel_* tables (MUL-3515). Interface-wired
				// consumers (patcher, typing indicator, dispatcher, hub,
				// backfills) take it directly; the constructor-based services
				// wrap *db.Queries internally, so they keep taking queries.
				cs := lark.NewChannelStore(queries)
				patcher := lark.NewPatcher(cs, installSvc, larkClient, lark.PatcherConfig{})
				patcher.Register(bus)

				// Typing indicator: shows a "processing" reaction on the user's
				// message while the agent is working, then removes it before the
				// reply is sent. Best-effort; failures are logged only.
				typingIndicator := lark.NewTypingIndicatorManager(larkClient, installSvc, cs, slog.Default())
				patcher.SetTypingIndicatorManager(typingIndicator)

				// Inbound pipeline seams: lark_inbound_audit logger and the
				// shared channel-agnostic chat-session service. They back the
				// Feishu ResolverSet that the engine.Router runs through,
				// sharing the same IssueService + TaskService that back HTTP, so
				// /issue-created issues share counter, dup guard, project
				// boundary, broadcast, analytics and agent-enqueue with the rest
				// of the product. Feishu is just another consumer of the shared
				// engine.ChatSession (channel_type-keyed); the Lark session
				// titles preserve the pre-cutover wording.
				auditLogger := lark.NewAuditLogger(queries)
				feishuSession := engine.NewChatSession(queries, pool, channel.TypeFeishu, engine.SessionTitles{
					Group:    "Lark group chat",
					Direct:   "Lark direct message",
					Fallback: "Lark chat",
				})

				// OutcomeReplier wires the outbound side: NeedsBinding /
				// AgentOffline / AgentArchived / issue-created translate to a
				// Lark-side reply card. Requires the real APIClient and the
				// binding token service; otherwise it falls back to the noop
				// replier (outcomes logged, not delivered). We only register
				// it on the ResolverSet when it can actually deliver, so a
				// pre-outbound deployment pays no reply-goroutine cost.
				replier := lark.NewLarkOutcomeReplier(lark.OutcomeReplierConfig{
					APIClient:   larkClient,
					BindingSvc:  h.LarkBindingTokens,
					Credentials: installSvc,
					Queries:     queries,
					AppURL:      appURLFromEnv(),
					Logger:      slog.Default(),
				})
				var resolverReplier lark.OutcomeReplier
				if larkClient.IsConfigured() {
					resolverReplier = replier
				}

				// Feishu adapter (MUL-3620): the WSLongConnConnector talks
				// Lark's long-conn protocol over gorilla/websocket and wraps
				// every read with a ctx-cancel watchdog so lease loss /
				// shutdown breaks the blocking ReadMessage in bounded time —
				// the invariant §4.4 leans on. If the endpoint fetcher fails
				// to initialize (bad MULTICA_LARK_CALLBACK_BASE_URL or
				// similar), buildLarkConnector logs and falls back to the
				// NoopConnector so the lease / supervisor lifecycle still runs
				// against real DB rows — inbound messages are silently dropped
				// until the config is fixed, with the boot log labelling the
				// mode "noop".
				//
				// Registering the Factory (connect/send) + ResolverSet
				// (inbound pipeline seams) is all it takes to add the platform
				// to the engine — no engine edit.
				connector, connectorLabel := buildLarkConnector(installSvc, larkClient)
				lark.RegisterFeishu(channelRegistry, lark.FeishuChannelDeps{
					Connector:   connector,
					APIClient:   larkClient,
					Credentials: installSvc,
					Logger:      slog.Default(),
				})
				channelRouter.Register(channel.TypeFeishu, lark.NewFeishuResolverSet(
					cs, feishuSession, auditLogger, resolverReplier, typingIndicator,
				))
				slog.Info("lark inbound pipeline wired", "connector", connectorLabel)

				// One-shot union_id backfill for installations created
				// before migration 112 added bot_union_id. Runs off the
				// hot startup path so a slow Lark round-trip cannot block
				// HTTP listener boot. New installs already write
				// bot_union_id during the device-flow finalize, so this
				// is bridge code — it will simply find no rows to update
				// on a fresh deployment and exit. MUL-2671.
				go lark.BackfillBotUnionIDs(context.Background(), cs, larkClient, installSvc, slog.Default())

				// Upgrade repair for deployments that ran the whole
				// integration against Lark international via the deployment-
				// wide base-URL override before per-installation region
				// existed: migration 116 backfilled their rows to 'feishu',
				// so relabel them to 'lark' (their true cloud) before the
				// operator clears the override. No-op on mainland / fresh
				// deployments. Off the hot startup path like the union_id
				// backfill. MUL-3083.
				go lark.BackfillRegionFromLegacyOverride(context.Background(), cs,
					strings.TrimSpace(os.Getenv("MULTICA_LARK_HTTP_BASE_URL")),
					strings.TrimSpace(os.Getenv("MULTICA_LARK_CALLBACK_BASE_URL")),
					slog.Default())

				// Device-flow registration service: end-to-end install
				// pipeline that talks to accounts.feishu.cn (RFC 8628)
				// for the QR-scan handshake and then commits the
				// resulting Bot credentials + the installer's
				// lark_user_binding in one DB transaction. The optional
				// MULTICA_LARK_REGISTRATION_DOMAIN / _LARK_DOMAIN env
				// vars override the protocol hosts for staging / dev.
				regCfg := lark.RegistrationConfig{
					Domain:     strings.TrimSpace(os.Getenv("MULTICA_LARK_REGISTRATION_DOMAIN")),
					LarkDomain: strings.TrimSpace(os.Getenv("MULTICA_LARK_REGISTRATION_LARK_DOMAIN")),
				}
				regClient := lark.NewRegistrationClient(regCfg)
				regSvc, rerr := lark.NewRegistrationService(
					lark.RegistrationServiceConfig{Logger: slog.Default()},
					regClient,
					larkClient,
					queries,
					pool,
					installSvc,
					h.LarkBindingTokens,
				)
				if rerr != nil {
					slog.Error("lark: RegistrationService init failed; install disabled", "error", rerr)
				} else {
					// Publish lark_installation:created at row-commit time so the
					// connection badge refreshes on every workspace client, not just
					// the tab that polls the install status to success.
					regSvc.SetEventBus(bus)
					h.LarkRegistration = regSvc
					slog.Info("lark device-flow install enabled")
				}
			}
		}
	} else {
		slog.Info("lark integration disabled (MULTICA_LARK_SECRET_KEY not set)")
	}

	// Slack integration (MUL-3516). Gated by MULTICA_SLACK_SECRET_KEY — the key
	// that decrypts the bot/app tokens stored on the channel_installation row.
	// When unset the whole block is skipped, so existing deployments are
	// unaffected; an operator opts in by setting the key and creating a
	// channel_type='slack' installation (config: app_id=team_id, bot_user_id,
	// bot_token_encrypted, app_token_encrypted). Registering the Factory
	// (Socket Mode connect/send) + ResolverSet (inbound pipeline) + the outbound
	// subscriber (agent reply -> Slack) is all it takes — no engine or core edit,
	// and Feishu is untouched. The Slack ResolverSet/Outbound share the same
	// engine.ChatSession, channel_* tables, IssueService and TaskService as
	// Feishu, so /issue, dedup, and run-triggering behave identically.
	if slackKey, err := secretbox.LoadKey("MULTICA_SLACK_SECRET_KEY"); err == nil {
		box, err := secretbox.New(slackKey)
		if err != nil {
			slog.Error("slack: secretbox.New failed; slack integration disabled", "error", err)
		} else {
			slack.RegisterSlack(channelRegistry, slack.SlackChannelDeps{Decrypt: box.Open, Logger: slog.Default()})
			channelRouter.Register(slack.TypeSlack, slack.NewSlackResolverSet(queries, pool))
			slack.NewOutbound(queries, box.Open, slog.Default()).Register(bus)
			slog.Info("slack integration enabled")
		}
	} else {
		slog.Info("slack integration disabled (MULTICA_SLACK_SECRET_KEY not set)")
	}

	if opts.HeartbeatScheduler != nil {
		h.HeartbeatScheduler = opts.HeartbeatScheduler
	}
	// Auth caches: PAT cache is shared between the regular Auth middleware,
	// the DaemonAuth fallback (mul_) path, and the revoke handler
	// (invalidate). DaemonTokenCache backs the DaemonAuth mdt_ path. Both
	// constructors return nil when rdb is nil — every consumer handles that
	// as "no cache, always hit DB".
	patCache := auth.NewPATCache(rdb)
	daemonTokenCache := auth.NewDaemonTokenCache(rdb)
	h.PATCache = patCache
	h.DaemonTokenCache = daemonTokenCache
	h.MembershipCache = auth.NewMembershipCache(rdb)

	// Cloud PAT verifier: validates mcn_ tokens against Multica Cloud
	// Fleet. Removed in the localized build — no mcn_ tokens exist.
	_ = rdb

	// Empty-claim cache: lets the daemon poll path skip a Postgres
	// scan when a recent check confirmed the runtime had no queued
	// task. Returns nil when rdb is nil — TaskService treats that
	// as "no cache, always hit DB" (existing behavior).
	h.TaskService.EmptyClaim = service.NewEmptyClaimCache(rdb)

	// Wire WS heartbeat after stores are finalized so the WS path uses the
	// same (possibly Redis-backed) stores as the HTTP path.
	daemonHub.SetHeartbeatHandler(h.HandleDaemonWSHeartbeat)
	health := newServerHealth(pool)

	r := chi.NewRouter()

	// Global middleware
	r.Use(chimw.RequestID)
	r.Use(middleware.ClientMetadata)
	r.Use(middleware.RequestLogger)
	if opts.HTTPMetrics != nil {
		r.Use(opts.HTTPMetrics.Middleware)
	}
	r.Use(chimw.Recoverer)
	r.Use(middleware.ContentSecurityPolicy)
	// 0.3.18 Labs safety: break flags that produce repeated 5xx
	// responses. Threshold + window are configurable via
	// MULTICA_EXPERIMENTAL_BURST_THRESHOLD / _WINDOW_SECONDS env vars
	// (defaults 3 errors in 60s). See middleware.ExperimentalFlagBurst.
	r.Use(middleware.ExperimentalFlagBurst(middleware.BurstConfig{
		Threshold: envPositiveInt("MULTICA_EXPERIMENTAL_BURST_THRESHOLD", 3),
		Window:    time.Duration(envPositiveInt("MULTICA_EXPERIMENTAL_BURST_WINDOW_SECONDS", 60)) * time.Second,
	}))

	// Share allowed origins with WebSocket origin checker.
	realtime.SetAllowedOrigins(origins)

	// Share the same trusted-proxy CIDRs (MULTICA_TRUSTED_PROXIES) so the
	// WebSocket origin check honors X-Forwarded-Host only from trusted proxies,
	// using one config source instead of a parallel one.
	realtime.SetTrustedProxies(signupConfig.TrustedProxies)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Workspace-ID", "X-Workspace-Slug", "X-Request-ID", "X-Agent-ID", "X-Task-ID", "X-CSRF-Token", "X-Client-Platform", "X-Client-Version", "X-Client-OS"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health / readiness checks
	r.Get("/health", health.liveHandler)
	r.Get("/readyz", health.readyHandler)
	r.Get("/healthz", health.readyHandler)

	// Experimental / Labs same-origin proxies (0.3.12).
	// 0.5.29 P1-1 — synthesizer Round 7: moved INSIDE the auth
	// chain (see `r.Group(middleware.Auth...)` below). The proxies
	// used to be mounted on the public router because the iframe
	// "is only reachable from inside the app" — but R4 surfaced the
	// pre-auth credential oracle: MountExperimentalProxies mounted
	// before the auth chain meant any caller that could reach
	// /experimental/semantica/* (an X-API-Key set on the request
	// line) got the proxy to forward that key verbatim while
	// Cookie/Authorization were stripped, yielding an
	// unauthenticated credential grant. The iframe stays
	// same-origin, so cookies are auto-attached; gating on
	// middleware.Auth closes the pre-auth reachability without
	// changing the renderer's call shape.
	// handler.MountExperimentalProxies(r, h) -- see below, in the auth group

	// 0.3.19 P2: bind install handlers to the experiment registry.
	// The dispatcher in experimental_resources.go reads
	// h.ExperimentRegistry; without these bindings the install
	// endpoint falls back to the marker-only path. New installable
	// labs add a single RegisterInstallHandler call here.
	//
	// 0.3.26: the Claude Lab install was previously wired to the
	// legacy `SourceClaudeScience` (the 0.3.20 source). Since 0.3.22
	// the user-facing catalog flag is `claude_science_lab`, so the
	// flag-toggle handler's `experimental.Source(f.Key)` returned
	// `SourceClaudeScienceLab`, which the dispatcher could not find
	// a handler for, falling through to the marker-only path. We
	// register the consolidated source below; the legacy source is
	// intentionally NOT re-wired (no flag currently maps to it). New
	// installable labs add a single RegisterInstallHandler call here.
	//
	// The install closures capture `r` and `h` for use at request
	// time, but they intentionally do NOT capture a per-request
	// context — the registry's InstallHandler signature passes
	// userID + workspaceID. The install implementations
	// (InstallClaudeScience / InstallMythos) construct their own
	// context with the deadline they need, so tying them to a
	// request ctx would couple install progress to a single HTTP
	// request's lifetime.
	if h.ExperimentRegistry != nil {
		hh := h
		h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceClaudeScienceLab),
			func(userID, workspaceID string) error {
				return hh.InstallClaudeScience(context.Background(), experimental.SourceClaudeScienceLab, userID, workspaceID)
			})
		h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceMythosSwarm),
			func(userID, workspaceID string) error {
				return hh.InstallMythos(context.Background(), userID, workspaceID)
			})
		// 0.3.27 B4: agent_self_optimization install handler. Removed
		// in 0.5.6 — the agent + 2 autopilots are boot-provisioned by
		// the agent_self_optimization service (0.3.45.1, always-on)
		// and the catalog flag is gone, so there is no longer an
		// `experimental install agent_self_optimization` entry point.
		// 0.3.54: pythia_oracle + code_canvas install handlers. Both
		// follow the same shape as install_mythos.go: a single leader
		// agent row is upserted and claimed under the lab's source.
		// Real subprocess lifecycle (Python for Pythia, run.sh stub
		// for code_canvas) is owned by the desktop manager-factory —
		// the install handler only writes the DB rows that the daemon
		// auto-dispatch path lands on.
		h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourcePythiaOracle),
			func(userID, workspaceID string) error {
				return hh.InstallPythia(context.Background(), userID, workspaceID)
			})
		h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceCodeCanvas),
			func(userID, workspaceID string) error {
				return hh.InstallCodeCanvas(context.Background(), userID, workspaceID)
			})
		// 0.5.22 Semantica × Multica Phase 2: install handler for the
		// semantica lab. Mirrors pythia_oracle / code_canvas shape — a
		// single-leader install that provisions the
		// semantica_decision_advisor agent + visibility row. The
		// Semantica FastAPI subprocess itself is owned by the desktop
		// manager-factory; the install handler only writes the DB rows
		// the daemon auto-dispatch path lands on.
		h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceSemantica),
			func(userID, workspaceID string) error {
				return hh.InstallSemantica(context.Background(), userID, workspaceID)
			})
		// 0.5.3: agent_creation_studio upgraded from an action-only lab to
		// an issue-bound lab: selecting it in LabPicker writes
		// issue.lab_source='agent_creation_studio' and the leader agent
		// (agent_creation_expert) is auto-assigned, so user-delegated
		// creation tasks dispatch to it. The manual creator stays.
		//
		// 0.5.6: removed entirely. The `agent_creation_expert` leader
		// agent is boot-provisioned by `boot_provision_product_labs.go`;
		// the install handler is no longer reachable, the catalog
		// literal is gone, and the Labs tab does not surface the lab.
	}

	// 0.3.60: boot-time user plugin loading. Merge active user plugins
	// from the DB into the experimental registry so they appear in Labs
	// alongside the built-in catalog flags. Non-fatal: a transient DB
	// failure on boot must not block startup.
	// 0.3.62: guard with a defer/recover so test harnesses that pass a
	// nil pgxpool.Pool (queries.New(nil) returns a non-nil struct whose
	// inner DBTX is nil and panics on Acquire) don't take down the
	// router. The boot-time load is a UX nicety, not a correctness
	// invariant — failure here must never block startup.
	if h.ExperimentRegistry != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("user plugins boot-time load skipped (nil DB pool)", "recover", r)
				}
			}()
			rows, err := h.Queries.ListActiveUserPlugins(context.Background())
			if err != nil {
				slog.Warn("failed to load user plugins at boot", "err", err)
				return
			}
			pluginRows := make([]experimental.UserPluginRow, 0, len(rows))
			for _, row := range rows {
				manifestStr := "{}"
				if len(row.ManifestJson) > 0 {
					manifestStr = string(row.ManifestJson)
				}
				pluginRows = append(pluginRows, experimental.UserPluginRow{
					Slug:          row.Slug,
					FlagKey:       row.FlagKey,
					TitleEn:       row.TitleEn,
					TitleZh:       row.TitleZh,
					DescriptionEn: row.DescriptionEn,
					DescriptionZh: row.DescriptionZh,
					ManifestJSON:  manifestStr,
					TriggerMode:   row.TriggerMode,
					RuntimeKind:   row.RuntimeKind,
					Status:        row.Status,
				})
			}
			userFlags := experimental.UserPluginsToFlags(pluginRows)
			experimental.RegisterUserPlugins(userFlags)
			h.ExperimentRegistry.MergeUserPlugins(userFlags)
			slog.Info("user plugins loaded at boot", "count", len(userFlags))
		}()
	}

	// 0.3.31: wire the Mythos supervise service so the HTTP tick /
	// get-state handlers can drive a synchronous tick. The supervise
	// goroutines themselves are launched lazily by Service.Run when
	// an enhancer-mode mythos_run is created; ResumeSupervision picks
	// up any orphaned runs from a previous daemon process.
	{
		svc := mythossvc.NewService(h.Queries)
		h.MythosService = svc
		// Best-effort recovery: scan every workspace for runs in
		// 'supervising' state. The 0.3.31 SQL query scopes by
		// workspace; we walk the workspace id list to cover all of
		// them. Logged but non-fatal — a transient DB failure on
		// boot should not block startup.
		bootCtx, bootCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer bootCancel()
		// A nil pool (tests that only exercise routing, e.g.
		// TestMainRouterDoesNotExposePrometheusMetrics) has no DB to
		// scan; skip the recovery walk rather than dereferencing a nil
		// pool inside Queries.ListAllWorkspaceIDs. Consistent with the
		// "non-fatal on boot" contract above.
		if pool != nil {
			if ids, err := h.Queries.ListAllWorkspaceIDs(bootCtx); err == nil {
				var totalResumed int
				for _, id := range ids {
					if n, err := svc.ResumeSupervision(bootCtx, id); err != nil {
						slog.Warn("mythos supervise resume failed",
							"workspace_id", util.UUIDToString(id),
							"err", err)
					} else {
						totalResumed += n
					}
				}
				if totalResumed > 0 {
					slog.Info("mythos supervise resumed",
						"total", totalResumed)
				}
			} else {
				slog.Warn("mythos supervise resume: list workspace ids failed",
					"err", err)
			}
		}
	}

	// 0.3.45.1: wire the agent_self_optimization service. Mirrors the
	// mythos supervise block above: a single Service instance hosts a
	// per-workspace scheduler ticker that fires when NextTrigger(now)
	// matches the user's local weekday 10:00 + ≥96h-since-last-success
	// gates. Service.Start() no-ops when the flag is OFF (see
	// agent_self_optimization/flag.go), so this block is safe to ship
	// without a per-flag toggle.
	{
		optSvc := selfoptsvc.NewService(h.Queries)
		h.SelfOptService = optSvc
		// Best-effort recovery: scan for runs left in 'pending' or
		// 'running' by a previous process. Resume marks zombies
		// (>MaxRunLifetime in 'running') as 'failed' so a fresh
		// scheduler tick can pick a new slot.
		bootCtx, bootCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer bootCancel()
		if pool != nil {
			if ids, err := h.Queries.ListAllWorkspaceIDs(bootCtx); err == nil {
				var totalResumed int
				for _, id := range ids {
					if n, err := optSvc.Resume(bootCtx, id); err != nil {
						slog.Warn("agent-self-opt resume failed",
							"workspace_id", util.UUIDToString(id),
							"err", err)
					} else {
						totalResumed += n
					}
				}
				if totalResumed > 0 {
					slog.Info("agent-self-opt resumed", "total", totalResumed)
				}
				// Launch the per-workspace tickers. Start() no-ops
				// when the flag is OFF, so the boot cost on a default
				// install is the (cheap) Resume scan only.
				if err := optSvc.Start(bootCtx, ids); err != nil {
					slog.Warn("agent-self-opt start failed", "err", err)
				}
			} else {
				slog.Warn("agent-self-opt start: list workspace ids failed",
					"err", err)
			}
		}
	}

	// 0.5.21: wire the swarm topology service. Mirrors the mythos
	// supervise block above: a single Service instance hosts a
	// per-swarm_run orchestrator goroutine (30s tick + 72h cap +
	// 5-phase machine). Orchestrators launch lazily via
	// StartOrchestrator on bootstrap (multica-creating-swarms SKILL.md
	// Phase 2); ResumeOrchestration re-adopts non-terminal runs from
	// a previous daemon process (mirrors mythos ResumeSupervision).
	// The swarm_gc GC loop runs on the same 6h cadence as runtime_gc
	// (terminal status + 7d → archive, 90d → trash).
	{
		svc := swarmsvc.NewService(h.Queries, slog.Default())
		h.SwarmService = svc
		bootCtx, bootCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer bootCancel()
		if pool != nil {
			if ids, err := h.Queries.ListAllWorkspaceIDs(bootCtx); err == nil {
				var totalResumed int
				for _, id := range ids {
					if n, err := svc.ResumeOrchestration(bootCtx, id); err != nil {
						slog.Warn("swarm orchestrator resume failed",
							"workspace_id", util.UUIDToString(id),
							"err", err)
					} else {
						totalResumed += n
					}
				}
				if totalResumed > 0 {
					slog.Info("swarm orchestrator resumed", "total", totalResumed)
				}
			} else {
				slog.Warn("swarm orchestrator resume: list workspace ids failed",
					"err", err)
			}
			// Launch swarm_gc (parallel to runtime_gc.Start pattern).
			// The GC is monolithic (one sweep per tick) so a nil pool
			// (test-only build) skips it cleanly.
			swarmGC := experimental.NewSwarmGC(experimental.SwarmGCConfig{
				Queries: h.Queries,
			})
			swarmGC.Start(bootCtx)
			// 0.5.22 audit fix (P2): store the GC on the Handler so
			// the shutdown sequence in cmd/server/main.go can call
			// Stop() — the GC goroutine otherwise outlives the
			// graceful shutdown and gets SIGKILL'd mid-tick (a tick
			// could be mid-archive, leaving the sentinel in place
			// until the next boot re-adopts it).
			h.SwarmGC = swarmGC
			slog.Info("swarm_gc started")

			// Launch runtime_gc — the 30/90/120-day retention ladder
			// for experimental_claude_runtime_session rows. Pre-0.5.25
			// this GC existed in the codebase (migration 151 documented
			// it) but was never wired at boot, so expired sessions
			// accumulated indefinitely. Mirrors the swarm_gc pattern:
			// store on the Handler so main.go's shutdown sequence
			// can call Stop() before SIGKILL. Interval defaults to
			// 6h inside NewRuntimeGC; production cadence matches
			// research tempo (a few sessions per day).
			runtimeGC := experimental.NewRuntimeGC(experimental.RuntimeGCConfig{
				Queries: h.Queries,
			})
			runtimeGC.Start()
			h.RuntimeGC = runtimeGC
			slog.Info("runtime_gc started")

			// 0.5.30 P1-3 — synthesizer Round 7: Semantica
			// provenance + api-key retention. Filesystem-only
			// (no db.Queries); 24h tick + 90d cutoff. Defaults
			// baked into NewSemanticaGC.
			semanticaGC := experimental.NewSemanticaGC(experimental.SemanticaGCConfig{})
			semanticaGC.Start()
			h.SemanticaGC = semanticaGC
			slog.Info("semantica_gc started")

			// 0.5.31: AuthTokenGC sweeps the three auth-token
			// tables that have an `expires_at` column but no
			// working retention GC (task_token + workspace_invitation
			// + daemon_token). Same dormant-ladder bug class as
			// RuntimeGC pre-0.5.25 — the migrations documented the
			// ladder but no GC ever swept the rows. Mirrors the
			// RuntimeGC + SwarmGC pattern: store on the Handler so
			// main.go's shutdown sequence can call Stop() before
			// SIGKILL. Interval defaults to 6h inside
			// NewAuthTokenGC; per-table sub-context timeout 15s.
			authTokenGC := experimental.NewAuthTokenGC(experimental.AuthTokenGCConfig{
				Queries: h.Queries,
			})
			authTokenGC.Start()
			h.AuthTokenGC = authTokenGC
			slog.Info("auth_token_gc started")
		}
	}

	// 0.5.5: boot-provision the agent_creation_studio leader
	// (`agent_creation_expert`) into every workspace. The studio is
	// now a product-level resource (catalog DefaultVal=true), so the
	// 0.3.46 P0#4 leader-rewrite path will look for this agent on
	// every issue creation/update and silently no-op if it is missing.
	// Provisioning at boot makes the AssigneePicker just work without
	// any user "install" step. Non-fatal: a transient DB failure
	// logs at warn and the next leader-rewrite call self-heals via
	// `Handler.EnsureProductAgentForWorkspace` (lazy fallback).
	if pool != nil {
		h.BootProvisionProductLabs(context.Background())
	}

	// Realtime subsystem metrics — connection counts, slow-client evictions,
	// and per-event-type send QPS counters. Exposed as JSON so it can be
	// scraped by ops or surfaced in the admin UI without adding a Prometheus
	// dependency. See MUL-1138 (Phase 0).
	//
	// Access is restricted (MUL-1342): when REALTIME_METRICS_TOKEN is set,
	// callers must present it via Authorization: Bearer <token>. When the
	// env var is unset the handler only serves loopback callers so local
	// dev keeps working without exposing the metrics on a public listener.
	r.Get("/health/realtime", realtimeMetricsHandler(os.Getenv("REALTIME_METRICS_TOKEN")))

	// WebSocket
	mc := &membershipChecker{queries: queries}
	pr := &patResolver{queries: queries, cache: patCache}
	slugResolver := realtime.SlugResolver(func(ctx context.Context, slug string) (string, error) {
		ws, err := queries.GetWorkspaceBySlug(ctx, slug)
		if err != nil {
			return "", err
		}
		return util.UUIDToString(ws.ID), nil
	})
	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		realtime.HandleWebSocket(hub, mc, pr, slugResolver, w, r)
	})

	// Local file serving (when using local storage)
	if local, ok := store.(*storage.LocalStorage); ok {
		r.Get("/uploads/*", func(w http.ResponseWriter, r *http.Request) {
			file := strings.TrimPrefix(r.URL.Path, "/uploads/")
			local.ServeFile(w, r, file)
		})
	}

	// Auth (public) — per-IP rate limiting.
	if rdb == nil {
		slog.Warn("rate limiting disabled: REDIS_URL not configured")
	}
	trustedProxies := middleware.ParseTrustedProxies(os.Getenv("RATE_LIMIT_TRUSTED_PROXIES"))
	authRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_AUTH", 5), time.Minute, trustedProxies)
	authVerifyRL := middleware.RateLimit(rdb, envPositiveInt("RATE_LIMIT_AUTH_VERIFY", 20), time.Minute, trustedProxies)
	r.With(authRL).Post("/auth/send-code", h.SendCode)
	r.With(authVerifyRL).Post("/auth/verify-code", h.VerifyCode)
	r.With(authRL).Post("/auth/google", h.GoogleLogin)
	r.With(authRL).Post("/auth/login", h.UsernameLogin)
	r.Post("/auth/logout", h.Logout)

	// Loopback bridge: PYTHIA's `_complete()` and future headless
	// experimental services (Claude Science skill matcher, Mythos Swarm
	// sub-agents) post here to run an LLM call. Source-IP gated to
	// 127.0.0.0/8; bearer auth still required so a stolen loopback can't
	// just dial it without a JWT. See handler.runtime_llm_call.go.
	r.Post("/api/runtime/llm-call", h.LLMCallHandler)

	// 0.3.16 Multica-native skills browser. The catalogue is sourced
	// from the claude_science reserved-slug workspace + the bundled
	// manifest; the renderer under /experimental/claude-science polls
	// this every refetch. Public to any signed-in user — see
	// handler.claude_science_skills.go for the visibility filter.
	r.Get("/api/experimental/claude-science/skills", h.ClaudeScienceSkills)

	// 0.3.30: Labs runtime + LLM Wiki bridge + Mythos Swarm + Pythia
	// per-issue forecast are now registered UNCONDITIONALLY inside the
	// authenticated group, each wrapped in a per-request
	// RequireExperimentalFlag guard. See the auth group below and
	// handler/experimental_guard.go for why the old boot-time
	// `if experimental.DefaultFor(key)` gate could never observe a
	// per-user runtime toggle.

	// Public API
	r.Get("/api/config", h.GetConfig)

	// Webhook ingress for autopilots. Outside the authenticated group on
	// purpose: the bearer token in the URL path IS the credential. Workspace
	// context is derived from the trigger row, never from request headers.
	r.Post("/api/webhooks/autopilots/{token}", h.HandleAutopilotWebhook)
	// GitHub App webhook (no Multica auth — requests are authenticated via
	// HMAC-SHA256 signature in the handler) and post-install setup callback.
	r.Post("/api/webhooks/github", h.HandleGitHubWebhook)
	r.Get("/api/github/setup", h.GitHubSetupCallback)
	// Stripe webhook (no Multica auth — Stripe signs the raw body
	// with a shared secret, the multica-cloud upstream verifies. We
	// only forward the bytes + the Stripe-Signature header; see
	// (Stripe webhook was the cloud-billing payment hook; removed with
	// the rest of cloud-billing.)

	// Daemon API routes (require daemon token or valid user token)
	r.Route("/api/daemon", func(r chi.Router) {
		r.Use(middleware.DaemonAuth(queries, patCache, daemonTokenCache))

		r.Post("/register", h.DaemonRegister)
		r.Post("/deregister", h.DaemonDeregister)
		r.Post("/heartbeat", h.DaemonHeartbeat)
		r.Get("/ws", h.DaemonWebSocket)
		r.Get("/workspaces/{workspaceId}/repos", h.GetDaemonWorkspaceRepos)
		r.Get("/workspaces/{workspaceId}/runtime-profiles", h.DaemonListRuntimeProfiles)

		r.Post("/runtimes/{runtimeId}/tasks/claim", h.ClaimTaskByRuntime)
		r.Post("/runtimes/{runtimeId}/tasks/{taskId}/prepare-lease", h.ExtendTaskPrepareLease)
		r.Post("/runtimes/{runtimeId}/tasks/{taskId}/skill-bundles/resolve", h.ResolveTaskSkillBundles)
		r.Get("/runtimes/{runtimeId}/tasks/pending", h.ListPendingTasksByRuntime)
		r.Post("/runtimes/{runtimeId}/update/{updateId}/result", h.ReportUpdateResult)
		r.Post("/runtimes/{runtimeId}/models/{requestId}/result", h.ReportModelListResult)
		r.Post("/runtimes/{runtimeId}/local-skills/{requestId}/result", h.ReportLocalSkillListResult)
		r.Post("/runtimes/{runtimeId}/local-skills/import/{requestId}/result", h.ReportLocalSkillImportResult)

		r.Get("/tasks/{taskId}/status", h.GetTaskStatus)
		r.Post("/tasks/{taskId}/start", h.StartTask)
		r.Post("/tasks/{taskId}/wait-local-directory", h.MarkTaskWaitingLocalDirectory)
		r.Post("/tasks/{taskId}/progress", h.ReportTaskProgress)
		r.Post("/tasks/{taskId}/complete", h.CompleteTask)
		r.Post("/tasks/{taskId}/fail", h.FailTask)
		r.Post("/tasks/{taskId}/usage", h.ReportTaskUsage)
		r.Post("/tasks/{taskId}/messages", h.ReportTaskMessages)
		r.Get("/tasks/{taskId}/messages", h.ListTaskMessages)

		r.Get("/issues/{issueId}/gc-check", h.GetIssueGCCheck)
		r.Get("/chat-sessions/{sessionId}/gc-check", h.GetChatSessionGCCheck)
		r.Get("/autopilot-runs/{runId}/gc-check", h.GetAutopilotRunGCCheck)
		r.Get("/tasks/{taskId}/gc-check", h.GetTaskGCCheck)

		r.Post("/runtimes/{runtimeId}/recover-orphans", h.RecoverOrphanedTasks)
		r.Post("/tasks/{taskId}/session", h.PinTaskSession)
	})

	// 0.5.18 M5: user-plugin artifact raw serve — accepts either Bearer auth
	// OR an HMAC-signed request (iframe/img/<a download> elements cannot
	// attach the Authorization header). The handler's signed branch verifies
	// the HMAC against the process-local secret and serves inline with
	// `X-Content-Type-Options: nosniff`; the unsigned branch falls back to
	// requireUserID and serves `Content-Disposition: attachment` (F-006).
	// Registered OUTSIDE the main Auth group with a sig-aware wrapper so
	// unsigned requests still get X-User-ID injected (need it for the F-006
	// attachment branch + future audit hooks) while signed requests bypass
	// Bearer auth at the chain level. (Discovered in 0.5.19 usability smoke —
	// initial fix moved the route out of the Auth group entirely, which
	// regressed the unsigned branch to 401 "user not authenticated" because
	// requireUserID reads X-User-ID set by Auth middleware.)
	r.With(pluginArtifactAuthOrSigned(queries, patCache)).Get(
		"/api/user-plugins/{slug}/artifacts/{artifactID}/raw",
		h.ServePluginArtifactRaw,
	)

	// Protected API routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(queries, patCache))
		// 0.5.29 P1-1 — synthesizer Round 7: MountExperimentalProxies
		// lives INSIDE the auth chain. Pre-0.5.29 it was mounted on
		// the public router "because the iframe is only reachable
		// from inside the app" — but R4 surfaced the pre-auth
		// credential oracle (MountExperimentalProxies sits before
		// the auth chain, and reverseProxyTo.Director does NOT
		// strip caller-supplied X-API-Key). Moving it INSIDE the
		// auth group closes the pre-auth reachability. The iframe
		// stays same-origin, so cookies are auto-attached; gating
		// on middleware.Auth is transparent to the renderer.
		handler.MountExperimentalProxies(r, h)
		// middleware.RefreshCloudFrontCookies removed with cloud-billing.

		// --- Experimental Labs surfaces (per-request, per-user gated) ---
		// Registered unconditionally but each wrapped in
		// RequireExperimentalFlag so the route is admitted only when the
		// caller has the flag on (per-user experimental_pref, with the
		// 0.3.18 blacklist forcing off). An off-flag caller gets 404 —
		// indistinguishable from a nonexistent route, so the surface
		// stays unenumerable. These live inside the auth group because
		// both the guard decision and the handlers depend on the
		// X-User-ID header that middleware.Auth injects.
		// 0.3.22 lab consolidation: claude_science_lab replaces the
		// 0.3.20 `claude_science_runtime` flag. The runtime handler keeps
		// its route prefix /api/experimental/claude-science-runtime/* for
		// wire-compat with the existing Skill adapter; only the gate moved.
		r.Group(func(r chi.Router) {
			r.Use(h.RequireExperimentalFlag("claude_science_lab"))
			handler.RegisterClaudeScienceRuntimeRoutes(r, h)
			// 0.3.24+: forecast SSE stream for the Claude Lab
			// `<ForecastTab />`.
			handler.RegisterClaudeLabForecastRoutes(r, h)
			// 0.3.29: Claude Lab tab data source — issue listing by
			// `lab_source`.
			handler.RegisterClaudeLabIssuesRoute(r, h)
			// 0.3.40: Claude Lab workbench context — single-call
			// fetch of issue + task timeline + agent comments + chat
			// session for the right-hand LabChatPanel. Reuses the
			// existing issue / agent / comment sqlc queries instead
			// of introducing per-issue lists — see lab.go header.
			handler.RegisterClaudeLabContextRoute(r, h)
			// 0.5.22: manual "Run research" endpoint for the
			// auto_dispatch=false catalog opt-out (claude_science_lab).
			// Bypasses the service-layer gates so the workbench's
			// "Run research" button can fire on demand.
			handler.RegisterClaudeScienceRunRoute(r, h)
		})
		r.Group(func(r chi.Router) {
			r.Use(h.RequireExperimentalFlag("llm_wiki_bridge"))
			handler.RegisterLLMWikiBridgeRoutes(r, h)
		})
		// 0.3.27 B3: Mythos Swarm direct invocation endpoint. Unlike the
		// install handler (which provisions the lab), this is the path
		// that actually runs the RDT loop and posts the coda summary as
		// an issue comment.
		r.Group(func(r chi.Router) {
			r.Use(h.RequireExperimentalFlag("mythos_swarm"))
			r.Post("/api/experimental/mythos-swarm/run", h.RunMythosSwarm)
			// 0.3.31: enhancer-mode supervise HTTP surface.
			// GET returns the current supervision_state JSONB for a
			// given run id; POST .../tick triggers an immediate
			// synchronous tick (used by the IssueLabsSection "立即
			// 检查" button).
			r.Get("/api/experimental/mythos-swarm/supervise/{runID}", h.GetMythosSuperviseState)
			r.Post("/api/experimental/mythos-swarm/supervise/{runID}/tick", h.PostMythosSuperviseTick)
		})
		// 0.5.21 swarm_topology: multi-agent role-graph topology. The
		// orchestrator runs in-process (Service.StartOrchestrator +
		// runOrchestratorLoop); no subprocess, no proxy. Mounted via
		// RegisterSwarmRoutes which wires POST /runs, POST .../interrupt,
		// GET .../state, and the issue-side reverse lookup GET
		// /api/issues/{id}/swarm-runs (also gated — if you can't run
		// the lab, the issue badge is hidden too).
		//
		// 0.5.21 fix: previously the routes were declared in
		// swarm_routes.go but never mounted, so every call 404'd.
		r.Group(func(r chi.Router) {
			r.Use(h.RequireExperimentalFlag("swarm_topology"))
			handler.RegisterSwarmRoutes(r, h)
		})
		// 0.3.29 Pythia Oracle per-issue forecast. Previously dead code:
		// RegisterPythiaIssueForecastRoutes / AttachPythiaIssueForecastMiddleware
		// had zero call sites, so the flagship 0.3.29 per-issue forecast
		// always 404'd. Now wired behind the pythia_oracle guard.
		r.Group(func(r chi.Router) {
			r.Use(h.RequireExperimentalFlag("pythia_oracle"))
			handler.AttachPythiaIssueForecastMiddleware(r, h)
			handler.RegisterPythiaIssueForecastRoutes(r)
		})
		// 0.5.18 M4: code_canvas issue-bound render + history surface.
		// Renders a pasted snippet through the code_canvas subprocess and
		// persists the canvas per issue. Gated like pythia_oracle; off-flag
		// the routes physically vanish. Note the proxy prefix
		// /experimental/code-canvas (no /api/) is mounted separately in
		// MountExperimentalProxies, so the /api/... paths here do not
		// collide with it.
		r.Group(func(r chi.Router) {
			r.Use(h.RequireExperimentalFlag("code_canvas"))
			handler.RegisterCodeCanvasRoutes(r, h)
		})

		// 0.3.45.1: agent_self_optimization history view endpoints.
		// Flag-gated inside the handlers themselves (returns 404 when
		// the flag is OFF) so the route table is uniform across the
		// localised fork regardless of which Labs the user has opted
		// into. Mirrors the pythia_oracle / mythos_swarm gating style
		// but without RequireExperimentalFlag middleware — this lab
		// has both a true-bypass runtime AND a user-facing history
		// surface, so the 404 path lives at the handler entry.
		r.Route("/api/experimental/self-opt", func(r chi.Router) {
			r.Get("/runs", h.ListSelfOptRuns)
			r.Post("/runs", h.TriggerSelfOptRun)
			r.Get("/runs/{id}", h.GetSelfOptRun)
			r.Post("/runs/{id}/cancel", h.CancelSelfOptRun)
			// 0.5.2: edit ledger (human-confirm tier). {id} routes must be
			// distinct from {id} above — chi matches by segment count, so
			// /edits and /runs are separate subtrees (no conflict).
			r.Get("/edits", h.ListSelfOptEdits)
			r.Post("/edits/{id}/apply", h.ApplySelfOptEdit)
			r.Post("/edits/{id}/reject", h.RejectSelfOptEdit)
			r.Post("/edits/{id}/ignore", h.IgnoreSelfOptEdit)
			r.Post("/edits/{id}/revert", h.RevertSelfOptEdit)
		})

		// 0.5.2: agent trust score + self-review ledger. Same gating style
		// as /api/experimental/self-opt (per-user flag check inside the
		// handlers; flag off → 404).
		r.Route("/api/experimental/trust", func(r chi.Router) {
			r.Get("/profiles", h.ListTrustProfiles)
			r.Get("/events", h.ListTrustEvents)
			r.Post("/{agentId}/correct", h.CorrectAgentTrust)
			r.Post("/{agentId}/review", h.ReviewAgentTrust)
		})

		// --- User-scoped routes (no workspace context required) ---
		r.Get("/api/me", h.GetMe)
		r.Patch("/api/me", h.UpdateMe)
		r.Patch("/api/me/onboarding", h.PatchOnboarding)
		r.Post("/api/me/onboarding/complete", h.CompleteOnboarding)
		// 0.3.66 (M3 PR2): JoinCloudWaitlist + cloud-waitlist route
		// removed. The endpoint is the only fork cloud-billing surface
		// left and was reachable from the web /download page; closing
		// it aligns with CLAUDE.md's "No cloud features" contract.
		// user.cloud_waitlist_* columns and User.CloudWaitlist*
		// model fields are kept (forward-only migration hard
		// constraint; 7 sqlc SELECTs reference them).
		// 0.3.66: DEPRECATED BootstrapOnboardingRuntime /
		// BootstrapOnboardingNoRuntime routes + their handler file
		// (onboarding_shim.go) and tests removed. v3 frontend creates
		// the Helper agent + starter issue via generic CreateAgent /
		// CreateIssue and only calls /complete here.
		r.Post("/api/cli-token", h.IssueCliToken)
		r.Post("/api/upload-file", h.UploadFile)
		r.Post("/api/feedback", h.CreateFeedback)
		// 0.5.0 fork wave 1 — client-usage daily heartbeat. Records one row
		// per (user, client_type, install_id, UTC date) for active/desktop usage
		// reporting. RequireHumanActor matches the upstream route gating
		// (a member, not an agent task queue, is reporting).
		r.With(handler.RequireHumanActor).Post("/api/client-usage", h.UpsertClientUsage)

		// Attachment download — user-scoped (auth-only), NOT
		// workspace-scoped. The handler self-resolves the workspace
		// from the attachment row and enforces membership inside, so
		// this route is callable as a native browser <img>/<video>
		// src that cannot attach X-Workspace-Slug / X-Workspace-ID
		// headers. Persisting `/api/attachments/<id>/download` into
		// comment markdown depends on this — see MUL-3130. The
		// metadata / delete endpoints below stay workspace-scoped
		// because they are JSON-API consumers that always have
		// workspace context.
		r.Get("/api/attachments/{id}/download", h.DownloadAttachment)

		r.Route("/api/workspaces", func(r chi.Router) {
			r.Get("/", h.ListWorkspaces)
			r.Post("/", h.CreateWorkspace)
			r.Route("/{id}", func(r chi.Router) {
				// Member-level access
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
					r.Get("/", h.GetWorkspace)
					r.Get("/members", h.ListMembersWithUser)
					r.Post("/leave", h.LeaveWorkspace)
					// (Workspace-scoped /invitations listing removed in the
					// localized build — invitations are disabled.)
					// Listing GitHub installations is member-visible so the
					// integrations tab no longer renders blank for non-admins;
					// the handler strips the management handle and adds a
					// can_manage hint so the UI can gate connect/disconnect.
					r.Get("/github/installations", h.ListGitHubInstallations)
					// Custom runtime profiles — listing/reading is member-visible
					// (the Runtime page renders for everyone; create/edit/delete
					// are admin-gated below).
					r.Get("/runtime-profiles", h.ListRuntimeProfiles)
					r.Get("/runtime-profiles/{profileId}", h.GetRuntimeProfile)
				})
				// Admin-level access
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
					r.Put("/", h.UpdateWorkspace)
					r.Patch("/", h.UpdateWorkspace)
					// (POST /members was the create-invitation endpoint —
					// removed in the localized build; invitations are disabled.)
					r.Route("/members/{memberId}", func(r chi.Router) {
						r.Patch("/", h.UpdateMember)
						r.Delete("/", h.DeleteMember)
					})
					// (DELETE /invitations/{id} removed; invitations are disabled.)
					// Custom runtime profile mutations (admin-only).
					r.Post("/runtime-profiles", h.CreateRuntimeProfile)
					r.Patch("/runtime-profiles/{profileId}", h.UpdateRuntimeProfile)
					r.Put("/runtime-profiles/{profileId}", h.UpdateRuntimeProfile)
					r.Delete("/runtime-profiles/{profileId}", h.DeleteRuntimeProfile)
				})
				// Owner-only access
				r.With(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner")).Delete("/", h.DeleteWorkspace)

				// GitHub integration — connect / disconnect remain admin-only;
				// the read-only list endpoint lives in the member-level group
				// above so non-admins can see the workspace's connection state.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
					r.Get("/github/connect", h.GitHubConnect)
					r.Delete("/github/installations/{installationId}", h.DeleteGitHubInstallation)
				})

				// Lark integration. Listing is member-visible (same
				// rationale as GitHub: the Integrations tab must
				// render for non-admins so they see "wired up by whom").
				// Install / revoke require admin to prevent a non-admin
				// from binding a Bot to a workspace agent or yanking
				// an installation out from under one.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceMemberFromURL(queries, "id"))
					r.Get("/lark/installations", h.ListLarkInstallations)
				})
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireWorkspaceRoleFromURL(queries, "id", "owner", "admin"))
					r.Delete("/lark/installations/{installationId}", h.RevokeLarkInstallation)
					// Device-flow scan-to-install. Begin opens a new
					// registration session against Lark and returns
					// the QR-code URL; the frontend dialog then polls
					// /install/{sessionId}/status until success or
					// terminal failure.
					r.Post("/lark/install/begin", h.BeginLarkInstall)
					r.Get("/lark/install/{sessionId}/status", h.GetLarkInstallStatus)
				})
			})
		})

		// Lark binding-token redemption. NOT workspace-scoped because
		// the redeemer hits this BEFORE they have any workspace
		// context — the redemption itself is what mints their
		// lark_user_binding row. Identity comes from the session;
		// the token only proves "this open_id requested binding," and
		// is combined with the logged-in user to create the mapping.
		r.Post("/api/lark/binding/redeem", h.RedeemLarkBindingToken)

		// (User-scoped /api/invitations/* routes removed in the localized
		// build — invitations are disabled.)

		r.Route("/api/tokens", func(r chi.Router) {
			r.Get("/", h.ListPersonalAccessTokens)
			r.Post("/", h.CreatePersonalAccessToken)
			r.Post("/current/renew", h.RenewCurrentPersonalAccessToken)
			r.Delete("/{id}", h.RevokePersonalAccessToken)
		})

		// Cloud Billing proxy. Same upstream service / port as
		// cloud-runtime — multica-cloud's Fleet and Billing share
		// :8080 and the same chi router. All routes here forward
		// to /api/v1/billing/* with X-User-ID stamped from the
		// authenticated context.
		//
		// User-scoped (account-level), NOT workspace-scoped — sits
		// outside the RequireWorkspaceMember group so a user can
		// inspect their balance, top up, and open the Billing Portal
		// without an active workspace selected. The upstream owner
		// model is single-user; X-Workspace-ID would be ignored even
		// if we sent it. The Stripe webhook is the public outlier
		// and lives outside the entire Auth group (see above).
		//
		// IMPORTANT — task-token actors are blocked here. The Auth
		// middleware happily turns an mat_ task token into a normal
		// X-User-ID stamp (so agents can comment, claim issues, etc.
		// as their owner), but billing is account-level and a running
		// agent reading its owner's balance / opening a checkout
		// session is the kind of lateral-movement we're explicitly
		// trying to prevent. handler.RequireHumanActor checks the
		// authoritative server-set X-Actor-Source header and 403s
		// any task-token request. See actor_guards.go for the full
		// rationale.
		// (Cloud billing routes removed in the localized build.)

		// --- Workspace-scoped routes (all require workspace membership) ---
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWorkspaceMember(queries))

			// Assignee frequency
			r.Get("/api/assignee-frequency", h.GetAssigneeFrequency)

			// Issues
			r.Route("/api/issues", func(r chi.Router) {
				r.Get("/search", h.SearchIssues)
				r.Get("/child-progress", h.ChildIssueProgress)
				r.Get("/children", h.ListChildrenByParents)
				r.Get("/grouped", h.ListGroupedIssues)
				r.Get("/", h.ListIssues)
				r.Post("/", h.CreateIssue)
				r.Post("/quick-create", h.QuickCreateIssue)
				r.Post("/preview-trigger", h.PreviewIssueTrigger)
				r.Post("/batch-update", h.BatchUpdateIssues)
				r.Post("/batch-delete", h.BatchDeleteIssues)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetIssue)
					r.Put("/", h.UpdateIssue)
					r.Delete("/", h.DeleteIssue)
					r.Post("/comments/trigger-preview", h.PreviewCommentTriggers)
					r.Post("/comments", h.CreateComment)
					r.Get("/comments", h.ListComments)
					r.Get("/timeline", h.ListTimeline)
					r.Get("/subscribers", h.ListIssueSubscribers)
					r.Post("/subscribe", h.SubscribeToIssue)
					r.Post("/unsubscribe", h.UnsubscribeFromIssue)
					r.Get("/active-task", h.GetActiveTaskForIssue)
					r.Post("/tasks/{taskId}/cancel", h.CancelTask)
					r.Post("/rerun", h.RerunIssue)
					r.Get("/task-runs", h.ListTasksByIssue)
					r.Get("/usage", h.GetIssueUsage)
					r.Post("/reactions", h.AddIssueReaction)
					r.Delete("/reactions", h.RemoveIssueReaction)
					r.Get("/attachments", h.ListAttachments)
					r.Get("/children", h.ListChildIssues)
					// 0.3.31: returns recent mythos runs for this
					// issue. The IssueLabsSection supervise panel
					// reads this to discover the run id for a
					// given enhancer-mode issue.
					r.Get("/mythos-runs", h.GetMythosRunsByIssue)
					r.Get("/labels", h.ListLabelsForIssue)
					r.Post("/labels", h.AttachLabel)
					r.Delete("/labels/{labelId}", h.DetachLabel)
					r.Get("/metadata", h.ListIssueMetadata)
					r.Put("/metadata/{key}", h.SetIssueMetadataKey)
					r.Delete("/metadata/{key}", h.DeleteIssueMetadataKey)
					r.Get("/pull-requests", h.ListPullRequestsForIssue)
				})
			})

			// Task messages (user-facing, not daemon auth)
			r.Get("/api/tasks/{taskId}/messages", h.ListTaskMessagesByUser)

			// Labels
			r.Route("/api/labels", func(r chi.Router) {
				r.Get("/", h.ListLabels)
				r.Post("/", h.CreateLabel)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetLabel)
					r.Put("/", h.UpdateLabel)
					r.Delete("/", h.DeleteLabel)
				})
			})

			// Issue status catalog (MUL-6243). Reads are open to any member —
			// every client needs the catalog to render a status. Writes are
			// gated to workspace owner/admin inside the handlers.
			r.Route("/api/issue-statuses", func(r chi.Router) {
				r.Get("/", h.ListIssueStatuses)
				r.Post("/", h.CreateIssueStatus)
				r.Route("/{id}", func(r chi.Router) {
					r.Patch("/", h.UpdateIssueStatus)
					r.Delete("/", h.ArchiveIssueStatus)
				})
			})

			// Projects
			r.Route("/api/projects", func(r chi.Router) {
				r.Get("/search", h.SearchProjects)
				r.Get("/", h.ListProjects)
				r.Post("/", h.CreateProject)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetProject)
					r.Put("/", h.UpdateProject)
					r.Delete("/", h.DeleteProject)
					r.Get("/resources", h.ListProjectResources)
					r.Post("/resources", h.CreateProjectResource)
					r.Put("/resources/{resourceId}", h.UpdateProjectResource)
					r.Delete("/resources/{resourceId}", h.DeleteProjectResource)
				})
			})

			// Squads
			r.Route("/api/squads", func(r chi.Router) {
				r.Get("/", h.ListSquads)
				r.Post("/", h.CreateSquad)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetSquad)
					r.Put("/", h.UpdateSquad)
					r.Delete("/", h.DeleteSquad)
					r.Get("/members", h.ListSquadMembers)
					r.Get("/members/status", h.ListSquadMemberStatus)
					r.Get("/issues", h.ListSquadIssues)
					r.Post("/members", h.AddSquadMember)
					r.Delete("/members", h.RemoveSquadMember)
					r.Patch("/members/role", h.UpdateSquadMemberRole)
				})
			})

			// Squad leader evaluation (writes to activity_log)
			r.Post("/api/issues/{id}/squad-evaluated", h.RecordSquadLeaderEvaluation)

			// Autopilots
			r.Route("/api/autopilots", func(r chi.Router) {
				r.Get("/", h.ListAutopilots)
				r.Post("/", h.CreateAutopilot)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetAutopilot)
					r.Patch("/", h.UpdateAutopilot)
					r.Delete("/", h.DeleteAutopilot)
					r.Post("/trigger", h.TriggerAutopilot)
					r.Get("/runs", h.ListAutopilotRuns)
					r.Get("/runs/{runId}", h.GetAutopilotRun)
					r.Get("/deliveries", h.ListAutopilotDeliveries)
					r.Get("/deliveries/{deliveryId}", h.GetAutopilotDelivery)
					r.Post("/deliveries/{deliveryId}/replay", h.ReplayAutopilotDelivery)
					r.Post("/triggers", h.CreateAutopilotTrigger)
					r.Route("/triggers/{triggerId}", func(r chi.Router) {
						r.Patch("/", h.UpdateAutopilotTrigger)
						r.Delete("/", h.DeleteAutopilotTrigger)
						r.Post("/rotate-webhook-token", h.RotateAutopilotTriggerWebhookToken)
						r.Put("/signing-secret", h.SetAutopilotTriggerSigningSecret)
					})
				})
			})

			// Pins
			r.Route("/api/pins", func(r chi.Router) {
				r.Get("/", h.ListPins)
				r.Post("/", h.CreatePin)
				r.Put("/reorder", h.ReorderPins)
				r.Delete("/{itemType}/{itemId}", h.DeletePin)
			})

			// Attachments
			r.Get("/api/attachments/{id}", h.GetAttachmentByID)
			// /api/attachments/{id}/download is registered in the
			// outer Auth-only group above so it can be loaded as a
			// native <img>/<video> src without workspace headers
			// (MUL-3130). The handler self-resolves the workspace
			// from the attachment row.
			r.Get("/api/attachments/{id}/content", h.GetAttachmentContent)
			r.Delete("/api/attachments/{id}", h.DeleteAttachment)

			// Comments
			r.Route("/api/comments/{commentId}", func(r chi.Router) {
				r.Put("/", h.UpdateComment)
				r.Delete("/", h.DeleteComment)
				r.Post("/resolve", h.ResolveComment)
				r.Delete("/resolve", h.UnresolveComment)
				r.Post("/reactions", h.AddReaction)
				r.Delete("/reactions", h.RemoveReaction)
			})

			// Agents
			r.Route("/api/agents", func(r chi.Router) {
				r.Get("/", h.ListAgents)
				r.Post("/", h.CreateAgent)
				// Agent templates: pre-configured instructions + skill refs.
				// Picking a template imports the referenced skills into the
				// workspace (find-or-create by name) and creates the agent
				// with the template's instructions in one transaction.
				r.Post("/from-template", h.CreateAgentFromTemplate)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetAgent)
					r.Put("/", h.UpdateAgent)
					r.Post("/archive", h.ArchiveAgent)
					r.Post("/restore", h.RestoreAgent)
					r.Post("/cancel-tasks", h.CancelAgentTasks)
					r.Get("/tasks", h.ListAgentTasks)
					r.Get("/skills", h.ListAgentSkills)
					r.Put("/skills", h.SetAgentSkills)
					r.Post("/skills/add", h.AddAgentSkills)
					// Dedicated env-management endpoint. Owner/admin only;
					// agent actors are denied. Every reveal / write is
					// audited to activity_log. See MUL-2600 and
					// internal/handler/agent_env.go.
					r.Get("/env", h.GetAgentEnv)
					r.Put("/env", h.UpdateAgentEnv)
				})
			})

			// Agent templates catalog (browse + detail). The Create flow
			// lives under /api/agents/from-template above; this route is for
			// the picker UI to list available templates.
			r.Route("/api/agent-templates", func(r chi.Router) {
				r.Get("/", h.ListAgentTemplates)
				r.Get("/{slug}", h.GetAgentTemplate)
			})

			// Skills
			r.Route("/api/skills", func(r chi.Router) {
				r.Get("/", h.ListSkills)
				r.Post("/", h.CreateSkill)
				r.Get("/search", h.SearchSkills)
				r.Post("/import", h.ImportSkill)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.GetSkill)
					r.Put("/", h.UpdateSkill)
					r.Delete("/", h.DeleteSkill)
					r.Get("/files", h.ListSkillFiles)
					r.Put("/files", h.UpsertSkillFile)
					r.Delete("/files/{fileId}", h.DeleteSkillFile)
				})
			})

			// Dashboard — workspace-wide token + run-time rollups for the
			// "/{slug}/dashboard" page. Optional ?project_id filter scopes
			// the rollup to a single project.
			r.Route("/api/dashboard", func(r chi.Router) {
				r.Get("/usage/daily", h.GetDashboardUsageDaily)
				r.Get("/usage/by-agent", h.GetDashboardUsageByAgent)
				r.Get("/agent-runtime", h.GetDashboardAgentRunTime)
				r.Get("/runtime/daily", h.GetDashboardRunTimeDaily)
			})

			// Runtimes
			r.Route("/api/runtimes", func(r chi.Router) {
				r.Get("/", h.ListAgentRuntimes)
				r.Route("/{runtimeId}", func(r chi.Router) {
					r.Patch("/", h.UpdateAgentRuntime)
					r.Get("/usage", h.GetRuntimeUsage)
					r.Get("/usage/by-agent", h.GetRuntimeUsageByAgent)
					r.Get("/usage/by-hour", h.GetRuntimeUsageByHour)
					r.Get("/activity", h.GetRuntimeTaskActivity)
					r.Post("/update", h.InitiateUpdate)
					r.Get("/update/{updateId}", h.GetUpdate)
					r.Post("/models", h.InitiateListModels)
					r.Get("/models/{requestId}", h.GetModelListRequest)
					r.Post("/local-skills", h.InitiateListLocalSkills)
					r.Get("/local-skills/{requestId}", h.GetLocalSkillListRequest)
					r.Post("/local-skills/import", h.InitiateImportLocalSkill)
					r.Get("/local-skills/import/{requestId}", h.GetLocalSkillImportRequest)
					r.Delete("/", h.DeleteAgentRuntime)
					// Cascade variant of DELETE: archive every active agent
					// bound to this runtime, cancel their tasks, then delete
					// the runtime — all in one transaction. Used by the
					// DeleteRuntimeDialog when the strict DELETE refused with
					// `runtime_has_active_agents` and the user confirmed the
					// cascade plan.
					r.Post("/archive-agents-and-delete", h.ArchiveAgentsAndDeleteRuntime)
				})
			})

			// (Cloud Runtime fleet proxy removed in the localized build.)

			// Tasks (user-facing, with ownership check)
			r.Post("/api/tasks/{taskId}/cancel", h.CancelTaskByUser)

			// Workspace-wide agent task snapshot for presence derivation:
			// every active task + each agent's most recent terminal task.
			r.Get("/api/agent-task-snapshot", h.ListWorkspaceAgentTaskSnapshot)

			// Workspace-wide daily agent activity (last 30d, anchored on
			// completed_at). Backs the Agents-list sparkline (trailing 7d
			// slice) AND the agent detail "Last 30 days" panel.
			r.Get("/api/agent-activity-30d", h.GetWorkspaceAgentActivity30d)

			// Workspace-wide 30-day run counts per agent for the Agents-list RUNS column.
			r.Get("/api/agent-run-counts", h.GetWorkspaceAgentRunCounts)

			r.Route("/api/chat/sessions", func(r chi.Router) {
				r.Post("/", h.CreateChatSession)
				r.Get("/", h.ListChatSessions)
				r.Route("/{sessionId}", func(r chi.Router) {
					r.Get("/", h.GetChatSession)
					r.Patch("/", h.UpdateChatSession)
					r.Delete("/", h.DeleteChatSession)
					r.Post("/messages", h.SendChatMessage)
					r.Get("/messages", h.ListChatMessages)
					r.Get("/messages/page", h.ListChatMessagesPage)
					r.Get("/pending-task", h.GetPendingChatTask)
					r.Post("/read", h.MarkChatSessionRead)
					r.Post("/pin", h.PinChatSession)
					r.Post("/unpin", h.UnpinChatSession)
				})
			})
			r.Get("/api/chat/pending-tasks", h.ListPendingChatTasks)

			// Experimental / Labs (0.3.6). Per-user opt-in flags surfacing
			// in Settings → Labs. Catalog lives in
			// server/internal/experimental/catalog.go. Auth: any signed-in
			// user; prefs are scoped to user_id, not workspace_id.
			r.Route("/api/experimental-flags", func(r chi.Router) {
				r.Get("/", h.ListExperimentalFlags)
				r.Patch("/{key}", h.UpdateExperimentalFlag)
			})

			// User plugin CRUD (0.3.60 Labs sandbox). Always available
			// to authenticated users — no RequireExperimentalFlag gate.
			r.Get("/api/user-plugins", h.ListUserPlugins)
			r.Post("/api/user-plugins", h.CreateUserPlugin)
			r.Put("/api/user-plugins/{slug}", h.UpdateUserPlugin)
			r.Delete("/api/user-plugins/{slug}", h.DeleteUserPlugin)

			// User plugin runtime (execution loop). Runs the plugin's
			// inline code in its persistent env dir and ingests emitted
			// files as artifacts. runtime_kind gates dispatch:
			// inline runs, subprocess → 501, none → 400.
			r.Post("/api/user-plugins/{slug}/run", h.RunUserPlugin)

			// User plugin artifact storage (0.3.60). Filesystem-backed
			// artifact index under ~/.multica/plugins/<slug>/artifacts/.
			r.Get("/api/user-plugins/{slug}/artifacts", h.ListPluginArtifacts)
			r.Post("/api/user-plugins/{slug}/artifacts", h.UploadPluginArtifact)
			// 0.5.18 M5: signed raw endpoint is registered OUTSIDE this Auth
			// group — see the comment above the protected-routes section.
			// 0.5.18 M5: mint a short-lived HMAC-signed URL so an
			// <img>/<iframe>/<a download> can load a file-backed artifact
			// without a Bearer header.
			r.Post("/api/user-plugins/{slug}/artifacts/{artifactID}/sign", h.SignPluginArtifact)
			r.Delete("/api/user-plugins/{slug}/artifacts/{artifactID}", h.DeletePluginArtifact)

			// 0.3.15: experimental-resource install / rollback endpoints.
			// Drives the per-lab lock table (PR 1) so the renderer can
			// show "已装载 N" / "已隐藏 N" badges per source, and PR 4
			// can wire flag toggles into these. Auth: any signed-in user;
			// the lab is global, not per-workspace.
			r.Route("/api/experimental-resources", func(r chi.Router) {
				r.Get("/{key}/status", h.GetExperimentalResourcesStatus)
				r.Post("/{key}/install", h.PostExperimentalResourcesInstall)
				r.Post("/{key}/rollback", h.PostExperimentalResourcesRollback)
				// 0.3.45.4: one-shot install-all. Walks every opted-in
				// flag and re-runs the install path so a user who
				// toggled the flag before the RunInstall hook landed
				// (commits before 23c5998) can self-heal without
				// toggling each flag off-then-on. Returns the per-
				// flag result so the renderer can show "5 of 5 labs
				// installed" / per-lab error messages.
				r.Post("/install-all", h.PostExperimentalResourcesInstallAll)
			})

			// Inbox
			r.Route("/api/inbox", func(r chi.Router) {
				r.Get("/", h.ListInbox)
				r.Get("/unread-count", h.CountUnreadInbox)
				r.Get("/unread-summary", h.UnreadInboxSummary)
				r.Post("/mark-all-read", h.MarkAllInboxRead)
				r.Post("/archive-all", h.ArchiveAllInbox)
				r.Post("/archive-all-read", h.ArchiveAllReadInbox)
				r.Post("/archive-completed", h.ArchiveCompletedInbox)
				r.Post("/{id}/read", h.MarkInboxRead)
				r.Post("/{id}/archive", h.ArchiveInboxItem)
			})

			// Notification preferences
			r.Route("/api/notification-preferences", func(r chi.Router) {
				r.Get("/", h.GetNotificationPreferences)
				r.Put("/", h.UpdateNotificationPreferences)
			})
		})
	})

	return r, h
}

// buildLarkConnector wires the real WS long-conn connector that talks
// to /callback/ws/endpoint directly with app_id/app_secret. The
// connector wraps every read with a ctx-cancel watchdog so lease loss /
// shutdown breaks the blocking ReadMessage in bounded time — the
// invariant §4.4 leans on. A single connector instance serves every
// installation; its Run is parameterized by the installation, so the
// feishuChannel hands it the per-installation row.
//
// If the endpoint fetcher fails to initialize (typically a malformed
// MULTICA_LARK_CALLBACK_BASE_URL), we log and fall back to the
// NoopConnector so the lease / supervisor lifecycle still exercises
// against real DB rows. Inbound messages are silently dropped until
// the config is fixed; the boot log labels the mode "noop" so the
// degraded state is visible.
//
// Returns the connector plus a short label for the boot log:
// "ws-long-conn" in the healthy case, "noop" in the fallback case.
func buildLarkConnector(installSvc *lark.InstallationService, apiClient lark.APIClient) (lark.EventConnector, string) {
	endpointFetcher, err := lark.NewHTTPConnectionTokenFetcher(lark.HTTPConnectionTokenConfig{
		BaseURL: strings.TrimSpace(os.Getenv("MULTICA_LARK_CALLBACK_BASE_URL")),
		Logger:  slog.Default(),
	})
	if err != nil {
		slog.Error("lark ws: endpoint fetcher init failed; falling back to noop", "error", err)
		return lark.NewNoopConnector(slog.Default()), "noop"
	}
	decoder := lark.NewLarkJSONFrameDecoder()
	dialer := lark.NewGorillaDialer()
	if proxyURL := strings.TrimSpace(os.Getenv("MULTICA_LARK_WS_PROXY_URL")); proxyURL != "" {
		dialer.ProxyURL = proxyURL
	}
	credsProvider := lark.CredentialsProviderFunc(func(ctx context.Context, inst lark.Installation) (lark.InstallationCredentials, error) {
		secret, err := installSvc.DecryptAppSecret(inst)
		if err != nil {
			return lark.InstallationCredentials{}, err
		}
		creds := lark.InstallationCredentials{
			AppID:     inst.AppID,
			AppSecret: secret,
			Region:    lark.RegionOrDefault(inst.Region),
		}
		if inst.TenantKey.Valid {
			creds.TenantKey = inst.TenantKey.String
		}
		return creds, nil
	})
	// Inbound enricher: expands quoted replies / forwarded bundles AND
	// prefetches a window of surrounding group history (MUL-3084) into the
	// agent's body via the IM API before dispatch. It shares the
	// connector's resolved credentials and runs under the connector's
	// EnrichTimeout so it cannot overrun the Lark long-conn ACK budget.
	enricher := lark.NewInboundEnricher(apiClient, lark.InboundEnricherConfig{
		RecentContextSize: lark.DefaultRecentContextSize,
		Logger:            slog.Default(),
	})
	conn, err := lark.NewWSLongConnConnector(lark.WSConnectorConfig{
		Dialer:              dialer,
		EndpointFetcher:     endpointFetcher,
		FrameDecoder:        decoder,
		Enricher:            enricher,
		CredentialsProvider: credsProvider,
		Logger:              slog.Default(),
	})
	if err != nil {
		slog.Error("lark ws: connector init failed; falling back to noop", "error", err)
		return lark.NewNoopConnector(slog.Default()), "noop"
	}
	return conn, "ws-long-conn"
}

// membershipChecker implements realtime.MembershipChecker using database queries.
type membershipChecker struct {
	queries *db.Queries
}

func (mc *membershipChecker) IsMember(ctx context.Context, userID, workspaceID string) bool {
	_, err := mc.queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: parseUUID(workspaceID),
	})
	return err == nil
}

// patResolver implements realtime.PATResolver using database queries.
// patCache is shared with the Auth and DaemonAuth middlewares so a token
// revoke through any path invalidates the cache for all of them. Nil
// cache is supported and degrades to direct DB lookups.
type patResolver struct {
	queries *db.Queries
	cache   *auth.PATCache
}

func (pr *patResolver) ResolveToken(ctx context.Context, token string) (string, bool) {
	hash := auth.HashToken(token)

	if userID, ok := pr.cache.Get(ctx, hash); ok {
		return userID, true
	}

	pat, err := pr.queries.GetPersonalAccessTokenByHash(ctx, hash)
	if err != nil {
		return "", false
	}

	userID := util.UUIDToString(pat.UserID)

	var expiresAt time.Time
	if pat.ExpiresAt.Valid {
		expiresAt = pat.ExpiresAt.Time
	}
	pr.cache.Set(ctx, hash, userID, auth.TTLForExpiry(time.Now(), expiresAt))

	// Cache miss = first WS auth in this TTL window. Refresh last_used_at;
	// subsequent connects within the window skip the write.
	go pr.queries.UpdatePersonalAccessTokenLastUsed(context.Background(), pat.ID)

	return userID, true
}

// parseUUID is a thin alias for util.MustParseUUID. Call sites here are all
// internal round-trips of DB-sourced UUIDs (e.g. issue.ID, e.ActorID), so an
// invalid value indicates a programming error and should panic loudly.
func parseUUID(s string) pgtype.UUID {
	return util.MustParseUUID(s)
}

// optionalUUID returns a NULL pgtype.UUID for an empty string and otherwise
// behaves like parseUUID. Use this for actor IDs on events where the producer
// may legitimately be a "system" actor with no member/agent attribution
// (e.g. GitHub webhook auto-status sync) — the activity_log and inbox_item
// tables both allow actor_id to be NULL.
func optionalUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	return util.MustParseUUID(s)
}

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

// pluginArtifactAuthOrSigned runs the standard Auth middleware EXCEPT when
// the request carries an HMAC signature in the query string (?sig=&exp=&uid=).
// Signed requests bypass Bearer auth at the chain level so <iframe>/<img>/
// <a download> elements (which cannot attach the Authorization header) can
// still load file-backed artifacts; the handler's signed branch verifies
// the HMAC against the process-local secret and serves inline with
// `X-Content-Type-Options: nosniff`. Unsigned requests go through normal
// Auth so the handler's `requireUserID` / `Content-Disposition: attachment`
// (F-006) path still has X-User-ID available. (0.5.18 M5.)
func pluginArtifactAuthOrSigned(queries *db.Queries, patCache *auth.PATCache) func(http.Handler) http.Handler {
	authMiddleware := middleware.Auth(queries, patCache)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("sig") != "" {
				next.ServeHTTP(w, r)
				return
			}
			authMiddleware(next).ServeHTTP(w, r)
		})
	}
}
