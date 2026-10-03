package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestReadOAuthErrorNeverEchoesBody(t *testing.T) {
	cases := map[string]string{
		`{"error":"invalid_grant","error_description":"refresh_token=SECRET"}`: "invalid_grant",
		`{"access_token":"SECRET"}`:        "no error code",
		`not json SECRET`:                  "no error code",
		`{"error":"bad\nSECRET <script>"}`: "unrecognized_error",
	}
	for body, want := range cases {
		if got := readOAuthError([]byte(body)); got != want {
			t.Errorf("readOAuthError(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestCallbackIgnoresForgedRequests(t *testing.T) {
	h, resCh := callbackHandler("real-state")
	hit := func(q string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/callback?"+q, nil))
		return rec.Code
	}

	// A third-party page navigating the browser here must not end the flow.
	for _, q := range []string{
		"error=access_denied",
		"state=wrong&error=access_denied",
		"state=wrong&code=attacker-code",
		"code=attacker-code",
	} {
		if code := hit(q); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, code)
		}
	}
	select {
	case r := <-resCh:
		t.Fatalf("forged request ended the login: %+v", r)
	default:
	}

	if code := hit("state=real-state&code=good"); code != http.StatusOK {
		t.Fatalf("legit callback: status %d", code)
	}
	r := <-resCh
	if r.err != nil || r.code != "good" {
		t.Fatalf("got %+v, want code=good", r)
	}
}

func TestCallbackReportsServerErrorWithoutEchoingIt(t *testing.T) {
	h, resCh := callbackHandler("s")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/callback?state=s&error="+url.QueryEscape("<script>x</script>\nlog-forge"), nil))
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("error param not HTML-escaped: %s", rec.Body.String())
	}
	r := <-resCh
	if r.err == nil || strings.ContainsAny(r.err.Error(), "<\n") {
		t.Fatalf("unexpected error %v", r.err)
	}
}
