package auth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"raven/internal/models"
)

func TestResolveMailboxEmailPriority(t *testing.T) {
	tests := []struct {
		name          string
		loginIdentity string
		authID        string
		domain        string
		want          string
	}{
		{
			name:          "login email wins",
			loginIdentity: " user@example.com ",
			authID:        "id@example.net",
			domain:        "opengovmail.example.com",
			want:          "user@example.com",
		},
		{
			name:          "auth id email used",
			loginIdentity: "user2",
			authID:        "user2@opengovmail.example.com",
			domain:        "example.com",
			want:          "user2@opengovmail.example.com",
		},
		{
			name:          "derived domain fallback",
			loginIdentity: "user2",
			authID:        "019cf0a6-114a",
			domain:        "opengovmail.example.com",
			want:          "user2@opengovmail.example.com",
		},
		{
			name:          "empty when nothing resolvable",
			loginIdentity: "",
			authID:        "019cf0a6-114a",
			domain:        "",
			want:          "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveMailboxEmail(tc.loginIdentity, tc.authID, tc.domain)
			if got != tc.want {
				t.Fatalf("resolveMailboxEmail() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	for input, want := range map[string]string{
		" user@example.com. ":    "user@example.com",
		"User@Silver.Example.LK": "User@silver.example.lk",
	} {
		if got := normalizeEmail(input); got != want {
			t.Fatalf("normalizeEmail(%q) = %q, want %q", input, got, want)
		}
	}

	for _, input := range []string{"", "user", "@example.com", "user@", "a@b@c"} {
		if got := normalizeEmail(input); got != "" {
			t.Fatalf("normalizeEmail(%q) = %q, want empty", input, got)
		}
	}
}

func TestEmailDomain(t *testing.T) {
	for input, want := range map[string]string{
		"user@example.com": "example.com",
		"user":             "",
		"":                 "",
	} {
		if got := emailDomain(input); got != want {
			t.Fatalf("emailDomain(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildAuthHTTPClient(t *testing.T) {
	client := buildAuthHTTPClient()
	if client == nil || client.Transport == nil {
		t.Fatal("buildAuthHTTPClient() returned nil client or transport")
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("AUTH_TEST_ENV", "")
	if got := getEnvOrDefault("AUTH_TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("getEnvOrDefault() = %q, want %q", got, "fallback")
	}

	t.Setenv("AUTH_TEST_ENV", " configured ")
	if got := getEnvOrDefault("AUTH_TEST_ENV", "fallback"); got != "configured" {
		t.Fatalf("getEnvOrDefault() = %q, want %q", got, "configured")
	}
}

func TestPostJSON_ErrorAndSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":"done"}`))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/bad":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out struct {
		Value string `json:"value"`
	}
	if err := postJSON(srv.URL+"/ok", map[string]string{"k": "v"}, "assertion", &out); err != nil {
		t.Fatalf("postJSON() unexpected error: %v", err)
	}
	if out.Value != "done" {
		t.Fatalf("postJSON() decoded value = %q, want %q", out.Value, "done")
	}

	if err := postJSON(srv.URL+"/empty", map[string]string{"k": "v"}, "", nil); err != nil {
		t.Fatalf("postJSON() with nil out unexpected error: %v", err)
	}

	if err := postJSON(srv.URL+"/bad", map[string]string{"k": "v"}, "", &out); err == nil {
		t.Fatal("postJSON() expected error for non-2xx response")
	}

}

func TestHandleSSLConnection_LoadCertFailureClosesConn(t *testing.T) {
	conn := &sslTrackingConn{}

	called := false
	HandleSSLConnection(func(_ net.Conn, _ *models.ClientState) {
		called = true
	}, conn)

	if called {
		t.Fatal("expected client handler not to be called on cert load failure")
	}
	if !conn.closed {
		t.Fatal("expected connection to be closed on cert load failure")
	}
}

type sslTrackingConn struct {
	closed bool
}

func (c *sslTrackingConn) Read(_ []byte) (int, error)         { return 0, os.ErrClosed }
func (c *sslTrackingConn) Write(b []byte) (int, error)        { return len(b), nil }
func (c *sslTrackingConn) Close() error                       { c.closed = true; return nil }
func (c *sslTrackingConn) LocalAddr() net.Addr                { return nil }
func (c *sslTrackingConn) RemoteAddr() net.Addr               { return nil }
func (c *sslTrackingConn) SetDeadline(_ time.Time) error      { return nil }
func (c *sslTrackingConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *sslTrackingConn) SetWriteDeadline(_ time.Time) error { return nil }
