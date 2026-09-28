package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	if got := normalizeEmail(" user@example.com. "); got != "user@example.com" {
		t.Fatalf("normalizeEmail() = %q, want %q", got, "user@example.com")
	}

	for _, input := range []string{"", "user", "@example.com", "user@", "a@b@c"} {
		if got := normalizeEmail(input); got != "" {
			t.Fatalf("normalizeEmail(%q) = %q, want empty", input, got)
		}
	}
}

func TestExtractBaseURL(t *testing.T) {
	base, err := extractBaseURL("https://example.com/auth/credentials/authenticate")
	if err != nil {
		t.Fatalf("extractBaseURL() unexpected error: %v", err)
	}
	if base != "https://example.com" {
		t.Fatalf("extractBaseURL() = %q, want %q", base, "https://example.com")
	}

	if _, err := extractBaseURL("not a url"); err == nil {
		t.Fatal("extractBaseURL() expected error for invalid URL")
	}
}

func TestFetchSystemAssertion(t *testing.T) {
	t.Run("returns the access token", func(t *testing.T) {
		t.Setenv("IDP_CLIENT_ID", "RAVEN_SYSTEM")
		t.Setenv("IDP_CLIENT_SECRET", "secret")

		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/oauth2/token" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if id, secret, ok := r.BasicAuth(); !ok || id != "RAVEN_SYSTEM" || secret != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"token-abc","expires_in":3600}`))
		}))
		defer srv.Close()

		if got := fetchSystemAssertion(srv.URL); got != "token-abc" {
			t.Fatalf("fetchSystemAssertion() = %q, want %q", got, "token-abc")
		}
	})

	t.Run("returns empty when no service account is configured", func(t *testing.T) {
		t.Setenv("IDP_CLIENT_ID", "")
		t.Setenv("IDP_CLIENT_SECRET", "")

		if got := fetchSystemAssertion("https://idp.example.com"); got != "" {
			t.Fatalf("fetchSystemAssertion() = %q, want empty", got)
		}
	})

	t.Run("returns empty when the token endpoint rejects the client", func(t *testing.T) {
		t.Setenv("IDP_CLIENT_ID", "RAVEN_SYSTEM")
		t.Setenv("IDP_CLIENT_SECRET", "wrong")

		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		if got := fetchSystemAssertion(srv.URL); got != "" {
			t.Fatalf("fetchSystemAssertion() = %q, want empty", got)
		}
	})
}

func TestResolveOrganizationUnitDomain(t *testing.T) {
	// The child's handle is unrelated to its parent's, and the parent is not a mail domain.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/organization-units/ou-child":
			_, _ = w.Write([]byte(`{"id":"ou-child","handle":"Silver.LSF.lk","parent":"ou-root"}`))
		case "/organization-units/ou-root":
			t.Errorf("parent OU fetched; an OU's domain must not depend on its parents")
			_, _ = w.Write([]byte(`{"id":"ou-root","handle":"foundations","parent":null}`))
		case "/organization-units/ou-grouping":
			_, _ = w.Write([]byte(`{"id":"ou-grouping","handle":"foundations","parent":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Run("handle is the domain", func(t *testing.T) {
		domain, err := resolveOrganizationUnitDomain(srv.URL, "ou-child", "token")
		if err != nil {
			t.Fatalf("resolveOrganizationUnitDomain() unexpected error: %v", err)
		}
		if domain != "silver.lsf.lk" {
			t.Fatalf("domain = %q, want %q", domain, "silver.lsf.lk")
		}
	})

	t.Run("handle that is not a domain", func(t *testing.T) {
		_, err := resolveOrganizationUnitDomain(srv.URL, "ou-grouping", "token")
		if err == nil || !strings.Contains(err.Error(), "not a mail domain") {
			t.Fatalf("expected not a mail domain error, got: %v", err)
		}
	})

	t.Run("unknown OU", func(t *testing.T) {
		if _, err := resolveOrganizationUnitDomain(srv.URL, "ou-missing", "token"); err == nil {
			t.Fatal("expected an error for an unknown OU")
		}
	})
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

func TestPostJSONAndGetJSON_ErrorAndSuccess(t *testing.T) {
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

	if err := getJSON(srv.URL+"/ok", "", &out); err != nil {
		t.Fatalf("getJSON() unexpected error: %v", err)
	}
	if out.Value != "done" {
		t.Fatalf("getJSON() decoded value = %q, want %q", out.Value, "done")
	}

	if err := getJSON(srv.URL+"/bad", "", &out); err == nil {
		t.Fatal("getJSON() expected error for non-2xx response")
	}
}

func TestResolveDomainFromOrganizationUnit_AdditionalBranches(t *testing.T) {
	if got := resolveDomainFromOrganizationUnit("not a url", "ou-1"); got != "" {
		t.Fatalf("expected empty result for invalid auth URL, got %q", got)
	}

	t.Setenv("IDP_CLIENT_ID", "RAVEN_SYSTEM")
	t.Setenv("IDP_CLIENT_SECRET", "secret")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if id, secret, ok := r.BasicAuth(); !ok || id != "RAVEN_SYSTEM" || secret != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"system-token","expires_in":3600}`))
		case "/organization-units/ou-child":
			if r.Header.Get("Authorization") != "Bearer system-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ou-child","handle":"opengovmail.example.com","parent":"ou-root"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, "not found: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	baseURL, err := extractBaseURL(srv.URL + "/auth/credentials/authenticate")
	if err != nil {
		t.Fatalf("extractBaseURL() unexpected error: %v", err)
	}
	if baseURL != srv.URL {
		t.Fatalf("extractBaseURL() = %q, want %q", baseURL, srv.URL)
	}

	if got := resolveDomainFromOrganizationUnit(srv.URL+"/auth/credentials/authenticate", "ou-child"); got != "opengovmail.example.com" {
		t.Fatalf("resolveDomainFromOrganizationUnit() = %q, want %q", got, "opengovmail.example.com")
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
