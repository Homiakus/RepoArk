package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	clientID     = "repoark-e2e"
	clientSecret = "repoark-e2e-secret"
)

type codeGrant struct {
	nonce       string
	challenge   string
	redirectURI string
	role        string
	amr         []string
	acr         string
}

type fakeIDP struct {
	issuer string
	key    *rsa.PrivateKey
	kid    string

	mu          sync.Mutex
	role        string
	codes       map[string]codeGrant
	stepUpCount int
	lastACR     string
}

func main() {
	idpAddr := envOr("REPOARK_E2E_IDP_ADDR", "127.0.0.1:19880")
	proxyAddr := envOr("REPOARK_E2E_PROXY_ADDR", "127.0.0.1:19789")
	upstream := envOr("REPOARK_E2E_UPSTREAM", "http://127.0.0.1:19788")
	issuer := "http://" + idpAddr

	idp, err := newFakeIDP(issuer)
	if err != nil {
		log.Fatal(err)
	}
	idpServer := &http.Server{
		Addr:              idpAddr,
		Handler:           idp.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	proxyServer, proxyListener, err := newTLSProxy(proxyAddr, upstream)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)
	go func() {
		log.Printf("fake OIDC provider listening on %s", issuer)
		if err := idpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("OIDC provider: %w", err)
		}
	}()
	go func() {
		log.Printf("TLS reverse proxy listening on https://%s -> %s", proxyAddr, upstream)
		if err := proxyServer.Serve(proxyListener); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("reverse proxy: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		log.Print(err)
		stop()
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = proxyServer.Shutdown(shutdown)
	_ = idpServer.Shutdown(shutdown)
}

func newFakeIDP(issuer string) (*fakeIDP, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(key.PublicKey.N.Bytes())
	return &fakeIDP{
		issuer: issuer,
		key:    key,
		kid:    base64.RawURLEncoding.EncodeToString(fingerprint[:9]),
		role:   "viewer",
		codes:  make(map[string]codeGrant),
	}, nil
}

func (p *fakeIDP) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("POST /__e2e/identity", p.setIdentity)
	mux.HandleFunc("GET /__e2e/status", p.status)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (p *fakeIDP) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.issuer,
		"authorization_endpoint":                p.issuer + "/authorize",
		"token_endpoint":                        p.issuer + "/token",
		"jwks_uri":                              p.issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "groups"},
	})
}

func (p *fakeIDP) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"kid": p.kid,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(p.key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.key.PublicKey.E)).Bytes()),
		}},
	})
}

func (p *fakeIDP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != clientID {
		http.Error(w, "invalid authorize request", http.StatusBadRequest)
		return
	}
	redirectURI := strings.TrimSpace(q.Get("redirect_uri"))
	state := strings.TrimSpace(q.Get("state"))
	nonce := strings.TrimSpace(q.Get("nonce"))
	challenge := strings.TrimSpace(q.Get("code_challenge"))
	if redirectURI == "" || state == "" || nonce == "" || challenge == "" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "missing OIDC/PKCE parameter", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	role := p.role
	amr := []string{"pwd"}
	acr := ""
	if q.Get("prompt") == "login" || strings.TrimSpace(q.Get("acr_values")) != "" {
		amr = []string{"pwd", "webauthn"}
		if values := strings.Fields(q.Get("acr_values")); len(values) > 0 {
			acr = values[0]
		}
		p.stepUpCount++
		p.lastACR = acr
	}
	code, err := randomToken(24)
	if err == nil {
		p.codes[code] = codeGrant{nonce: nonce, challenge: challenge, redirectURI: redirectURI, role: role, amr: amr, acr: acr}
	}
	p.mu.Unlock()
	if err != nil {
		http.Error(w, "code generation failed", http.StatusInternalServerError)
		return
	}

	callback, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect URI", http.StatusBadRequest)
		return
	}
	cq := callback.Query()
	cq.Set("code", code)
	cq.Set("state", state)
	callback.RawQuery = cq.Encode()
	http.Redirect(w, r, callback.String(), http.StatusFound)
}

func (p *fakeIDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid token request", http.StatusBadRequest)
		return
	}
	gotID, gotSecret, ok := r.BasicAuth()
	if !ok {
		gotID = r.FormValue("client_id")
		gotSecret = r.FormValue("client_secret")
	}
	if gotID != clientID || gotSecret != clientSecret || r.FormValue("grant_type") != "authorization_code" {
		http.Error(w, "invalid client or grant", http.StatusUnauthorized)
		return
	}

	code := strings.TrimSpace(r.FormValue("code"))
	p.mu.Lock()
	grant, exists := p.codes[code]
	if exists {
		delete(p.codes, code)
	}
	p.mu.Unlock()
	if !exists || grant.redirectURI != r.FormValue("redirect_uri") {
		http.Error(w, "invalid authorization code", http.StatusBadRequest)
		return
	}
	verifier := r.FormValue("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	if verifier == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge {
		http.Error(w, "PKCE verification failed", http.StatusBadRequest)
		return
	}

	idToken, err := p.signIDToken(grant)
	if err != nil {
		http.Error(w, "ID token signing failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "repoark-e2e-access",
		"token_type":   "Bearer",
		"expires_in":   300,
		"id_token":     idToken,
	})
}

func (p *fakeIDP) signIDToken(grant codeGrant) (string, error) {
	now := time.Now().UTC()
	groups := map[string][]string{
		"viewer":   {"repoark-viewers"},
		"operator": {"repoark-operators"},
		"admin":    {"repoark-admins"},
	}[grant.role]
	if len(groups) == 0 {
		return "", fmt.Errorf("unsupported role %q", grant.role)
	}
	header := map[string]any{"alg": "RS256", "kid": p.kid, "typ": "JWT"}
	claims := map[string]any{
		"iss":            p.issuer,
		"sub":            grant.role + "-user",
		"aud":            clientID,
		"exp":            now.Add(5 * time.Minute).Unix(),
		"iat":            now.Unix(),
		"auth_time":      now.Unix(),
		"nonce":          grant.nonce,
		"email":          grant.role + "@repoark.e2e",
		"email_verified": true,
		"groups":         groups,
		"amr":            grant.amr,
	}
	if grant.acr != "" {
		claims["acr"] = grant.acr
	}
	encodedHeader, err := encodeJSONSegment(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeJSONSegment(claims)
	if err != nil {
		return "", err
	}
	unsigned := encodedHeader + "." + encodedClaims
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (p *fakeIDP) setIdentity(w http.ResponseWriter, r *http.Request) {
	role := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("role")))
	switch role {
	case "viewer", "operator", "admin":
	default:
		http.Error(w, "role must be viewer, operator, or admin", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.role = role
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"role": role})
}

func (p *fakeIDP) status(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"role":          p.role,
		"step_up_count": p.stepUpCount,
		"last_acr":      p.lastACR,
	})
}

func newTLSProxy(addr, upstream string) (*http.Server, net.Listener, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		externalHost := r.Host
		originalDirector(r)
		r.Host = target.Host
		r.Header.Set("X-Forwarded-Host", externalHost)
		r.Header.Set("X-Forwarded-Proto", "https")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Set("X-RepoArk-E2E-Proxy", "1")
		return nil
	}

	cert, err := selfSignedCertificate()
	if err != nil {
		return nil, nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	tlsListener := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	server := &http.Server{
		Addr:              addr,
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server, tlsListener, nil
}

func selfSignedCertificate() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now().UTC()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "repoark-e2e.local"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost", "repoark-e2e.local"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func encodeJSONSegment(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
