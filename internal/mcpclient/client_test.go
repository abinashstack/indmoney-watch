package mcpclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeTokens struct {
	tok      string
	forced   int
	newToken string
}

func (f *fakeTokens) AccessToken(context.Context) (string, error) { return f.tok, nil }
func (f *fakeTokens) ForceRefresh(context.Context) error {
	f.forced++
	f.tok = f.newToken
	return nil
}

func TestRetriesOnceAfter401WithRefreshedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	}))
	defer srv.Close()

	ts := &fakeTokens{tok: "revoked", newToken: "fresh"}
	c := New(srv.URL, ts)
	if _, err := c.Raw(context.Background(), "ping", nil); err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if ts.forced != 1 {
		t.Fatalf("ForceRefresh called %d times, want 1", ts.forced)
	}
}

func TestPersistent401IsNotRetriedForever(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, &fakeTokens{tok: "a", newToken: "b"})
	if _, err := c.Raw(context.Background(), "ping", nil); err != ErrUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if calls != 2 {
		t.Fatalf("server hit %d times, want 2", calls)
	}
}
