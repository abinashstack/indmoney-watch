package oauth

import "testing"

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
