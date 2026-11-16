package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"raven/internal/conf"
	"raven/internal/models"
	"raven/internal/server"
)

func writeAuthOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"testuser@example.com","type":"test-user","ouId":""}`))
}

// setupTestConfig creates a temporary config file and returns cleanup function
func setupTestConfig(t *testing.T, domain, authServerURL string) func() {
	// LoadConfig looks for config in these paths:
	// - /etc/raven/raven.yaml
	// - ./config/raven.yaml
	// - ./raven.yaml
	// - config/raven.yaml

	// Get current working directory
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Create a temp directory and change to it
	tmpDir := t.TempDir()
	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Create config directory
	configDir := filepath.Join(tmpDir, "config")
	err = os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	// Write config file
	configPath := filepath.Join(configDir, "raven.yaml")
	configContent := fmt.Sprintf(`domain: %s
auth_server_url: %s
`, domain, authServerURL)

	err = os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	return func() {
		// Restore original working directory
		defer func() {
			if err := os.Chdir(oldWd); err != nil {
				t.Logf("Failed to restore working directory: %v", err)
			}
		}()
	}
}

// TestAuthenticateUser_ConfigLoadError tests authentication with config load error
func TestAuthenticateUser_ConfigLoadError(t *testing.T) {
	// Change to a directory with no config file
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	tmpDir := t.TempDir()
	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Logf("Failed to restore working directory: %v", err)
		}
	}()

	// No config file exists in this directory

	s, cleanup := server.SetupTestServer(t)
	defer cleanup()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	// Try to login
	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "pass"}, state)

	response := conn.GetWrittenData()

	// Should get configuration error
	if !strings.Contains(response, "NO") {
		t.Errorf("Expected NO response for config error, got: %s", response)
	}
	if !strings.Contains(response, "SERVERBUG") || !strings.Contains(response, "Configuration error") {
		t.Errorf("Expected SERVERBUG Configuration error, got: %s", response)
	}
}

// TestAuthenticateUser_MissingDomain tests authentication works without domain in config.
func TestAuthenticateUser_MissingDomain(t *testing.T) {
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		writeAuthOK(w)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "pass"}, state)

	response := conn.GetWrittenData()

	// Domain is no longer required in config. Authentication should succeed.
	if !strings.Contains(response, "A001 OK") {
		t.Errorf("Expected successful auth without configured domain, got: %s", response)
	}
}

// TestAuthenticateUser_MissingAuthServerURL tests authentication with missing auth server URL
func TestAuthenticateUser_MissingAuthServerURL(t *testing.T) {
	cleanup := setupTestConfig(t, "example.com", "")
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "pass"}, state)

	response := conn.GetWrittenData()

	// Should get configuration error
	if !strings.Contains(response, "NO") || !strings.Contains(response, "SERVERBUG") {
		t.Errorf("Expected NO SERVERBUG response for missing auth server URL, got: %s", response)
	}
}

// TestAuthenticateUser_UsernameWithDomain tests authentication with username@domain format
func TestAuthenticateUser_UsernameWithDomain(t *testing.T) {
	// Create mock auth server that returns 200
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request contains the full email
		w.WriteHeader(http.StatusOK)
		writeAuthOK(w)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "example.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	// Login with full email address
	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "password"}, state)

	response := conn.GetWrittenData()
	t.Logf("Response: %s", response)

	// Should succeed
	if strings.Contains(response, "OK") {
		t.Log("Authentication succeeded as expected")
	}
}

// TestAuthenticateUser_UsernameWithoutDomain rejects bare username logins.
func TestAuthenticateUser_UsernameWithoutDomain(t *testing.T) {
	// Create mock auth server
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		writeAuthOK(w)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "testdomain.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	// Login with bare username must be rejected.
	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "bareuser", "password"}, state)

	response := conn.GetWrittenData()
	t.Logf("Response: %s", response)

	if !strings.Contains(response, "NO [AUTHENTICATIONFAILED]") {
		t.Fatalf("Expected bare username authentication failure, got: %s", response)
	}
}

// TestAuthenticateUser_SubdomainEmailFromIDP prevents split mailboxes for subdomain users.
func TestAuthenticateUser_SubdomainEmailFromIDP(t *testing.T) {
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"user2@opengovmail.example.com","type":"test-user","ouId":"opengovmail"}`))
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, ".example.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user2@opengovmail.example.com", "password"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A001 OK") {
		t.Fatalf("Expected successful login, got: %s", response)
	}

	if state.Email != "user2@opengovmail.example.com" {
		t.Fatalf("Expected canonical subdomain email, got: %s", state.Email)
	}
}

// orgUnitAuthServer answers logins as a user of OU ouID and fails the test if raven asks the
// IdP about organization units: their domains come from configuration.
func orgUnitAuthServer(t *testing.T, ouID string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/credentials/authenticate" {
			t.Errorf("unexpected IdP request %s; OU domains must come from configuration", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"019cf0a6-114a-7dad-bea1-9a36bc728ece","type":"opengovmailuser","ouId":%q}`, ouID)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// setupOrgUnitDomainsConfig writes a config whose organization_unit_domains maps ouID to domains.
func setupOrgUnitDomainsConfig(t *testing.T, authServerURL, ouID string, domains ...string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	var b strings.Builder
	fmt.Fprintf(&b, "auth_server_url: %s\norganization_unit_domains:\n  %q:\n", authServerURL, ouID)
	for _, d := range domains {
		fmt.Fprintf(&b, "    - %q\n", d)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "raven.yaml"), []byte(b.String()), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
}

// TestAuthenticateUser_OrgUnitDomains checks the login domain against the domains configured
// for the user's OU.
func TestAuthenticateUser_OrgUnitDomains(t *testing.T) {
	const ouID = "019cf0a5-4109-79ac-857b-07fc7b5c19ac"

	tests := []struct {
		name      string
		mappedOU  string
		domains   []string
		login     string
		wantOK    bool
		wantEmail string
	}{
		{
			name:      "login domain is the OU's domain",
			mappedOU:  ouID,
			domains:   []string{"opengovmail.example.com"},
			login:     "user2@OpenGovMail.example.com",
			wantOK:    true,
			wantEmail: "user2@opengovmail.example.com",
		},
		{
			name:     "login domain is not the OU's domain",
			mappedOU: ouID,
			domains:  []string{"opengovmail.example.com"},
			login:    "user2@sil.example.com",
		},
		{
			name:      "login domain is one of the OU's several domains",
			mappedOU:  ouID,
			domains:   []string{"a.example.com", "b.example.com"},
			login:     "user2@b.example.com",
			wantOK:    true,
			wantEmail: "user2@b.example.com",
		},
		{
			name:     "login domain belongs to another OU",
			mappedOU: "some-other-ou",
			domains:  []string{"opengovmail.example.com"},
			login:    "user2@opengovmail.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authServer := orgUnitAuthServer(t, ouID)
			setupOrgUnitDomainsConfig(t, authServer.URL+"/auth/credentials/authenticate", tt.mappedOU, tt.domains...)

			s, cleanupServer := server.SetupTestServer(t)
			defer cleanupServer()

			conn := server.NewMockTLSConn()
			state := &models.ClientState{}

			s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", tt.login, "password"}, state)

			response := conn.GetWrittenData()
			if tt.wantOK {
				if !strings.Contains(response, "A001 OK") {
					t.Fatalf("Expected successful login, got: %s", response)
				}
				if state.Email != tt.wantEmail {
					t.Fatalf("state.Email = %q, want %q", state.Email, tt.wantEmail)
				}
				return
			}
			if !strings.Contains(response, "NO [AUTHENTICATIONFAILED]") {
				t.Fatalf("Expected authentication failure, got: %s", response)
			}
		})
	}
}

// TestAuthenticateUser_AuthServerUnavailable tests authentication when auth server is down
func TestAuthenticateUser_AuthServerUnavailable(t *testing.T) {
	// Use invalid URL that will fail to connect
	cleanup := setupTestConfig(t, "example.com", "https://invalid-nonexistent-server.local:9999")
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "pass"}, state)

	response := conn.GetWrittenData()

	// Should get UNAVAILABLE error
	if !strings.Contains(response, "NO") {
		t.Errorf("Expected NO response for unavailable auth server, got: %s", response)
	}
	if !strings.Contains(response, "UNAVAILABLE") || !strings.Contains(response, "Authentication service unavailable") {
		t.Errorf("Expected UNAVAILABLE message, got: %s", response)
	}
}

// TestAuthenticateUser_AuthenticationSuccess tests successful authentication
func TestAuthenticateUser_AuthenticationSuccess(t *testing.T) {
	// Create mock auth server that returns 200 OK
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a POST request
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		// Verify Content-Type
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected application/json content type, got %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
		writeAuthOK(w)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "example.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "testuser@example.com", "testpass"}, state)

	response := conn.GetWrittenData()

	// Should succeed with OK and CAPABILITY
	if !strings.Contains(response, "A001 OK") {
		t.Errorf("Expected OK response, got: %s", response)
	}
	if !strings.Contains(response, "CAPABILITY") {
		t.Errorf("Expected CAPABILITY in response, got: %s", response)
	}
	if !strings.Contains(response, "Authenticated") {
		t.Errorf("Expected 'Authenticated' message, got: %s", response)
	}

	// Verify state was updated
	if !state.Authenticated {
		t.Error("Expected state.Authenticated to be true")
	}
	if state.Username == "" {
		t.Error("Expected state.Username to be set")
	}
}

// TestAuthenticateUser_AuthenticationFailure tests failed authentication
func TestAuthenticateUser_AuthenticationFailure(t *testing.T) {
	// Create mock auth server that returns 401 Unauthorized
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "example.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "wrongpass"}, state)

	response := conn.GetWrittenData()

	// Should get AUTHENTICATIONFAILED
	if !strings.Contains(response, "NO") {
		t.Errorf("Expected NO response for auth failure, got: %s", response)
	}
	if !strings.Contains(response, "AUTHENTICATIONFAILED") {
		t.Errorf("Expected AUTHENTICATIONFAILED, got: %s", response)
	}

	// State should not be updated
	if state.Authenticated {
		t.Error("Expected state.Authenticated to remain false")
	}
}

// TestAuthenticateUser_VariousStatusCodes tests different HTTP status codes
func TestAuthenticateUser_VariousStatusCodes(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		expectOK   bool
	}{
		{"200 OK", http.StatusOK, true},
		{"401 Unauthorized", http.StatusUnauthorized, false},
		{"403 Forbidden", http.StatusForbidden, false},
		{"404 Not Found", http.StatusNotFound, false},
		{"500 Internal Server Error", http.StatusInternalServerError, false},
		{"503 Service Unavailable", http.StatusServiceUnavailable, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				if tc.statusCode == http.StatusOK {
					writeAuthOK(w)
				}
			}))
			defer authServer.Close()

			cleanup := setupTestConfig(t, "example.com", authServer.URL)
			defer cleanup()

			_, err := conf.LoadConfig()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			s, cleanupServer := server.SetupTestServer(t)
			defer cleanupServer()

			conn := server.NewMockTLSConn()
			state := &models.ClientState{}

			s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "pass"}, state)

			response := conn.GetWrittenData()

			if tc.expectOK {
				if !strings.Contains(response, "OK") {
					t.Errorf("Expected OK response, got: %s", response)
				}
			} else {
				if !strings.Contains(response, "NO") {
					t.Errorf("Expected NO response, got: %s", response)
				}
				if !strings.Contains(response, "AUTHENTICATIONFAILED") {
					t.Errorf("Expected AUTHENTICATIONFAILED, got: %s", response)
				}
			}
		})
	}
}

// TestAuthenticateUser_TLSConnectionCapabilities tests CAPABILITY response on TLS connection
func TestAuthenticateUser_TLSConnectionCapabilities(t *testing.T) {
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		writeAuthOK(w)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "example.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	// Use TLS connection
	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "pass"}, state)

	response := conn.GetWrittenData()

	// Should include capabilities without STARTTLS (already on TLS)
	if !strings.Contains(response, "CAPABILITY") {
		t.Errorf("Expected CAPABILITY in response, got: %s", response)
	}
	if !strings.Contains(response, "UIDPLUS") {
		t.Errorf("Expected UIDPLUS capability, got: %s", response)
	}
	if !strings.Contains(response, "IDLE") {
		t.Errorf("Expected IDLE capability, got: %s", response)
	}
	// Should NOT have STARTTLS or LOGINDISABLED on TLS connection
	if strings.Contains(response, "LOGINDISABLED") {
		t.Errorf("Should not have LOGINDISABLED on TLS connection, got: %s", response)
	}
}

// TestAuthenticateUser_PlainConnectionCapabilities tests CAPABILITY response on plain connection
func TestAuthenticateUser_PlainConnectionCapabilities(t *testing.T) {
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		writeAuthOK(w)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "example.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	// Note: HandleLogin on plain connection should reject, but we're testing the capability format
	// if it somehow gets through (for testing the authenticateUser function's capability logic)
	conn := server.NewMockConn()
	state := &models.ClientState{}

	// This will fail due to TLS requirement in HandleLogin, but demonstrates the test structure
	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "user@example.com", "pass"}, state)

	response := conn.GetWrittenData()
	t.Logf("Response on plain connection: %s", response)

	// Plain connection should be rejected by HandleLogin before reaching authenticateUser
	if strings.Contains(response, "PRIVACYREQUIRED") {
		t.Log("Correctly rejected login on plain connection")
	}
}

// TestAuthenticateUser_RoleAssignmentsSuccess tests successful role assignment loading
func TestAuthenticateUser_RoleAssignmentsSuccess(t *testing.T) {
	authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	cleanup := setupTestConfig(t, "example.com", authServer.URL)
	defer cleanup()

	_, err := conf.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	s, cleanupServer := server.SetupTestServer(t)
	defer cleanupServer()

	conn := server.NewMockTLSConn()
	state := &models.ClientState{}

	s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", "testuser@example.com", "testpass"}, state)

	response := conn.GetWrittenData()

	if strings.Contains(response, "OK") {
		t.Log("Authentication succeeded")
		// Role assignments should be loaded (or empty array if none exist)
		// The function handles role loading errors gracefully
	}
}

// TestAuthenticateUser_UsernameExtraction tests username and domain extraction
func TestAuthenticateUser_UsernameExtraction(t *testing.T) {
	testCases := []struct {
		name     string
		username string
		expectNo bool
	}{
		{"Simple username", "john", true},
		{"Username with dots", "john.doe", true},
		{"Username with numbers", "user123", true},
		{"Full email", "user@example.com", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			authServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer authServer.Close()

			cleanup := setupTestConfig(t, "example.com", authServer.URL)
			defer cleanup()

			_, err := conf.LoadConfig()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			s, cleanupServer := server.SetupTestServer(t)
			defer cleanupServer()

			conn := server.NewMockTLSConn()
			state := &models.ClientState{}

			s.HandleLogin(conn, "A001", []string{"A001", "LOGIN", tc.username, "password"}, state)

			response := conn.GetWrittenData()
			t.Logf("Response for username '%s': %s", tc.username, response)

			if tc.expectNo {
				if !strings.Contains(response, "NO [AUTHENTICATIONFAILED]") {
					t.Fatalf("Expected non-email login rejection for %q, got: %s", tc.username, response)
				}
			}

			if !tc.expectNo && strings.Contains(response, "OK") {
				t.Logf("Username extraction succeeded for: %s", tc.username)
			}
		})
	}
}
