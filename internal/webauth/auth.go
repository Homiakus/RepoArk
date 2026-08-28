package webauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/Homiakus/repoark/internal/config"
)

type Role int

const (
	RoleNone Role = iota
	RoleViewer
	RoleOperator
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleOperator:
		return "operator"
	case RoleAdmin:
		return "admin"
	default:
		return "none"
	}
}

type Identity struct {
	Subject string   `json:"sub"`
	CSRF    string   `json:"csrf"`
	Email   string   `json:"email,omitempty"`
	Groups  []string `json:"groups,omitempty"`
	AMR     []string `json:"amr,omitempty"`
	ACR     string   `json:"acr,omitempty"`
	Role    string   `json:"role"`
	Expires int64    `json:"exp"`
}

type Manager struct {
	cfg      config.WebAuthConfig
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	aead     cipher.AEAD
}

func New(ctx context.Context, cfg config.WebAuthConfig) (*Manager, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if strings.ToLower(strings.TrimSpace(cfg.Mode)) != "oidc" {
		return nil, fmt.Errorf("unsupported web auth mode %q", cfg.Mode)
	}
	secret := strings.TrimSpace(os.Getenv(cfg.ClientSecretEnv))
	if secret == "" {
		return nil, fmt.Errorf("%s is not set", cfg.ClientSecretEnv)
	}
	session := os.Getenv(cfg.SessionKeyEnv)
	if session == "" {
		return nil, fmt.Errorf("%s is not set", cfg.SessionKeyEnv)
	}
	if len(session) < 32 {
		return nil, fmt.Errorf("%s must contain at least 32 bytes of high-entropy secret material", cfg.SessionKeyEnv)
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(session))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, oauth: oauth2.Config{ClientID: cfg.ClientID, ClientSecret: secret, Endpoint: provider.Endpoint(), RedirectURL: cfg.RedirectURL, Scopes: oidcScopes(cfg.Scopes)}, verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}), aead: aead}, nil
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) { m.beginLogin(w, r, false) }

// StepUp starts a fresh provider authentication ceremony. RepoArk still does
// not implement WebAuthn itself; the IdP owns the ceremony and must reflect the
// achieved assurance in amr/acr claims consumed after callback.
func (m *Manager) StepUp(w http.ResponseWriter, r *http.Request) { m.beginLogin(w, r, true) }

func (m *Manager) beginLogin(w http.ResponseWriter, r *http.Request, stepUp bool) {
	state := randomToken(32)
	nonce := randomToken(32)
	verifier := oauth2.GenerateVerifier()
	setOIDCTempCookie(w, m.cfg, "repoark_oidc_state", state)
	setOIDCTempCookie(w, m.cfg, "repoark_oidc_nonce", nonce)
	setOIDCTempCookie(w, m.cfg, "repoark_oidc_verifier", verifier)
	opts := []oauth2.AuthCodeOption{oauth2.AccessTypeOnline, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce)}
	if stepUp {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"))
		if len(m.cfg.StepUpACRValues) > 0 {
			opts = append(opts, oauth2.SetAuthURLParam("acr_values", strings.Join(m.cfg.StepUpACRValues, " ")))
		}
	}
	http.Redirect(w, r, m.oauth.AuthCodeURL(state, opts...), http.StatusFound)
}

func (m *Manager) Callback(w http.ResponseWriter, r *http.Request) error {
	stateCookie, err := r.Cookie("repoark_oidc_state")
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		return errors.New("OIDC state mismatch")
	}
	nonceCookie, err := r.Cookie("repoark_oidc_nonce")
	if err != nil || nonceCookie.Value == "" {
		return errors.New("OIDC nonce missing")
	}
	verifierCookie, err := r.Cookie("repoark_oidc_verifier")
	if err != nil || verifierCookie.Value == "" {
		return errors.New("OIDC PKCE verifier missing")
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		return errors.New("OIDC code missing")
	}
	tok, err := m.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(verifierCookie.Value))
	if err != nil {
		return err
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return errors.New("OIDC id_token missing")
	}
	idToken, err := m.verifier.Verify(r.Context(), raw)
	if err != nil {
		return err
	}
	if idToken.Nonce != nonceCookie.Value {
		return errors.New("OIDC nonce mismatch")
	}
	var claims struct {
		Subject string   `json:"sub"`
		Email   string   `json:"email"`
		Groups  []string `json:"groups"`
		AMR     []string `json:"amr"`
		ACR     string   `json:"acr"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return err
	}
	if claim := strings.TrimSpace(m.cfg.GroupClaim); claim != "" && claim != "groups" {
		var rawClaims map[string]json.RawMessage
		if err := idToken.Claims(&rawClaims); err != nil {
			return err
		}
		if rawGroups, ok := rawClaims[claim]; ok {
			if err := json.Unmarshal(rawGroups, &claims.Groups); err != nil {
				return fmt.Errorf("OIDC group claim %q is not a string array", claim)
			}
		} else {
			claims.Groups = nil
		}
	}
	role := roleForGroups(claims.Groups, m.cfg)
	if role == RoleNone {
		return errors.New("OIDC identity has no RepoArk role")
	}
	now := time.Now()
	sessionExpiry := now.Add(8 * time.Hour)
	if !idToken.Expiry.IsZero() && idToken.Expiry.Before(sessionExpiry) {
		sessionExpiry = idToken.Expiry
	}
	if !sessionExpiry.After(now) {
		return errors.New("OIDC id_token is already expired")
	}
	ident := Identity{Subject: claims.Subject, CSRF: randomToken(24), Email: claims.Email, Groups: claims.Groups, AMR: claims.AMR, ACR: claims.ACR, Role: role.String(), Expires: sessionExpiry.Unix()}
	sealed, err := m.seal(ident)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "repoark_session", Value: sealed, Path: "/", MaxAge: int(time.Until(sessionExpiry).Seconds()), HttpOnly: true, Secure: m.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
	clearOIDCTempCookie(w, m.cfg, "repoark_oidc_state")
	clearOIDCTempCookie(w, m.cfg, "repoark_oidc_nonce")
	clearOIDCTempCookie(w, m.cfg, "repoark_oidc_verifier")
	return nil
}

func setOIDCTempCookie(w http.ResponseWriter, cfg config.WebAuthConfig, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: 600, HttpOnly: true, Secure: cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
}

func clearOIDCTempCookie(w http.ResponseWriter, cfg config.WebAuthConfig, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
}

func (m *Manager) Logout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "repoark_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func (m *Manager) Identity(r *http.Request) (Identity, error) {
	c, err := r.Cookie("repoark_session")
	if err != nil {
		return Identity{}, err
	}
	return m.open(c.Value)
}

func (m *Manager) Authorize(r *http.Request, minimum Role, requireStepUp bool) (Identity, error) {
	id, err := m.Identity(r)
	if err != nil {
		return id, err
	}
	if roleFromString(id.Role) < minimum {
		return id, fmt.Errorf("role %s is insufficient", id.Role)
	}
	if requireStepUp && len(m.cfg.RequiredAMR) > 0 {
		got := map[string]bool{}
		for _, v := range id.AMR {
			got[strings.ToLower(v)] = true
		}
		for _, required := range m.cfg.RequiredAMR {
			if !got[strings.ToLower(strings.TrimSpace(required))] {
				return id, fmt.Errorf("OIDC step-up requirement %q missing from amr", required)
			}
		}
	}
	return id, nil
}

// ValidateCSRF binds browser mutations to the encrypted RepoArk session.
// SameSite cookies are retained as defense-in-depth, but are not treated as a
// complete CSRF control.
func (m *Manager) ValidateCSRF(r *http.Request, id Identity) error {
	provided := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	if provided == "" {
		if err := r.ParseForm(); err != nil {
			return err
		}
		provided = strings.TrimSpace(r.FormValue("_csrf"))
	}
	if id.CSRF == "" || provided == "" || len(provided) != len(id.CSRF) || subtle.ConstantTimeCompare([]byte(provided), []byte(id.CSRF)) != 1 {
		return errors.New("CSRF token mismatch")
	}
	return nil
}

func (m *Manager) Middleware(minimum Role, requireStepUp bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := m.Authorize(r, minimum, requireStepUp); err != nil {
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func roleForGroups(groups []string, cfg config.WebAuthConfig) Role {
	set := map[string]bool{}
	for _, g := range groups {
		set[strings.ToLower(strings.TrimSpace(g))] = true
	}
	match := func(xs []string) bool {
		for _, x := range xs {
			if set[strings.ToLower(strings.TrimSpace(x))] {
				return true
			}
		}
		return false
	}
	if match(cfg.AdminGroups) {
		return RoleAdmin
	}
	if match(cfg.OperatorGroups) {
		return RoleOperator
	}
	if match(cfg.ViewerGroups) {
		return RoleViewer
	}
	return RoleNone
}

func roleFromString(s string) Role {
	switch strings.ToLower(s) {
	case "admin":
		return RoleAdmin
	case "operator":
		return RoleOperator
	case "viewer":
		return RoleViewer
	}
	return RoleNone
}

func (m *Manager) seal(id Identity) (string, error) {
	b, err := json.Marshal(id)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := append(nonce, m.aead.Seal(nil, nonce, b, []byte("repoark-web-session-v1"))...)
	return base64.RawURLEncoding.EncodeToString(out), nil
}
func (m *Manager) open(v string) (Identity, error) {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return Identity{}, err
	}
	ns := m.aead.NonceSize()
	if len(raw) <= ns {
		return Identity{}, errors.New("invalid session")
	}
	plain, err := m.aead.Open(nil, raw[:ns], raw[ns:], []byte("repoark-web-session-v1"))
	if err != nil {
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal(plain, &id); err != nil {
		return Identity{}, err
	}
	if id.Expires <= time.Now().Unix() {
		return Identity{}, errors.New("session expired")
	}
	return id, nil
}
func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func oidcScopes(configured []string) []string {
	seen := map[string]bool{oidc.ScopeOpenID: true}
	out := []string{oidc.ScopeOpenID}
	for _, scope := range configured {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out
}

// StableGroups is useful in diagnostics/tests and avoids leaking provider order.
func StableGroups(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
