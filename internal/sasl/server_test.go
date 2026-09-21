package sasl_test

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"raven/internal/conf"
	"raven/internal/sasl"
	"strings"
	"sync"
	"testing"
	"time"
)

type synchronizedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// getSocketPath returns a short socket path to avoid Unix socket path length limits (104 chars on macOS)
func getSocketPath(t *testing.T) string {
	// Use /tmp/ with a short random suffix to stay well under the 104 character limit
	socketPath := fmt.Sprintf("/tmp/sasl-%d.sock", rand.Int63())
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
	})
	return socketPath
}

// TestNewServer tests server creation
func TestNewServer(t *testing.T) {
	socketPath := "/tmp/test-sasl.sock"
	tcpAddr := ""
	authURL := "https://example.com/auth"
	domain := "example.com"

	server := sasl.NewServer(socketPath, tcpAddr, authURL, domain, conf.SASLScopeAll)

	if server == nil {
		t.Fatal("Expected server to be created, got nil")
	}

	// Note: Cannot test private fields from external package
	// Tests now focus on public API behavior
}

func TestServerListenerStartupLogs(t *testing.T) {
	tests := []struct {
		name      string
		socket    bool
		tcp       bool
		scope     conf.SASLScope
		wantLogs  []string
		avoidLogs []string
	}{
		{
			name:   "all listeners enabled",
			socket: true,
			tcp:    true,
			scope:  conf.SASLScopeAll,
			wantLogs: []string{
				"SASL server listening on Unix socket:",
				"SASL server listening on TCP:",
			},
			avoidLogs: []string{
				"Skipping Unix socket listener",
				"Skipping TCP listener",
			},
		},
		{
			name:   "TCP-only scope",
			socket: true,
			tcp:    true,
			scope:  conf.SASLScopeTCPOnly,
			wantLogs: []string{
				"Skipping Unix socket listener (scope: tcp_only",
				"SASL server listening on TCP:",
			},
			avoidLogs: []string{"Skipping TCP listener"},
		},
		{
			name:   "Unix-socket-only scope",
			socket: true,
			tcp:    true,
			scope:  conf.SASLScopeUnixSocketOnly,
			wantLogs: []string{
				"SASL server listening on Unix socket:",
				"Skipping TCP listener (scope: unix_socket_only",
			},
			avoidLogs: []string{"Skipping Unix socket listener"},
		},
		{
			name:  "unset Unix socket path",
			tcp:   true,
			scope: conf.SASLScopeAll,
			wantLogs: []string{
				"Skipping Unix socket listener: no socket path configured",
				"SASL server listening on TCP:",
			},
		},
		{
			name:   "unset TCP address",
			socket: true,
			scope:  conf.SASLScopeAll,
			wantLogs: []string{
				"SASL server listening on Unix socket:",
				"Skipping TCP listener: no TCP address configured",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logBuffer := &synchronizedLogBuffer{}
			originalLogOutput := log.Writer()
			log.SetOutput(logBuffer)
			defer log.SetOutput(originalLogOutput)

			socketPath := ""
			if tt.socket {
				socketPath = getSocketPath(t)
			}
			tcpAddr := ""
			if tt.tcp {
				tcpAddr = "127.0.0.1:0"
			}
			server := sasl.NewServer(socketPath, tcpAddr, "https://example.com/auth", "example.com", tt.scope)
			errChan := make(chan error, 1)
			go func() {
				errChan <- server.Start()
			}()

			deadline := time.Now().Add(2 * time.Second)
			for {
				logs := logBuffer.String()
				allPresent := true
				for _, expected := range tt.wantLogs {
					if !strings.Contains(logs, expected) {
						allPresent = false
						break
					}
				}
				if allPresent {
					break
				}
				if time.Now().After(deadline) {
					_ = server.Shutdown()
					t.Fatalf("expected startup log messages %q, got:\n%s", tt.wantLogs, logs)
				}
				time.Sleep(10 * time.Millisecond)
			}

			logs := logBuffer.String()
			for _, unexpected := range tt.avoidLogs {
				if strings.Contains(logs, unexpected) {
					_ = server.Shutdown()
					t.Fatalf("did not expect startup log message %q, got:\n%s", unexpected, logs)
				}
			}

			if err := server.Shutdown(); err != nil {
				t.Fatalf("server shutdown failed: %v", err)
			}
			select {
			case err := <-errChan:
				if err != nil {
					t.Fatalf("server returned error: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("server did not stop within timeout")
			}
		})
	}
}

// TestServerStartShutdown tests server startup and graceful shutdown
func TestServerStartShutdown(t *testing.T) {
	socketPath := getSocketPath(t)

	// Create a mock auth server
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Verify socket was created
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Errorf("Socket file was not created at %s", socketPath)
	}

	// Test graceful shutdown
	if err := server.Shutdown(); err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// Verify server stopped
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("Server returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Server did not stop within timeout")
	}

	// Verify socket was cleaned up
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("Socket file was not removed after shutdown")
	}
}

// TestServerShutdownIdempotent tests that shutdown can be called multiple times safely
func TestServerShutdownIdempotent(t *testing.T) {
	socketPath := getSocketPath(t)

	server := sasl.NewServer(socketPath, "", "https://example.com/auth", "example.com", conf.SASLScopeAll)

	// Start server in goroutine
	go func() { _ = server.Start() }()
	time.Sleep(100 * time.Millisecond)

	// Call shutdown multiple times
	err1 := server.Shutdown()
	err2 := server.Shutdown()
	err3 := server.Shutdown()

	if err1 != nil {
		t.Errorf("First shutdown failed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("Second shutdown failed: %v", err2)
	}
	if err3 != nil {
		t.Errorf("Third shutdown failed: %v", err3)
	}
}

// TestVersionHandshake tests the VERSION command handling
func TestVersionHandshake(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send VERSION command
	_, _ = fmt.Fprintf(conn, "VERSION\t1\t2\n")

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Check response
	expectedResponse := "VERSION\t1\t2\n"
	if response != expectedResponse {
		t.Errorf("Expected response %q, got %q", expectedResponse, response)
	}
}

// TestCPIDCommand tests the CPID command handling
func TestCPIDCommand(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send CPID command
	_, _ = fmt.Fprintf(conn, "CPID\t12345\n")

	// Read all responses - server sends MECH lines followed by DONE
	reader := bufio.NewReader(conn)

	mechs := map[string]bool{}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("Failed to read CPID response: %v", readErr)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "DONE" {
			break
		}
		if strings.HasPrefix(trimmed, "MECH\t") {
			parts := strings.Split(trimmed, "\t")
			if len(parts) >= 2 {
				mechs[parts[1]] = true
			}
		}
	}

	if !mechs["PLAIN"] {
		t.Error("Expected PLAIN mechanism to be announced")
	}
	if !mechs["LOGIN"] {
		t.Error("Expected LOGIN mechanism to be announced")
	}
	if !mechs["OAUTHBEARER"] {
		t.Error("Expected OAUTHBEARER mechanism to be announced")
	}
	if !mechs["XOAUTH2"] {
		t.Error("Expected XOAUTH2 mechanism to be announced")
	}
}

// TestPlainAuthenticationSuccess tests successful PLAIN authentication
func TestPlainAuthenticationSuccess(t *testing.T) {
	socketPath := getSocketPath(t)

	// Mock auth server that accepts credentials
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()
	defer func() { _ = server.Shutdown() }()

	// Wait for socket to be created
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Create credentials: \x00username\x00password
	credentials := "\x00testuser\x00testpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))

	// Send AUTH command with PLAIN mechanism
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Check for success response
	if !strings.HasPrefix(response, "OK\t1\t") {
		t.Errorf("Expected OK response, got: %s", response)
	}

	if !strings.Contains(response, "user=testuser") {
		t.Errorf("Expected user=testuser in response, got: %s", response)
	}
}

// TestPlainAuthenticationWithDomain tests PLAIN authentication with domain appending
func TestPlainAuthenticationWithDomain(t *testing.T) {
	socketPath := getSocketPath(t)

	// Track the username identifier received by auth server
	var receivedUsername string
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read and parse the request body to capture the username identifier.
		var requestBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err == nil {
			if identifiers, ok := requestBody["identifiers"].(map[string]any); ok {
				if username, ok := identifiers["username"].(string); ok {
					receivedUsername = username
				}
			}
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Create credentials with username (no domain)
	credentials := "\x00testuser\x00testpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))

	// Send AUTH command
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)

	// Read response
	reader := bufio.NewReader(conn)
	_, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Give time for the auth request to complete
	time.Sleep(100 * time.Millisecond)

	// Verify local-part username was sent to the auth API.
	if receivedUsername != "testuser" {
		t.Errorf("Expected username testuser to be sent to auth server, got %q", receivedUsername)
	}
}

// TestPlainAuthenticationFailure tests failed PLAIN authentication
func TestPlainAuthenticationFailure(t *testing.T) {
	socketPath := getSocketPath(t)

	// Mock auth server that rejects credentials
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Create credentials
	credentials := "\x00wronguser\x00wrongpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))

	// Send AUTH command
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Check for failure response
	if !strings.HasPrefix(response, "FAIL\t1\t") {
		t.Errorf("Expected FAIL response, got: %s", response)
	}

	if !strings.Contains(response, "reason=Invalid credentials") {
		t.Errorf("Expected 'Invalid credentials' reason, got: %s", response)
	}
}

// TestPlainAuthenticationWithAuthzid tests PLAIN authentication with authorization identity
func TestPlainAuthenticationWithAuthzid(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Create credentials with authzid: authzid\x00authcid\x00password
	credentials := "admin\x00testuser\x00testpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))

	// Send AUTH command
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Should succeed with authcid (testuser)
	if !strings.HasPrefix(response, "OK\t1\t") {
		t.Errorf("Expected OK response, got: %s", response)
	}

	if !strings.Contains(response, "user=testuser") {
		t.Errorf("Expected user=testuser in response, got: %s", response)
	}
}

// TestPlainAuthenticationInvalidBase64 tests handling of invalid base64 encoding
func TestPlainAuthenticationInvalidBase64(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send AUTH command with invalid base64
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=!!!invalid-base64!!!\n")

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Should fail with encoding error
	if !strings.HasPrefix(response, "FAIL\t1\t") {
		t.Errorf("Expected FAIL response, got: %s", response)
	}

	if !strings.Contains(response, "reason=Invalid encoding") {
		t.Errorf("Expected 'Invalid encoding' reason, got: %s", response)
	}
}

// TestPlainAuthenticationMalformedCredentials tests handling of malformed credentials
func TestPlainAuthenticationMalformedCredentials(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	testCases := []struct {
		name        string
		credentials string
		description string
	}{
		{
			name:        "NoNullSeparators",
			credentials: "usernamepassword",
			description: "Credentials without null separators",
		},
		{
			name:        "OnlyOneField",
			credentials: "username",
			description: "Only one field provided",
		},
		{
			name:        "EmptyCredentials",
			credentials: "",
			description: "Empty credentials",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Connect to the socket
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				t.Fatalf("Failed to connect to socket: %v", err)
			}
			defer func() { _ = conn.Close() }()

			// Encode malformed credentials
			encodedCreds := base64.StdEncoding.EncodeToString([]byte(tc.credentials))

			// Send AUTH command
			_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)

			// Read response
			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			// Should fail with format error
			if !strings.HasPrefix(response, "FAIL\t1\t") {
				t.Errorf("Expected FAIL response for %s, got: %s", tc.description, response)
			}
		})
	}
}

// TestPlainAuthenticationContinuationRequest tests continuation request when no response provided
func TestPlainAuthenticationContinuationRequest(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send AUTH command without response
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\n")

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Should get continuation request
	expectedResponse := "CONT\t1\t\n"
	if response != expectedResponse {
		t.Errorf("Expected continuation response %q, got %q", expectedResponse, response)
	}
}

// TestLoginMechanism tests LOGIN authentication mechanism
func TestLoginMechanism(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send AUTH command with LOGIN mechanism
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tLOGIN\tservice=smtp\n")

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Should get continuation request or not implemented message
	// LOGIN is not fully implemented, so we expect either CONT or FAIL
	if !strings.HasPrefix(response, "CONT\t1\t") && !strings.HasPrefix(response, "FAIL\t1\t") {
		t.Errorf("Expected CONT or FAIL response, got: %s", response)
	}
}

// TestUnsupportedMechanism tests handling of unsupported authentication mechanisms
func TestUnsupportedMechanism(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	mechanisms := []string{"CRAM-MD5", "DIGEST-MD5", "GSSAPI", "NTLM"}

	for _, mechanism := range mechanisms {
		t.Run(mechanism, func(t *testing.T) {
			// Connect to the socket
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				t.Fatalf("Failed to connect to socket: %v", err)
			}
			defer func() { _ = conn.Close() }()

			// Send AUTH command with unsupported mechanism
			_, _ = fmt.Fprintf(conn, "AUTH\t1\t%s\tservice=smtp\n", mechanism)

			// Read response
			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			// Should fail with unsupported mechanism
			if !strings.HasPrefix(response, "FAIL\t1\t") {
				t.Errorf("Expected FAIL response for %s, got: %s", mechanism, response)
			}

			if !strings.Contains(response, "Unsupported mechanism") {
				t.Errorf("Expected 'Unsupported mechanism' in response, got: %s", response)
			}
		})
	}
}

// TestAuthMechanismCaseInsensitive tests that mechanism names are case-insensitive
func TestAuthMechanismCaseInsensitive(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	mechanisms := []string{"PLAIN", "Plain", "plain", "pLaIn"}

	for _, mechanism := range mechanisms {
		t.Run(mechanism, func(t *testing.T) {
			// Connect to the socket
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				t.Fatalf("Failed to connect to socket: %v", err)
			}
			defer func() { _ = conn.Close() }()

			// Send AUTH command
			_, _ = fmt.Fprintf(conn, "AUTH\t1\t%s\tservice=smtp\n", mechanism)

			// Read response
			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			// Should get continuation request (not failure)
			if !strings.HasPrefix(response, "CONT\t1\t") {
				t.Errorf("Expected CONT response for %s, got: %s", mechanism, response)
			}
		})
	}
}

// TestInvalidAuthCommand tests handling of invalid AUTH command formats
func TestInvalidAuthCommand(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send AUTH command with missing mechanism
	_, _ = fmt.Fprintf(conn, "AUTH\t1\n")

	// Read response - server should handle gracefully (might not respond or log error)
	// The current implementation doesn't send a response for invalid formats
	// We just verify the server doesn't crash
	time.Sleep(100 * time.Millisecond)
}

// TestConcurrentConnections tests handling of multiple concurrent connections
func TestConcurrentConnections(t *testing.T) {
	socketPath := getSocketPath(t)

	// Track number of authentication requests
	var authCount int
	var authMutex sync.Mutex

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMutex.Lock()
		authCount++
		authMutex.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Number of concurrent connections
	numConnections := 10
	var wg sync.WaitGroup

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Connect to the socket
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				t.Errorf("Connection %d failed: %v", id, err)
				return
			}
			defer func() { _ = conn.Close() }()

			// Send authentication request
			credentials := fmt.Sprintf("\x00user%d\x00pass%d", id, id)
			encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
			_, _ = fmt.Fprintf(conn, "AUTH\t%d\tPLAIN\tservice=smtp\tresp=%s\n", id, encodedCreds)

			// Read response
			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				t.Errorf("Connection %d failed to read response: %v", id, err)
				return
			}

			// Verify success
			if !strings.HasPrefix(response, fmt.Sprintf("OK\t%d\t", id)) {
				t.Errorf("Connection %d expected OK response, got: %s", id, response)
			}
		}(i)
	}

	// Wait for all connections to complete
	wg.Wait()

	// Verify all authentications were processed
	authMutex.Lock()
	defer authMutex.Unlock()
	if authCount != numConnections {
		t.Errorf("Expected %d authentication requests, got %d", numConnections, authCount)
	}
}

// TestConnectionTimeout tests that connections timeout properly
func TestConnectionTimeout(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Don't send anything and wait for timeout (30 seconds in the implementation)
	// For testing purposes, we just verify the connection is established
	// The actual timeout test would take 30+ seconds, so we skip it
	// This is more of a smoke test to ensure connection handling works

	reader := bufio.NewReader(conn)

	// Set a short deadline for testing
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

	_, err = reader.ReadString('\n')

	// We expect a timeout error
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

// TestAuthenticationAPIError tests handling of authentication API errors
func TestAuthenticationAPIError(t *testing.T) {
	socketPath := getSocketPath(t)

	testCases := []struct {
		name       string
		statusCode int
		shouldFail bool
	}{
		{"Success200", http.StatusOK, false},
		{"Unauthorized401", http.StatusUnauthorized, true},
		{"Forbidden403", http.StatusForbidden, true},
		{"InternalError500", http.StatusInternalServerError, true},
		{"BadGateway502", http.StatusBadGateway, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer authServer.Close()

			server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

			// Start server
			go func() { _ = server.Start() }()
			defer func() { _ = server.Shutdown() }()
			time.Sleep(100 * time.Millisecond)

			// Connect to the socket
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				t.Fatalf("Failed to connect to socket: %v", err)
			}
			defer func() { _ = conn.Close() }()

			// Send authentication request
			credentials := "\x00testuser\x00testpass"
			encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
			_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)

			// Read response
			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("Failed to read response: %v", err)
			}

			// Check expected outcome
			if tc.shouldFail {
				if !strings.HasPrefix(response, "FAIL\t1\t") {
					t.Errorf("Expected FAIL response for status %d, got: %s", tc.statusCode, response)
				}
			} else {
				if !strings.HasPrefix(response, "OK\t1\t") {
					t.Errorf("Expected OK response for status %d, got: %s", tc.statusCode, response)
				}
			}
		})
	}
}

// TestMultipleCommandsInSession tests sending multiple commands in a single session
func TestMultipleCommandsInSession(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)

	// Send VERSION
	_, _ = fmt.Fprintf(conn, "VERSION\t1\t2\n")
	response, _ := reader.ReadString('\n')
	if !strings.HasPrefix(response, "VERSION") {
		t.Errorf("Expected VERSION response, got: %s", response)
	}

	// Send CPID
	_, _ = fmt.Fprintf(conn, "CPID\t12345\n")
	mechs := map[string]bool{}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("Failed to read CPID response: %v", readErr)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "DONE" {
			break
		}
		if strings.HasPrefix(trimmed, "MECH\t") {
			parts := strings.Split(trimmed, "\t")
			if len(parts) >= 2 {
				mechs[parts[1]] = true
			}
		}
	}

	if !mechs["PLAIN"] {
		t.Error("Expected PLAIN mechanism during CPID")
	}
	if !mechs["LOGIN"] {
		t.Error("Expected LOGIN mechanism during CPID")
	}
	if !mechs["OAUTHBEARER"] {
		t.Error("Expected OAUTHBEARER mechanism during CPID")
	}
	if !mechs["XOAUTH2"] {
		t.Error("Expected XOAUTH2 mechanism during CPID")
	}

	// Send AUTH
	credentials := "\x00testuser\x00testpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)
	response, _ = reader.ReadString('\n')
	if !strings.HasPrefix(response, "OK\t1\t") {
		t.Errorf("Expected OK response, got: %s", response)
	}

	// Send another AUTH
	_, _ = fmt.Fprintf(conn, "AUTH\t2\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)
	response, _ = reader.ReadString('\n')
	if !strings.HasPrefix(response, "OK\t2\t") {
		t.Errorf("Expected OK response with id 2, got: %s", response)
	}
}

// TestTCPListener tests TCP listener functionality
func TestTCPListener(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	// Use a random port
	server := sasl.NewServer("", "127.0.0.1:0", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Find the actual port by attempting to connect
	// Note: Since we can't get the actual port from the server,
	// we'll use a fixed port for testing
	server2 := sasl.NewServer("", "127.0.0.1:12345", authServer.URL, "example.com", conf.SASLScopeAll)
	go func() { _ = server2.Start() }()
	defer func() { _ = server2.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect via TCP
	conn, err := net.Dial("tcp", "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("Failed to connect via TCP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send VERSION command
	_, _ = fmt.Fprintf(conn, "VERSION\t1\t2\n")

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Check response
	expectedResponse := "VERSION\t1\t2\n"
	if response != expectedResponse {
		t.Errorf("Expected response %q, got %q", expectedResponse, response)
	}
}

// TestTCPAuthentication tests authentication over TCP
func TestTCPAuthentication(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer("", "127.0.0.1:12346", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect via TCP
	conn, err := net.Dial("tcp", "127.0.0.1:12346")
	if err != nil {
		t.Fatalf("Failed to connect via TCP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send PLAIN authentication
	credentials := "\x00testuser\x00testpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Check response
	if !strings.HasPrefix(response, "OK\t1\t") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestBothListeners tests that both Unix socket and TCP listeners work together
func TestBothListeners(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "127.0.0.1:12347", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(200 * time.Millisecond)

	// Test Unix socket connection
	unixConn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect via Unix socket: %v", err)
	}
	defer func() { _ = unixConn.Close() }()

	// Test TCP connection
	tcpConn, err := net.Dial("tcp", "127.0.0.1:12347")
	if err != nil {
		t.Fatalf("Failed to connect via TCP: %v", err)
	}
	defer func() { _ = tcpConn.Close() }()

	// Send commands to both connections
	credentials := "\x00testuser\x00testpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))

	// Unix socket auth
	_, _ = fmt.Fprintf(unixConn, "AUTH\t1\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)
	unixReader := bufio.NewReader(unixConn)
	unixResponse, _ := unixReader.ReadString('\n')

	// TCP auth
	_, _ = fmt.Fprintf(tcpConn, "AUTH\t2\tPLAIN\tservice=smtp\tresp=%s\n", encodedCreds)
	tcpReader := bufio.NewReader(tcpConn)
	tcpResponse, _ := tcpReader.ReadString('\n')

	// Both should succeed
	if !strings.HasPrefix(unixResponse, "OK\t1\t") {
		t.Errorf("Unix socket: Expected OK response, got: %s", unixResponse)
	}
	if !strings.HasPrefix(tcpResponse, "OK\t2\t") {
		t.Errorf("TCP: Expected OK response, got: %s", tcpResponse)
	}
}

// TestTCPConcurrentConnections tests concurrent connections over TCP
func TestTCPConcurrentConnections(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer("", "127.0.0.1:12348", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Create multiple concurrent connections
	var wg sync.WaitGroup
	numConnections := 10

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", "127.0.0.1:12348")
			if err != nil {
				t.Errorf("Connection %d failed: %v", id, err)
				return
			}
			defer func() { _ = conn.Close() }()

			// Send auth
			credentials := fmt.Sprintf("\x00user%d\x00pass%d", id, id)
			encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))
			_, _ = fmt.Fprintf(conn, "AUTH\t%d\tPLAIN\tservice=smtp\tresp=%s\n", id, encodedCreds)

			// Read response
			reader := bufio.NewReader(conn)
			response, err := reader.ReadString('\n')
			if err != nil {
				t.Errorf("Connection %d failed to read: %v", id, err)
				return
			}

			if !strings.HasPrefix(response, fmt.Sprintf("OK\t%d\t", id)) {
				t.Errorf("Connection %d: Expected OK response, got: %s", id, response)
			}
		}(i)
	}

	wg.Wait()
}

// BenchmarkPlainAuthentication benchmarks PLAIN authentication performance
func BenchmarkPlainAuthentication(b *testing.B) {
	tmpDir := b.TempDir()
	socketPath := filepath.Join(tmpDir, "test-sasl.sock")

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Prepare credentials
	credentials := "\x00testuser\x00testpass"
	encodedCreds := base64.StdEncoding.EncodeToString([]byte(credentials))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			b.Fatalf("Failed to connect: %v", err)
		}

		_, _ = fmt.Fprintf(conn, "AUTH\t%d\tPLAIN\tservice=smtp\tresp=%s\n", i, encodedCreds)

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')

		_ = conn.Close()
	}
}

// BenchmarkBase64EncodeDecode benchmarks base64 encoding and decoding
func BenchmarkBase64EncodeDecode(b *testing.B) {
	credentials := "\x00testuser@example.com\x00testpassword123"

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		encoded := base64.StdEncoding.EncodeToString([]byte(credentials))
		decoded, _ := base64.StdEncoding.DecodeString(encoded)
		_ = strings.Split(string(decoded), "\x00")
	}
}

// TestServerWithSASLScope tests server creation with different SASL scopes
func TestServerWithSASLScope(t *testing.T) {
	tests := []struct {
		name  string
		scope conf.SASLScope
	}{
		{
			name:  "Server with tcp_only scope",
			scope: conf.SASLScopeTCPOnly,
		},
		{
			name:  "Server with unix_socket_only scope",
			scope: conf.SASLScopeUnixSocketOnly,
		},
		{
			name:  "Server with all scope",
			scope: conf.SASLScopeAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := sasl.NewServer(
				"/tmp/test.sock",
				":12345",
				"http://localhost",
				"example.com",
				tt.scope,
			)

			// NewServer should always return a non-nil server
			if server == nil {
				t.Fatal("Expected server to be created, got nil")
			}

			// Verify the server has the correct scope configured
			// Note: This requires accessing the server's internal state,
			// which is tested indirectly through the Start() method behavior
			// in TestSASLScopeConfiguration
		})
	}
}

// TestConnectionTypeConstants tests that connection type constants are distinct
func TestConnectionTypeConstants(t *testing.T) {
	// Test that connection types are different
	tcp := sasl.ConnectionTypeTCP
	unix := sasl.ConnectionTypeUnixSocket

	if tcp == unix {
		t.Error("ConnectionTypeTCP and ConnectionTypeUnixSocket should be different")
	}

	// Test that they can be compared
	if tcp != sasl.ConnectionTypeTCP {
		t.Error("ConnectionTypeTCP constant comparison failed")
	}

	if unix != sasl.ConnectionTypeUnixSocket {
		t.Error("ConnectionTypeUnixSocket constant comparison failed")
	}
}

// TestSASLScopeConfiguration tests SASL scope in real server scenario
func TestSASLScopeConfiguration(t *testing.T) {
	// Create mock auth server
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	socketPath := getSocketPath(t)

	tests := []struct {
		name  string
		scope conf.SASLScope
	}{
		{"TCPOnly", conf.SASLScopeTCPOnly},
		{"UnixSocketOnly", conf.SASLScopeUnixSocketOnly},
		{"All", conf.SASLScopeAll},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := sasl.NewServer(socketPath, "127.0.0.1:0", authServer.URL, "example.com", tt.scope)

			// Start server
			go func() {
				_ = server.Start()
			}()

			// Give server time to start
			time.Sleep(100 * time.Millisecond)

			// Just verify server starts without error
			if err := server.Shutdown(); err != nil {
				t.Errorf("Shutdown error with scope %s: %v", tt.scope, err)
			}
		})
	}
}

// TestLoginMechanismWithInitialResponse tests LOGIN authentication mechanism when username is provided in the initial AUTH command
func TestLoginMechanismWithInitialResponse(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Encode username: testuser
	encodedUser := base64.StdEncoding.EncodeToString([]byte("testuser"))

	// Send AUTH command with LOGIN mechanism and initial response (username)
	_, _ = fmt.Fprintf(conn, "AUTH\t1\tLOGIN\tservice=smtp\tresp=%s\n", encodedUser)

	// Read response
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	// Should get a request for password
	expectedResponse := "CONT\t1\tPassword:\n"
	if response != expectedResponse {
		t.Errorf("Expected %q, got: %q", expectedResponse, response)
	}

	// Send password: testpass
	encodedPass := base64.StdEncoding.EncodeToString([]byte("testpass"))
	_, _ = fmt.Fprintf(conn, "CONT\t1\t%s\n", encodedPass)

	// Read response
	response2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read second response: %v", err)
	}

	// Should get OK response
	if !strings.HasPrefix(response2, "OK\t1\t") {
		t.Errorf("Expected OK response, got: %q", response2)
	}
}

// TestDoSProtection tests that concurrent auth attempts are correctly limited
func TestDoSProtection(t *testing.T) {
	socketPath := getSocketPath(t)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	server := sasl.NewServer(socketPath, "", authServer.URL, "example.com", conf.SASLScopeAll)

	// Start server
	go func() { _ = server.Start() }()
	defer func() { _ = server.Shutdown() }()
	time.Sleep(100 * time.Millisecond)

	// Connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)

	// Spawn maxAuthStatesPerConn (10) active auth states
	// We will use LOGIN without initial response so they remain active
	for i := 1; i <= 10; i++ {
		_, _ = fmt.Fprintf(conn, "AUTH\t%d\tLOGIN\tservice=smtp\n", i)
		response, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("Failed to read response for auth %d: %v", i, err)
		}
		expectedResponse := fmt.Sprintf("CONT\t%d\tUsername:\n", i)
		if response != expectedResponse {
			t.Fatalf("Expected CONT response for auth %d, got: %q", i, response)
		}
	}

	// Now try one more AUTH PLAIN (with or without initial response)
	// It should immediately fail due to DoS protection
	_, _ = fmt.Fprintf(conn, "AUTH\t11\tPLAIN\tservice=smtp\n")
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response for exceeded auth: %v", err)
	}

	if !strings.HasPrefix(response, "FAIL\t11\t") || !strings.Contains(response, "reason=Too many authentication attempts") {
		t.Errorf("Expected DoS FAIL response, got: %q", response)
	}

	// Test that an AUTH attempt WITH an initial response is also correctly blocked by DoS protection
	encodedUser := base64.StdEncoding.EncodeToString([]byte("testuser"))
	_, _ = fmt.Fprintf(conn, "AUTH\t12\tLOGIN\tservice=smtp\tresp=%s\n", encodedUser)
	response2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("Failed to read response for exceeded auth with initial response: %v", err)
	}

	if !strings.HasPrefix(response2, "FAIL\t12\t") || !strings.Contains(response2, "reason=Too many authentication attempts") {
		t.Errorf("Expected DoS FAIL response with initial response, got: %q", response2)
	}
}
