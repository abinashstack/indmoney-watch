package oauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNeedsLogin signals that the OAuth server has rejected our refresh token
// (RFC 6749 invalid_grant) and silent recovery is impossible. The user must
// run `indw login -f` to start a fresh authorization flow. Daemons should
// treat this as a terminal error for the cycle and surface it to the user.
var ErrNeedsLogin = errors.New("oauth: refresh rejected, re-login required")

const (
	AuthorizeURL = "https://mcp.indmoney.com/authorize"
	TokenURL     = "https://mcp.indmoney.com/token"
	RegisterURL  = "https://mcp.indmoney.com/register"
	Scopes       = "portfolio:read market:read"
)

// httpClient is a bounded-timeout client used for all OAuth/registration
// requests. http.DefaultClient has no Timeout, so a hung TLS handshake or a
// stalled response body would block the CLI indefinitely (and, when invoked
// from launchd, hold up subsequent run-once cycles). 30 s is generous for an
// OAuth endpoint while still letting users notice and Ctrl-C.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// maxBody caps how much of an OAuth endpoint response we read. Token and
// registration responses are a few hundred bytes; 1 MiB is generous while
// still bounding memory if the server misbehaves.
const maxBody = 1 << 20

// oauthErrCode matches a conservative subset of RFC 6749 error codes. Used
// to decide whether a server- or URL-supplied error string is safe to echo
// into logs (no newlines, no long attacker-controlled text).
var oauthErrCode = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// safeErrCode returns code if it looks like a plain OAuth error code, else a
// placeholder. Never return raw response bodies from these endpoints: they
// may contain tokens or client secrets, and errors end up in agent.log.
func safeErrCode(code string) string {
	if oauthErrCode.MatchString(code) {
		return code
	}
	return "unrecognized_error"
}

// readOAuthError decodes the RFC 6749 §5.2 error code from a response body.
func readOAuthError(rb []byte) string {
	var oe struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rb, &oe)
	if oe.Error == "" {
		return "no error code"
	}
	return safeErrCode(oe.Error)
}

// ClientCreds is the result of dynamic client registration.
type ClientCreds struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// Tokens is what the daemon stores in Keychain.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
}

// Register performs RFC 7591 Dynamic Client Registration.
func Register(ctx context.Context, redirectURI string) (*ClientCreds, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                "indmoney-watch",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "client_secret_post",
		"scope":                      Scopes,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", RegisterURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("register read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("register http %d: %s", resp.StatusCode, readOAuthError(rb))
	}
	var c ClientCreds
	if err := json.Unmarshal(rb, &c); err != nil {
		// Don't wrap err: json syntax errors can quote body fragments, and the
		// body may carry client_secret.
		return nil, errors.New("register: malformed response")
	}
	if c.ClientID == "" {
		return nil, errors.New("register: empty client_id")
	}
	return &c, nil
}

// Login runs the full PKCE auth-code flow: starts a local callback server
// on the exact port encoded in redirectURI, opens the browser to /authorize,
// waits for the redirect, exchanges the code.
//
// redirectURI MUST exactly match the URI passed to Register (the IndMoney
// authorization server enforces strict equality).
func Login(ctx context.Context, creds *ClientCreds, redirectURI string) (*Tokens, string, error) {
	// PKCE.
	verifier := randomURLSafe(64)
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	state := randomURLSafe(24)

	u, err := url.Parse(redirectURI)
	if err != nil {
		return nil, "", fmt.Errorf("parse redirect_uri: %w", err)
	}
	if u.Host == "" {
		return nil, "", fmt.Errorf("redirect_uri must include host:port")
	}
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		return nil, "", fmt.Errorf("bind %s: %w", u.Host, err)
	}

	// Build auth URL.
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", creds.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", Scopes)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL := AuthorizeURL + "?" + q.Encode()

	// Start callback server.
	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)
	var once sync.Once
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			gotState := r.URL.Query().Get("state")
			code := r.URL.Query().Get("code")
			errParam := r.URL.Query().Get("error")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// Anyone who can navigate the user's browser (i.e. any web page)
			// can hit this endpoint during the login window. Requests that
			// don't carry our state are rejected WITHOUT ending the flow, so a
			// forged ?error= or ?state= can't abort a legitimate login.
			if subtle.ConstantTimeCompare([]byte(gotState), []byte(state)) != 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "<h1>State mismatch</h1><p>Ignored. Finish logging in from the original tab.</p>")
				return
			}
			if errParam != "" {
				_, _ = io.WriteString(w, "<h1>Login failed</h1><p>"+html.EscapeString(errParam)+"</p>")
				once.Do(func() { resCh <- result{err: fmt.Errorf("oauth error: %s", safeErrCode(errParam))} })
				return
			}
			if code == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "<h1>Missing authorization code</h1>")
				return
			}
			_, _ = io.WriteString(w, "<h1>Logged in. You can close this tab.</h1>")
			once.Do(func() { resCh <- result{code: code} })
		}),
	}
	go func() { _ = srv.Serve(ln) }()

	fmt.Printf("Opening browser for INDmoney login…\nIf it doesn't open, visit:\n  %s\n\n", authURL)
	_ = exec.Command("/usr/bin/open", authURL).Start()

	var got result
	select {
	case got = <-resCh:
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return nil, "", ctx.Err()
	case <-time.After(5 * time.Minute):
		_ = srv.Shutdown(context.Background())
		return nil, "", fmt.Errorf("login timeout")
	}
	_ = srv.Shutdown(context.Background())
	if got.err != nil {
		return nil, "", got.err
	}

	// Exchange code → token.
	tokens, err := exchangeCode(ctx, creds, redirectURI, got.code, verifier)
	if err != nil {
		return nil, "", err
	}
	tokens.ClientID = creds.ClientID
	tokens.ClientSecret = creds.ClientSecret
	return tokens, redirectURI, nil
}

func exchangeCode(ctx context.Context, creds *ClientCreds, redirectURI, code, verifier string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", creds.ClientID)
	if creds.ClientSecret != "" {
		form.Set("client_secret", creds.ClientSecret)
	}
	form.Set("code_verifier", verifier)
	return tokenRequest(ctx, form)
}

// Refresh uses a refresh token to get a fresh access token.
func Refresh(ctx context.Context, t *Tokens) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", t.RefreshToken)
	form.Set("client_id", t.ClientID)
	if t.ClientSecret != "" {
		form.Set("client_secret", t.ClientSecret)
	}
	nt, err := tokenRequest(ctx, form)
	if err != nil {
		return nil, err
	}
	if nt.RefreshToken == "" {
		nt.RefreshToken = t.RefreshToken
	}
	nt.ClientID = t.ClientID
	nt.ClientSecret = t.ClientSecret
	return nt, nil
}

func tokenRequest(ctx context.Context, form url.Values) (*Tokens, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("token read: %w", err)
	}
	if resp.StatusCode >= 400 {
		// Parse the standard OAuth error response (RFC 6749 §5.2). If the
		// server says invalid_grant, the refresh token is dead and silent
		// recovery isn't possible — wrap ErrNeedsLogin so callers can match
		// via errors.Is and trigger a re-login flow. Only the error code is
		// surfaced; the body is never echoed because it ends up in agent.log.
		code := readOAuthError(rb)
		if code == "invalid_grant" {
			return nil, fmt.Errorf("token http %d: %s: %w", resp.StatusCode, code, ErrNeedsLogin)
		}
		return nil, fmt.Errorf("token http %d: %s", resp.StatusCode, code)
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(rb, &raw); err != nil {
		// Never include the body or the decoder error: a successful token
		// response carries access_token and refresh_token.
		return nil, errors.New("token: malformed response")
	}
	if raw.AccessToken == "" {
		return nil, errors.New("token: empty access_token")
	}
	if raw.TokenType != "" && !strings.EqualFold(raw.TokenType, "bearer") {
		return nil, fmt.Errorf("token: unsupported token_type %q", safeErrCode(raw.TokenType))
	}
	return &Tokens{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second),
		Scope:        raw.Scope,
	}, nil
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
