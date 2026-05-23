package oauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

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
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("register http %d: %s", resp.StatusCode, string(rb))
	}
	var c ClientCreds
	if err := json.Unmarshal(rb, &c); err != nil {
		return nil, fmt.Errorf("register decode: %w", err)
	}
	if c.ClientID == "" {
		return nil, fmt.Errorf("register: empty client_id (body=%s)", string(rb))
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
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			gotState := r.URL.Query().Get("state")
			code := r.URL.Query().Get("code")
			errParam := r.URL.Query().Get("error")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if errParam != "" {
				_, _ = io.WriteString(w, "<h1>Login failed</h1><p>"+errParam+"</p>")
				once.Do(func() { resCh <- result{err: fmt.Errorf("oauth error: %s", errParam)} })
				return
			}
			if gotState != state {
				_, _ = io.WriteString(w, "<h1>State mismatch</h1>")
				once.Do(func() { resCh <- result{err: fmt.Errorf("state mismatch")} })
				return
			}
			_, _ = io.WriteString(w, "<h1>Logged in. You can close this tab.</h1>")
			once.Do(func() { resCh <- result{code: code} })
		}),
	}
	go func() { _ = srv.Serve(ln) }()

	fmt.Printf("Opening browser for INDmoney login…\nIf it doesn't open, visit:\n  %s\n\n", authURL)
	_ = exec.Command("open", authURL).Start()

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
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("token http %d: %s", resp.StatusCode, string(rb))
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(rb, &raw); err != nil {
		return nil, fmt.Errorf("token decode: %w (body=%s)", err, string(rb))
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
