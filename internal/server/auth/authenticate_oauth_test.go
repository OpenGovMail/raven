package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"raven/internal/models"
	"raven/internal/server"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthenticateOAuth_NoTLSRejected(t *testing.T) {
	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	conn := server.NewMockConn()
	state := &models.ClientState{Authenticated: false}

	s.HandleAuthenticate(conn, "A900", []string{"A900", "AUTHENTICATE", "OAUTHBEARER", "Zm9v"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A900 NO Plaintext authentication disallowed without TLS") {
		t.Fatalf("expected TLS rejection, got: %s", response)
	}
}

func TestAuthenticateOAuth_ContinuationCancel(t *testing.T) {
	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{Authenticated: false}
	conn.AddReadData("*\r\n")

	s.HandleAuthenticate(conn, "A901", []string{"A901", "AUTHENTICATE", "XOAUTH2", "="}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "+ ") {
		t.Fatalf("expected continuation response, got: %s", response)
	}
	if !strings.Contains(response, "A901 BAD Authentication exchange cancelled") {
		t.Fatalf("expected cancellation response, got: %s", response)
	}
}

func TestAuthenticateOAuth_InvalidPayload(t *testing.T) {
	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{Authenticated: false}

	s.HandleAuthenticate(conn, "A902", []string{"A902", "AUTHENTICATE", "OAUTHBEARER", "not-base64"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A902 NO [AUTHENTICATIONFAILED] Invalid OAuth payload") {
		t.Fatalf("expected invalid payload response, got: %s", response)
	}
}

func TestAuthenticateOAuth_InvalidTokenRejected(t *testing.T) {
	tmpDir := t.TempDir()
	mustWriteConfig(t, tmpDir, "https://issuer.example.com", "https://jwks.example.com/jwks", []string{"raven-imap"})

	restore := mustChdir(t, tmpDir)
	defer restore()

	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	payload := fmt.Sprintf("user=user2\x01auth=Bearer %s\x01\x01", "not-a-jwt")
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))

	conn := server.NewMockTLSConn()
	state := &models.ClientState{Authenticated: false}

	s.HandleAuthenticate(conn, "A903", []string{"A903", "AUTHENTICATE", "XOAUTH2", encoded}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A903 NO [AUTHENTICATIONFAILED] Authentication failed") {
		t.Fatalf("expected auth failure, got: %s", response)
	}
	if state.Authenticated {
		t.Fatal("expected unauthenticated state")
	}
}

func TestAuthenticateOAuth_RejectsBareUsernameInSASLUser(t *testing.T) {
	tmpDir := t.TempDir()
	mustWriteConfig(t, tmpDir, "https://issuer.example.com", "https://jwks.example.com/jwks", []string{"raven-imap"})

	restore := mustChdir(t, tmpDir)
	defer restore()

	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	payload := fmt.Sprintf("user=user2\x01auth=Bearer %s\x01\x01", "not-a-jwt")
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))

	conn := server.NewMockTLSConn()
	state := &models.ClientState{Authenticated: false}

	s.HandleAuthenticate(conn, "A903A", []string{"A903A", "AUTHENTICATE", "XOAUTH2", encoded}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A903A NO [AUTHENTICATIONFAILED] Authentication failed") {
		t.Fatalf("expected auth failure, got: %s", response)
	}
	if state.Authenticated {
		t.Fatal("expected unauthenticated state")
	}
}

func TestAuthenticateOAuth_SuccessWithJWKS(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	issuer := "https://issuer.example.com"
	aud := "raven-imap"

	jwkN, jwkE := rsaPublicJWK(t, &priv.PublicKey)
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": "kid-auth-1",
				"n":   jwkN,
				"e":   jwkE,
			}},
		})
	}))
	defer jwksServer.Close()

	tmpDir := t.TempDir()
	mustWriteConfig(t, tmpDir, issuer, jwksServer.URL, []string{aud})

	restore := mustChdir(t, tmpDir)
	defer restore()

	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	token, err := signToken(priv, "kid-auth-1", jwt.MapClaims{
		"iss":      issuer,
		"aud":      []string{aud},
		"exp":      time.Now().Add(2 * time.Minute).Unix(),
		"username": "user2",
		"email":    "user2@example.com",
		"sub":      "subject-1",
	})
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	payload := fmt.Sprintf("user=user2@example.com\x01auth=Bearer %s\x01\x01", token)
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))

	conn := server.NewMockTLSConn()
	state := &models.ClientState{Authenticated: false}

	s.HandleAuthenticate(conn, "A904", []string{"A904", "AUTHENTICATE", "OAUTHBEARER", encoded}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A904 OK [CAPABILITY ") || !strings.Contains(response, "Authenticated") {
		t.Fatalf("expected successful auth response, got: %s", response)
	}
	if !state.Authenticated {
		t.Fatal("expected authenticated state")
	}
	if state.Email != "user2@example.com" {
		t.Fatalf("unexpected state email: %q", state.Email)
	}
}

func TestAuthenticateOAuth_RejectsSASLUserEmailMismatch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	issuer := "https://issuer.example.com"
	aud := "raven-imap"

	jwkN, jwkE := rsaPublicJWK(t, &priv.PublicKey)
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": "kid-auth-2",
				"n":   jwkN,
				"e":   jwkE,
			}},
		})
	}))
	defer jwksServer.Close()

	tmpDir := t.TempDir()
	mustWriteConfig(t, tmpDir, issuer, jwksServer.URL, []string{aud})

	restore := mustChdir(t, tmpDir)
	defer restore()

	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	token, err := signToken(priv, "kid-auth-2", jwt.MapClaims{
		"iss":      issuer,
		"aud":      []string{aud},
		"exp":      time.Now().Add(2 * time.Minute).Unix(),
		"username": "user2",
		"email":    "user2@opengovmail.example.com",
		"sub":      "subject-2",
	})
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	payload := fmt.Sprintf("user=user2@example.com\x01auth=Bearer %s\x01\x01", token)
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))

	conn := server.NewMockTLSConn()
	state := &models.ClientState{Authenticated: false}

	s.HandleAuthenticate(conn, "A905", []string{"A905", "AUTHENTICATE", "OAUTHBEARER", encoded}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A905 NO [AUTHENTICATIONFAILED] Authentication failed") {
		t.Fatalf("expected auth failure, got: %s", response)
	}
	if state.Authenticated {
		t.Fatal("expected unauthenticated state")
	}
}

// mustWriteConfig writes a raven.yaml under rootDir/config. extraYAML is appended as is.
// TestAuthenticateOAuth_DomainFromOrgUnit builds the mailbox address from a token that carries
// only a username and an OU, using the OU's configured domains.
func TestAuthenticateOAuth_DomainFromOrgUnit(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	issuer := "https://issuer.example.com"
	aud := "raven-imap"

	jwkN, jwkE := rsaPublicJWK(t, &priv.PublicKey)
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{"kty": "RSA", "kid": "kid-ou", "n": jwkN, "e": jwkE}},
		})
	}))
	defer jwksServer.Close()

	tests := []struct {
		name      string
		domains   string
		saslUser  string
		wantEmail string // empty when the login must fail
	}{
		{name: "single domain", domains: "[a.example.com]", saslUser: "user2@a.example.com", wantEmail: "user2@a.example.com"},
		{name: "several domains, SASL user picks one", domains: "[a.example.com, b.example.com]", saslUser: "user2@B.example.com", wantEmail: "user2@b.example.com"},
		{name: "several domains, SASL user picks none", domains: "[a.example.com, b.example.com]", saslUser: "user2@c.example.com"},
		{name: "OU not mapped", domains: "", saslUser: "user2@a.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapping := ""
			if tt.domains != "" {
				mapping = "organization_unit_domains:\n  ou-1: " + tt.domains + "\n"
			}

			tmpDir := t.TempDir()
			mustWriteConfig(t, tmpDir, issuer, jwksServer.URL, []string{aud}, mapping)
			restore := mustChdir(t, tmpDir)
			defer restore()

			s, cleanup := server.SetupTestServer(t)
			defer cleanup()

			token, err := signToken(priv, "kid-ou", jwt.MapClaims{
				"iss":      issuer,
				"aud":      []string{aud},
				"exp":      time.Now().Add(2 * time.Minute).Unix(),
				"username": "user2",
				"ouId":     "ou-1",
				"sub":      "subject-ou",
			})
			if err != nil {
				t.Fatalf("failed to sign token: %v", err)
			}

			payload := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", tt.saslUser, token)
			encoded := base64.StdEncoding.EncodeToString([]byte(payload))

			conn := server.NewMockTLSConn()
			state := &models.ClientState{}

			s.HandleAuthenticate(conn, "A906", []string{"A906", "AUTHENTICATE", "OAUTHBEARER", encoded}, state)

			response := conn.GetWrittenData()
			if tt.wantEmail == "" {
				if !strings.Contains(response, "A906 NO [AUTHENTICATIONFAILED]") || state.Authenticated {
					t.Fatalf("expected auth failure, got: %s", response)
				}
				return
			}
			if !strings.Contains(response, "A906 OK") || state.Email != tt.wantEmail {
				t.Fatalf("expected login as %q, got email %q and response: %s", tt.wantEmail, state.Email, response)
			}
		})
	}
}

func mustWriteConfig(t *testing.T, rootDir, issuer, jwksURL string, audience []string, extraYAML ...string) {
	t.Helper()

	configDir := filepath.Join(rootDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	var b strings.Builder
	b.WriteString("domain: \"example.com\"\n")
	b.WriteString("auth_server_url: \"http://auth-service:8080\"\n")
	b.WriteString(fmt.Sprintf("oauth_issuer_url: %q\n", issuer))
	b.WriteString(fmt.Sprintf("oauth_jwks_url: %q\n", jwksURL))
	b.WriteString("oauth_audience:\n")
	for _, a := range audience {
		b.WriteString(fmt.Sprintf("  - %q\n", a))
	}
	b.WriteString("oauth_clock_skew_seconds: 60\n")
	for _, extra := range extraYAML {
		b.WriteString(extra)
	}

	path := filepath.Join(configDir, "raven.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}
}

func mustChdir(t *testing.T, dir string) func() {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", dir, err)
	}
	return func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("failed to restore cwd: %v", err)
		}
	}
}

func signToken(priv *rsa.PrivateKey, kid string, claims jwt.MapClaims) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	return tok.SignedString(priv)
}

func rsaPublicJWK(t *testing.T, pub *rsa.PublicKey) (string, string) {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBytes := intToBytes(pub.E)
	e := base64.RawURLEncoding.EncodeToString(eBytes)
	return n, e
}

func intToBytes(v int) []byte {
	if v == 0 {
		return []byte{0}
	}
	buf := make([]byte, 0, 8)
	for v > 0 {
		buf = append([]byte{byte(v & 0xff)}, buf...)
		v >>= 8
	}
	return buf
}
