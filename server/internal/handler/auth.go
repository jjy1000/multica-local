package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SignupError represents signup restriction errors
type SignupError struct {
	Message string
}

func (e SignupError) Error() string {
	return e.Message
}

var ErrSignupProhibited = SignupError{Message: "user registration is disabled on this self-hosted instance"}
var ErrEmailNotAllowed = SignupError{Message: "email address or domain not allowed on this instance"}

const devVerificationCodeEnv = "MULTICA_DEV_VERIFICATION_CODE"

// supportedLanguages mirrors `SUPPORTED_LOCALES` in packages/core/i18n/types.ts.
// Keep both lists in sync when adding a locale — the user-controlled `language`
// field round-trips through GetMe back into i18n.changeLanguage(), so without
// validation an arbitrary string would persist and echo to every device.
var supportedLanguages = map[string]struct{}{
	"en":      {},
	"zh-Hans": {},
	"ko":      {},
	"ja":      {},
}

type UserResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	AvatarURL *string `json:"avatar_url"`
	Language  *string `json:"language"`
	// Pinned IANA tz; nil = no preference (use browser-detected tz).
	Timezone                *string         `json:"timezone"`
	OnboardedAt             *string         `json:"onboarded_at"`
	OnboardingQuestionnaire json.RawMessage `json:"onboarding_questionnaire"`
	StarterContentState     *string         `json:"starter_content_state"`
	ProfileDescription      string          `json:"profile_description"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
}

// MaxProfileDescriptionLen caps the user-supplied profile_description body.
// Picked at 2000 chars per MUL-2406: enough room for role / stack / a few
// preferences, short enough that injecting it into every agent brief
// doesn't move the needle on prompt cost.
const MaxProfileDescriptionLen = 2000

func userToResponse(u db.User) UserResponse {
	// JSONB column is []byte with DEFAULT '{}', so it's never nil at the DB
	// level. Defensive coalesce just in case a future ALTER makes the column
	// nullable and some row comes back with no default applied.
	q := u.OnboardingQuestionnaire
	if len(q) == 0 {
		q = []byte("{}")
	}
	return UserResponse{
		ID:                      uuidToString(u.ID),
		Name:                    u.Name,
		Email:                   u.Email,
		AvatarURL:               textToPtr(u.AvatarUrl),
		Language:                textToPtr(u.Language),
		Timezone:                textToPtr(u.Timezone),
		OnboardedAt:             timestampToPtr(u.OnboardedAt),
		OnboardingQuestionnaire: json.RawMessage(q),
		StarterContentState:     textToPtr(u.StarterContentState),
		ProfileDescription:      u.ProfileDescription,
		CreatedAt:               timestampToString(u.CreatedAt),
		UpdatedAt:               timestampToString(u.UpdatedAt),
	}
}

type LoginResponse struct {
	Token string       `json:"token"`
	User  UserResponse `json:"user"`
}

type SendCodeRequest struct {
	Email string `json:"email"`
}

type VerifyCodeRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func generateCode() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(buf[:]) % 1000000
	return fmt.Sprintf("%06d", n), nil
}

func isDevVerificationCode(code string) bool {
	if isProductionEnv() {
		return false
	}

	devCode := strings.TrimSpace(os.Getenv(devVerificationCodeEnv))
	if !isSixDigitCode(devCode) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(code), []byte(devCode)) == 1
}

func isProductionEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production")
}

func isSixDigitCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func (h *Handler) issueJWT(user db.User) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   uuidToString(user.ID),
		"email": user.Email,
		"name":  user.Name,
		"exp":   time.Now().Add(auth.AuthTokenTTL()).Unix(),
		"iat":   time.Now().Unix(),
	})
	return token.SignedString(auth.JWTSecret())
}

// findOrCreateUser returns the existing user for an email, or creates one if
// none exists. isNew reports whether this call created the user — the signup
// event fires on that edge, covering both the verification-code and Google
// OAuth entry points.
func (h *Handler) findOrCreateUser(ctx context.Context, email string) (user db.User, isNew bool, err error) {
	user, err = h.Queries.GetUserByEmail(ctx, email)
	isNew = isNotFound(err)
	if err != nil && !isNew {
		return db.User{}, false, err
	}

	if err := h.checkSignupAllowed(email, isNew); err != nil {
		return db.User{}, false, err
	}

	if !isNew {
		return user, false, nil
	}

	name := email
	if at := strings.Index(email, "@"); at > 0 {
		name = email[:at]
	}
	created, err := h.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:  name,
		Email: email,
	})
	if err != nil {
		return db.User{}, false, err
	}
	return created, true, nil
}

// signupSourceFromRequest reads the attribution cookie the web frontend
// sets on the first pageview (UTM + referrer bundle). The frontend writes
// a JSON string URL-encoded into the cookie value — Go does not
// auto-decode Cookie.Value, so we have to unescape here before the string
// lands in PostHog. Missing cookie / decode failures collapse to the
// empty string; that simply omits signup_source from the event rather
// than sending percent-encoded garbage. Never fall back to r.Referer() —
// the frontend has already sanitised attribution and a raw referer can
// leak OAuth code/state from the callback URL.
//
// The cap is the server-side defence against a client that manages to set
// an oversize cookie; it matches SIGNUP_SOURCE_MAX_LEN on the frontend.
const signupSourceMaxLen = 512

func signupSourceFromRequest(r *http.Request) string {
	c, err := r.Cookie("multica_signup_source")
	if err != nil || c == nil {
		return ""
	}
	decoded, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	if len(decoded) > signupSourceMaxLen {
		return ""
	}
	return decoded
}

func (h *Handler) checkSignupAllowed(email string, isNewUser bool) error {
	if !isNewUser {
		return nil // existing users always allowed to log in
	}

	email = strings.ToLower(email)
	domain := ""
	if at := strings.Index(email, "@"); at > 0 {
		domain = email[at+1:]
	}

	// 1. explicit email whitelist always wins
	if len(h.cfg.AllowedEmails) > 0 && contains(h.cfg.AllowedEmails, email) {
		return nil
	}

	// 2. domain whitelist always wins
	if len(h.cfg.AllowedEmailDomains) > 0 && contains(h.cfg.AllowedEmailDomains, domain) {
		return nil
	}

	// 3. general signup flag
	if !h.cfg.AllowSignup {
		return ErrSignupProhibited
	}

	// 4. if allowlists are set but didn't match, block
	if len(h.cfg.AllowedEmailDomains) > 0 || len(h.cfg.AllowedEmails) > 0 {
		return ErrSignupProhibited
	}

	return nil
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

func (h *Handler) SendCode(w http.ResponseWriter, r *http.Request) {
	slog.Info("email verification code rejected (localized build)", append(logger.RequestAttrs(r), "path", r.URL.Path)...)
	writeError(w, http.StatusGone, "Email verification is disabled in this build — use username login")
}

func (h *Handler) VerifyCode(w http.ResponseWriter, r *http.Request) {
	slog.Info("email verification code rejected (localized build)", append(logger.RequestAttrs(r), "path", r.URL.Path)...)
	writeError(w, http.StatusGone, "Email verification is disabled in this build — use username login")
}

// UsernameLoginRequest is the localized-build auth payload: just a username.
// The server synthesises a private `name@local` email so the existing
// `users.email` UNIQUE index is preserved without schema changes. The
// first login for a new name creates the user; subsequent logins return
// the same row. There is no password, no email verification, no SSO —
// the trade-off the user explicitly chose when this build was forked.
type UsernameLoginRequest struct {
	Name string `json:"name"`
}

// UsernameLogin creates-or-fetches a user keyed on the supplied name and
// returns a JWT identical to the previous email-code / Google flows. The
// synthesized email is the only piece of state the DB sees as
// "user-identifying"; the row's `name` is the human-facing handle. All
// downstream code (workspace list, queries, mutations) is unchanged
// because the issued JWT carries the same `sub` UUID.
func (h *Handler) UsernameLogin(w http.ResponseWriter, r *http.Request) {
	var req UsernameLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Keep names compact and printable; the longest of the social-handle
	// shape the rest of the UI assumes (~40) is the practical ceiling.
	if utf8.RuneCountInString(name) > 64 {
		writeError(w, http.StatusBadRequest, "name is too long (max 64 characters)")
		return
	}

	email := name + "@local"

	user, err := h.Queries.GetUserByEmail(r.Context(), email)
	isNew := isNotFound(err)
	if err != nil && !isNew {
		// Log the wrapped err before returning so a future PG outage
		// surfaces in slog with the actual cause (ECONNREFUSED, schema
		// mismatch, etc.) instead of being summarised as "failed to
		// lookup user". See memory pg-binary-tree-ship-wipe-2026-07-14.
		slog.Warn("auth lookup failed", "error", err, "name", req.Name)
		writeError(w, http.StatusInternalServerError, "failed to lookup user")
		return
	}

	if isNew {
		created, createErr := h.Queries.CreateUser(r.Context(), db.CreateUserParams{
			Name:  name,
			Email: email,
		})
		if createErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to create user")
			return
		}
		user = created
	}

	tokenString, err := h.issueJWT(user)
	if err != nil {
		slog.Warn("username login failed", append(logger.RequestAttrs(r), "error", err, "name", name)...)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("failed to set auth cookies", "error", err)
	}

	slog.Info("user logged in via username", append(logger.RequestAttrs(r), "user_id", uuidToString(user.ID), "name", name)...)
	writeJSON(w, http.StatusOK, LoginResponse{
		Token: tokenString,
		User:  userToResponse(user),
	})
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	user, err := h.Queries.GetUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	writeJSON(w, http.StatusOK, userToResponse(user))
}

type UpdateMeRequest struct {
	Name               *string `json:"name"`
	AvatarURL          *string `json:"avatar_url"`
	Language           *string `json:"language"`
	ProfileDescription *string `json:"profile_description"`
	// IANA tz to pin; "" clears back to NULL; nil leaves untouched.
	Timezone *string `json:"timezone"`
}

type GoogleLoginRequest struct {
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
}

type googleTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

type googleUserInfo struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// GoogleLogin is a stub. Localized build: Google OAuth is prohibited; the
// route is registered so the frontend can detect a 410 and stop offering
// the button, but every request is rejected. The original Google OAuth
// implementation is preserved in git history (pre-localization) and can
// be restored for an enterprise build if Google SSO is needed again.
func (h *Handler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	slog.Info("google login rejected (localized build)", append(logger.RequestAttrs(r), "path", r.URL.Path)...)
	writeError(w, http.StatusGone, "Google login is disabled in this build — use username login")
}

// IssueCliToken returns a fresh JWT for the authenticated user.
// This allows cookie-authenticated browser sessions to obtain a bearer token
// that can be handed off to the CLI via the cli_callback redirect.
func (h *Handler) IssueCliToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	user, err := h.Queries.GetUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	tokenString, err := h.issueJWT(user)
	if err != nil {
		slog.Warn("cli-token: failed to issue JWT", append(logger.RequestAttrs(r), "error", err, "user_id", userID)...)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": tokenString})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	auth.ClearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req UpdateMeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	currentUser, err := h.Queries.GetUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	name := currentUser.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
	}

	params := db.UpdateUserParams{
		ID:   currentUser.ID,
		Name: name,
	}
	if req.AvatarURL != nil {
		params.AvatarUrl = pgtype.Text{String: strings.TrimSpace(*req.AvatarURL), Valid: true}
	}
	if req.Language != nil {
		lang := strings.TrimSpace(*req.Language)
		if _, ok := supportedLanguages[lang]; !ok {
			writeError(w, http.StatusBadRequest, "unsupported language")
			return
		}
		params.Language = pgtype.Text{String: lang, Valid: true}
	}
	if req.ProfileDescription != nil {
		// Count runes, not bytes: 2000 chars of Chinese must not be rejected
		// as ~6000 bytes. utf8.RuneCountInString handles invalid UTF-8 by
		// counting each bad byte as one rune, which still bounds the column.
		desc := strings.TrimSpace(*req.ProfileDescription)
		if utf8.RuneCountInString(desc) > MaxProfileDescriptionLen {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("profile_description exceeds %d characters", MaxProfileDescriptionLen))
			return
		}
		params.ProfileDescription = pgtype.Text{String: desc, Valid: true}
	}

	if req.Timezone != nil {
		// Valid=false → column untouched; Valid=true + "" → clear to
		// NULL; Valid=true + IANA → set. Three-way semantics enforced
		// in the UpdateUser SQL CASE.
		tz := strings.TrimSpace(*req.Timezone)
		if tz != "" {
			if loc, err := time.LoadLocation(tz); err != nil || loc == nil {
				writeError(w, http.StatusBadRequest, "invalid timezone")
				return
			}
		}
		params.Timezone = pgtype.Text{String: tz, Valid: true}
	}

	updatedUser, err := h.Queries.UpdateUser(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	writeJSON(w, http.StatusOK, userToResponse(updatedUser))
}
