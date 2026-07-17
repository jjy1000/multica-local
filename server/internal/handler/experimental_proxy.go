package handler

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// experimentalLoopback is kept as a process-local pointer to the
// Handler's ExperimentRegistry (0.3.19 P2). The desktop main process
// and the Go server still talk over HTTP for upstream registration
// (POST /__experimental/upstream), but the storage now lives in the
// registry's loopback map, not in a fixed {claudeScience, pythia}
// struct.
//
// The reason this exists: the renderer embeds the OpenScience UI in
// an iframe. Without same-origin, the iframe cannot share
// localStorage with the Multica renderer (and therefore cannot pick
// up our theme + i18n injections). Reverse-proxying OpenScience onto
// the Multica origin gives us same-origin while keeping the actual
// subprocess on a free loopback port.
//
// Writes happen over localhost HTTP, not via shared memory, because
// the desktop main process and the Go server are separate processes
// that already speak HTTP for everything else.
var experimentalLoopback struct {
	sync.RWMutex
	registry *experimental.Registry
}

// SetExperimentalLoopbackURL is called by the desktop main process
// after the manager spawns its subprocess. The Go server uses the
// URL to reverse-proxy the iframe traffic back to the same host on
// the manager's chosen port. Hot updates are supported — a restart
// that picks a different free port is reflected on the next request.
//
// 0.3.19 P2: the URL is stored in the registry's loopback map, not
// in a per-service struct field. Adding a third subprocess lab is a
// catalog edit, not a code change here.
func SetExperimentalLoopbackURL(service, u string) {
	experimentalLoopback.RLock()
	reg := experimentalLoopback.registry
	experimentalLoopback.RUnlock()
	if reg == nil {
		// Defensive: when the registry is not yet wired (older
		// boot paths, tests) we drop the URL on the floor rather
		// than fall back to a private struct. The renderer will
		// see 502 and the user can retry; the alternative (a
		// silent ghost URL) is worse.
		return
	}
	reg.SetLoopbackURL(service, u)
}

// AttachExperimentalRegistry binds the loopback storage to the given
// registry. Called once at boot by MountExperimentalProxies so
// SetExperimentalLoopbackURL can route writes to the registry map.
// The binding is a single pointer assignment, so the lock here is
// just to publish the pointer safely.
func AttachExperimentalRegistry(reg *experimental.Registry) {
	experimentalLoopback.Lock()
	defer experimentalLoopback.Unlock()
	experimentalLoopback.registry = reg
}

// claudeScienceProxyURL returns the manager's loopback URL, or "" if
// the manager is not up. Internal helper for the proxy handler.
func claudeScienceProxyURL() string {
	experimentalLoopback.RLock()
	reg := experimentalLoopback.registry
	experimentalLoopback.RUnlock()
	if reg == nil {
		return ""
	}
	return reg.LoopbackURL("claude_science")
}

// experimentalClaudeScienceProxy mounts a same-origin reverse proxy
// for the OpenScience (Claude Science) bundle. See the long-form
// comment in the companion server/internal/handler/experimental_proxy.go
// for the architecture rationale — short version: we reverse-proxy
// to make the iframe same-origin so the renderer can drive the
// OpenScience theme store (localStorage) and run a translation
// MutationObserver.
//
// Auth: registered on the PUBLIC router. The iframe is only
// reachable from inside the desktop app (which itself requires
// username-only login), so the loopback service itself is the auth
// boundary. OpenScience additionally enforces login / BYOK
// independently — see service/builtin_skills/multica-claude-science/SKILL.md.
func (h *Handler) experimentalClaudeScienceProxy(w http.ResponseWriter, r *http.Request) {
	upstream := claudeScienceProxyURL()
	if upstream == "" {
		writeError(w, http.StatusBadGateway,
			"claude science manager is not running — open the sidebar entry to start it")
		return
	}
	reverseProxyTo(w, r, upstream, "/experimental/claude-science")
}

// experimentalPythiaProxy mirrors the Claude Science proxy for the
// Pythia FastAPI service. We do not render Pythia inside an iframe
// (Pythia is headless), but the proxy lets the renderer pull status
// + URLs through the same Multica origin so the existing fetch calls
// in pythia-view.tsx stay same-origin.
func (h *Handler) experimentalPythiaProxy(w http.ResponseWriter, r *http.Request) {
	experimentalLoopback.RLock()
	reg := experimentalLoopback.registry
	experimentalLoopback.RUnlock()
	if reg == nil {
		writeError(w, http.StatusBadGateway,
			"pythia manager is not running — open the sidebar entry to start it")
		return
	}
	upstream := reg.LoopbackURL("pythia_oracle")
	if upstream == "" {
		writeError(w, http.StatusBadGateway,
			"pythia manager is not running — open the sidebar entry to start it")
		return
	}
	reverseProxyTo(w, r, upstream, "/experimental/pythia")
}

// reverseProxyTo forwards the request to upstream after stripping
// the routing prefix. Shared logic so Claude Science and Pythia
// don't each maintain their own director.
func reverseProxyTo(w http.ResponseWriter, r *http.Request, upstream, prefix string) {
	target, err := url.Parse(upstream)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid upstream URL")
		return
	}
	r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Strip the caller's session credentials before the request
		// leaves the Multica origin. The experimental upstreams are
		// local subprocesses (Pythia / OpenScience) that authenticate
		// via their own injected env token, never via the user's
		// Multica JWT. Forwarding Cookie/Authorization would let a
		// compromised or misregistered upstream harvest the user's
		// session — see the loopback-only guard in upstreamRegister.
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		// Carry workspace context (no-op today, future-proofing).
		if ws := r.Header.Get("X-Workspace-ID"); ws != "" {
			req.Header.Set("X-Workspace-ID", ws)
		}
		// Tell the upstream it is being embedded inside Multica so
		// it can suppress "open in browser" affordances.
		req.Header.Set("X-Multica-Embedded", "1")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeError(w, http.StatusBadGateway,
			"experimental upstream unreachable: "+err.Error())
	}
	proxy.ServeHTTP(w, r)
}

// MountExperimentalProxies registers the same-origin proxies on the
// public router. Called from cmd/server/router.go before the auth
// chain so the iframe can load without the desktop needing to send
// the JWT cookie for proxied subresources.
//
// 0.3.19 P2: the static route list is still hard-coded for the two
// shipped subprocess services (claude_science, pythia_oracle). The
// registry-driven mount (Registry.ProxyRoutes()) is wired by the
// router's experimental_resources block; the legacy paths stay for
// backward compat with the existing renderer's URL patterns.
//
// 0.3.19 P7: every proxied route also gets a middleware that
// injects the X-Experimental-Flag header so the safety net
// (ExperimentalFlagBurst middleware) can attribute 5xx responses
// to the right flag. This is the auto-mount contract: a new
// subprocess flag only needs a catalog edit + manifest + binary;
// the proxy wiring, the safety attribution, and the loopback
// registration are all picked up by iterating ProxyRoutes().
func MountExperimentalProxies(r chi.Router, h *Handler) {
	// Nil-handler guard: the fallback branch below dereferences
	// h.experimentalClaudeScienceProxy / h.experimentalPythiaProxy
	// (method values), which panic on a nil receiver. A test or an
	// older boot path that calls MountExperimentalProxies(r, nil)
	// should register nothing and let callers see a clean 404 rather
	// than crashing the process.
	if h == nil {
		return
	}
	// 0.3.19 P2: bind the loopback storage to the handler's
	// ExperimentRegistry. The legacy SetExperimentalLoopbackURL is
	// preserved as a thin wrapper around the registry so the
	// desktop main process's IPC contract is unchanged.
	//
	// 0.3.19 P7: safety attribution middleware. chi requires
	// Use() before any route registration on the same mux. But
	// the public router (cmd/server/router.go) already has routes
	// registered by the time it calls MountExperimentalProxies.
	// To avoid the "all middlewares must be defined before routes"
	// panic, we put the experimental routes on a child mux, group
	// the middleware on that child, THEN register routes — the
	// ordering constraint is per-mux, not cross-mux.
	expGroup := r.Group(func(exp chi.Router) {
		if h != nil && h.ExperimentRegistry != nil {
			AttachExperimentalRegistry(h.ExperimentRegistry)
			exp.Use(injectExperimentalFlagHeader(h.ExperimentRegistry))
		}
	})

	// 0.3.20: auto-mount every subprocess flag from the registry.
	// A new flag only needs a catalog entry + manifest — no router
	// edit. The two legacy hard-coded routes below remain for
	// backward compat with any caller that uses the same path
	// without going through Registry.ProxyRoutes().
	if h != nil && h.ExperimentRegistry != nil {
		for _, p := range h.ExperimentRegistry.ProxyRoutes() {
			mountExperimentalProxy(expGroup, h, p)
		}
	} else {
		// Fallback: registry not wired (older boot paths, tests).
		// Keep the 2 legacy routes so callers don't see 404.
		expGroup.HandleFunc("/experimental/claude-science/*", h.experimentalClaudeScienceProxy)
		expGroup.HandleFunc("/experimental/claude-science", h.experimentalClaudeScienceProxy)
		expGroup.HandleFunc("/experimental/pythia/*", h.experimentalPythiaProxy)
		expGroup.HandleFunc("/experimental/pythia", h.experimentalPythiaProxy)
	}

	// Internal upstream registry. The desktop main process POSTs
	// here whenever an experimental manager becomes ready; the proxy
	// handlers above read from the same registry. Path uses the
	// /__experimental prefix to mark it as internal-only — not
	// exposed via the public API surface and never reachable from
	// renderer code.
	expGroup.Post("/__experimental/upstream", h.upstreamRegister)
	expGroup.Delete("/__experimental/upstream/{service}", h.upstreamUnregister)
}

// mountExperimentalProxy registers the two routes for one
// ProxyRoute and binds a handler that reverse-proxies through the
// loopback URL keyed by the flag's LoopbackService. Generated
// rather than written by hand so a new subprocess flag lights up
// without touching this file.
func mountExperimentalProxy(exp chi.Router, h *Handler, p experimental.ProxyRoute) {
	prefix := experimental.NormalizeProxyPrefix(p.Prefix)
	if prefix == "" {
		return
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		upstream, ok := lookupLoopbackURL(h, p.LoopbackService)
		if !ok || upstream == "" {
			writeError(w, http.StatusBadGateway,
				p.LoopbackService+" manager is not running — open the sidebar entry to start it")
			return
		}
		reverseProxyTo(w, r, upstream, prefix)
	}
	exp.HandleFunc(prefix+"/*", handler)
	exp.HandleFunc(prefix, handler)
}

// lookupLoopbackURL resolves a service key to its loopback URL via
// the registry. Returns ok=false when the registry is not wired.
func lookupLoopbackURL(h *Handler, service string) (string, bool) {
	if h == nil || h.ExperimentRegistry == nil {
		return "", false
	}
	return h.ExperimentRegistry.LoopbackURL(service), true
}

// injectExperimentalFlagHeader returns a middleware that sets
// X-Experimental-Flag on every request whose path matches one of
// the registered ProxyRoutes. The header is a no-op for non-flag
// requests (paths the burst middleware would ignore anyway), so
// the cost is one map lookup per request.
func injectExperimentalFlagHeader(reg *experimental.Registry) func(http.Handler) http.Handler {
	if reg == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	// Snapshot the proxy routes once at mount time. New subprocess
	// flags added after boot require a router rebuild (same as the
	// proxy itself); the burst middleware does not need a dynamic
	// lookup.
	routes := reg.ProxyRoutes()
	prefixByService := make(map[string]string, len(routes))
	for _, p := range routes {
		prefixByService[p.Prefix] = p.FlagKey
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			for prefix, flagKey := range prefixByService {
				if len(req.URL.Path) >= len(prefix) && req.URL.Path[:len(prefix)] == prefix {
					req.Header.Set(experimentalHeader, flagKey)
					break
				}
			}
			next.ServeHTTP(w, req)
		})
	}
}

// experimentalHeader mirrors middleware.ExperimentalFlagHeader.
// We re-declare it here (rather than import) because the proxy
// package sits one level above the middleware package and the
// constant is a single string. Drift between the two is a contract
// bug; the burst middleware tests pin the value at the source.
const experimentalHeader = "X-Experimental-Flag"

// upstreamRegister accepts a {"service": "...", "url": "..."} body
// from the desktop main process and stores it in the in-process
// registry. Idempotent — a fresh spawn on a new port simply
// overwrites the previous entry.
//
// 0.3.20: the service allowlist is registry-driven. A subprocess
// flag is accepted when its LoopbackService appears in the
// registry's ProxyRoutes(). The hard-coded {claude_science,
// pythia_oracle} pair is the only valid set when the registry is
// not wired (older boot paths, tests).
func (h *Handler) upstreamRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Service string `json:"service"`
		URL     string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.URL == "" || body.Service == "" {
		writeError(w, http.StatusBadRequest, "service and url required")
		return
	}
	if !isAllowedUpstreamService(h, body.Service) {
		writeError(w, http.StatusBadRequest,
			"service not in registry's subprocess allowlist: "+body.Service)
		return
	}
	if !isLoopbackUpstreamURL(body.URL) {
		// SSRF guard: the upstream MUST be a local subprocess on the
		// loopback interface. Without this check any client that can
		// reach this endpoint could register an arbitrary external URL
		// and turn the reverse proxy into an open proxy that forwards
		// requests (and, before the Director strip above, the caller's
		// session) to an attacker-controlled host.
		writeError(w, http.StatusBadRequest,
			"upstream url must target a loopback address (127.0.0.0/8 or ::1)")
		return
	}
	SetExperimentalLoopbackURL(body.Service, body.URL)
	w.WriteHeader(http.StatusNoContent)
}

// isLoopbackUpstreamURL reports whether raw is an http(s) URL whose
// host resolves to a loopback address. Registration of any non-local
// upstream is rejected so the same-origin proxy can never be pointed
// at a remote host. A hostname other than "localhost" is refused
// outright — we do not perform DNS resolution here, both to avoid a
// blocking lookup on the request path and to prevent a DNS-rebinding
// bypass.
func isLoopbackUpstreamURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// upstreamUnregister clears the registry entry when a manager exits.
// The reverse proxy will return 502 with a structured error until a
// new manager registers a fresh URL.
func (h *Handler) upstreamUnregister(w http.ResponseWriter, r *http.Request) {
	service := chi.URLParam(r, "service")
	if !isAllowedUpstreamService(h, service) {
		writeError(w, http.StatusBadRequest, "unknown service: "+service)
		return
	}
	SetExperimentalLoopbackURL(service, "")
	w.WriteHeader(http.StatusNoContent)
}

// isAllowedUpstreamService checks whether service appears in the
// registry's subprocess proxy routes. The fallback set is the
// pre-0.3.20 hard-coded pair so older boots / tests still pass.
func isAllowedUpstreamService(h *Handler, service string) bool {
	if h != nil && h.ExperimentRegistry != nil {
		for _, p := range h.ExperimentRegistry.ProxyRoutes() {
			if p.LoopbackService == service {
				return true
			}
		}
		return false
	}
	return service == "claude_science" || service == "pythia_oracle"
}
