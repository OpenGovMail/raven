package auth_test

import (
	"strings"
	"testing"

	"raven/internal/models"
	"raven/internal/server"
)

// TestLogoutCommand_Unauthenticated tests LOGOUT before authentication
// RFC 3501: LOGOUT can be used in any state
func TestLogoutCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	srv.HandleLogout(conn, "L001", []string{"L001", "LOGOUT"})

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have exactly 2 lines: BYE untagged response and OK tagged response
	if len(lines) != 2 {
		t.Fatalf("Expected 2 response lines, got %d: %v", len(lines), lines)
	}

	// Check BYE untagged response (first line)
	if !strings.HasPrefix(lines[0], "* BYE") {
		t.Errorf("Expected BYE untagged response, got: '%s'", lines[0])
	}

	// Check tagged OK response (second line)
	expectedOK := "L001 OK LOGOUT completed"
	if lines[1] != expectedOK {
		t.Errorf("Expected '%s', got: '%s'", expectedOK, lines[1])
	}
}

// TestLogoutCommand_RejectsArguments tests LOGOUT with extra arguments
func TestLogoutCommand_RejectsArguments(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	loggedOut := srv.HandleLogout(conn, "A1", []string{"A1", "LOGOUT", "extra"})
	if loggedOut {
		t.Error("HandleLogout should return false when extra arguments are provided")
	}

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A1 BAD LOGOUT takes no arguments") {
		t.Errorf("Expected BAD response for extra arguments, got: %s", response)
	}
	if strings.Contains(response, "* BYE") {
		t.Errorf("LOGOUT with extra arguments must not send BYE, got: %s", response)
	}
	if strings.Contains(response, "OK LOGOUT completed") {
		t.Errorf("LOGOUT with extra arguments should not complete OK, got: %s", response)
	}
	if response != "A1 BAD LOGOUT takes no arguments\r\n" {
		t.Errorf("Expected only the exact BAD reply, got: %q", response)
	}
}

// TestLogoutCommand_RejectsMultipleArguments tests LOGOUT with multiple extra arguments
func TestLogoutCommand_RejectsMultipleArguments(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	loggedOut := srv.HandleLogout(conn, "A2", []string{"A2", "LOGOUT", "foo", "bar"})
	if loggedOut {
		t.Error("HandleLogout should return false when extra arguments are provided")
	}

	response := conn.GetWrittenData()
	if response != "A2 BAD LOGOUT takes no arguments\r\n" {
		t.Errorf("Expected exact BAD reply for multiple arguments, got: %q", response)
	}
}

// TestLogoutCommand_Authenticated tests LOGOUT after successful authentication
func TestLogoutCommand_Authenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	srv.HandleLogout(conn, "A023", []string{"A023", "LOGOUT"})

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have exactly 2 lines: BYE untagged response and OK tagged response
	if len(lines) != 2 {
		t.Fatalf("Expected 2 response lines, got %d: %v", len(lines), lines)
	}

	// Check BYE untagged response (first line) - must be sent BEFORE OK
	if !strings.HasPrefix(lines[0], "* BYE") {
		t.Errorf("Expected BYE untagged response as first line, got: '%s'", lines[0])
	}

	// Verify BYE contains appropriate message
	if !strings.Contains(lines[0], "logging out") {
		t.Errorf("Expected BYE message to contain 'logging out', got: '%s'", lines[0])
	}

	// Check tagged OK response (second line) - must be sent AFTER BYE
	expectedOK := "A023 OK LOGOUT completed"
	if lines[1] != expectedOK {
		t.Errorf("Expected '%s', got: '%s'", expectedOK, lines[1])
	}
}

// TestLogoutCommand_WithSelectedFolder tests LOGOUT with a folder selected
func TestLogoutCommand_WithSelectedFolder(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	// Simulate state with selected folder
	_ = &models.ClientState{
		Authenticated:  true,
		Username:       "testuser",
		SelectedFolder: "INBOX",
	}

	srv.HandleLogout(conn, "L003", []string{"L003", "LOGOUT"})

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have exactly 2 lines regardless of state
	if len(lines) != 2 {
		t.Fatalf("Expected 2 response lines, got %d: %v", len(lines), lines)
	}

	// Verify BYE then OK order
	if !strings.HasPrefix(lines[0], "* BYE") {
		t.Errorf("Expected BYE untagged response as first line, got: '%s'", lines[0])
	}

	if !strings.HasPrefix(lines[1], "L003 OK LOGOUT completed") {
		t.Errorf("Expected tagged OK response as second line, got: '%s'", lines[1])
	}
}

// TestLogoutCommand_ResponseOrder tests that BYE is sent before OK
// This is a MUST requirement from RFC 3501
func TestLogoutCommand_ResponseOrder(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	srv.HandleLogout(conn, "L004", []string{"L004", "LOGOUT"})

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	if len(lines) < 2 {
		t.Fatalf("Expected at least 2 response lines, got %d", len(lines))
	}

	// RFC 3501: The server MUST send a BYE untagged response BEFORE the (tagged) OK response
	byeIndex := -1
	okIndex := -1

	for i, line := range lines {
		if strings.HasPrefix(line, "* BYE") {
			byeIndex = i
		}
		if strings.HasPrefix(line, "L004 OK") {
			okIndex = i
		}
	}

	if byeIndex == -1 {
		t.Error("Missing BYE untagged response")
	}

	if okIndex == -1 {
		t.Error("Missing OK tagged response")
	}

	if byeIndex >= okIndex {
		t.Errorf("BYE must come before OK. BYE at index %d, OK at index %d", byeIndex, okIndex)
	}
}

// TestLogoutCommand_MultipleSequentialLogouts tests multiple LOGOUT commands
// (though in practice connection closes after first)
func TestLogoutCommand_MultipleSequentialLogouts(t *testing.T) {
	srv := server.SetupTestServerSimple(t)

	testCases := []struct {
		tag        string
		expectedOK string
	}{
		{"L001", "L001 OK LOGOUT completed"},
		{"L002", "L002 OK LOGOUT completed"},
		{"ABC123", "ABC123 OK LOGOUT completed"},
	}

	for _, tc := range testCases {
		conn := server.NewMockConn()
		srv.HandleLogout(conn, tc.tag, []string{tc.tag, "LOGOUT"})

		response := conn.GetWrittenData()
		lines := strings.Split(strings.TrimSpace(response), "\r\n")

		if len(lines) != 2 {
			t.Errorf("Tag %s: Expected 2 lines, got %d", tc.tag, len(lines))
			continue
		}

		if !strings.HasPrefix(lines[0], "* BYE") {
			t.Errorf("Tag %s: Expected BYE response, got: '%s'", tc.tag, lines[0])
		}

		if lines[1] != tc.expectedOK {
			t.Errorf("Tag %s: Expected '%s', got: '%s'", tc.tag, tc.expectedOK, lines[1])
		}
	}
}

// TestLogoutCommand_AlwaysSucceeds tests that LOGOUT always returns OK
// LOGOUT should never fail with BAD or NO response
func TestLogoutCommand_AlwaysSucceeds(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	srv.HandleLogout(conn, "L005", []string{"L005", "LOGOUT"})

	response := conn.GetWrittenData()

	// Should never contain BAD or NO
	if strings.Contains(response, " BAD ") {
		t.Error("LOGOUT should never return BAD response")
	}

	if strings.Contains(response, " NO ") {
		t.Error("LOGOUT should never return NO response")
	}

	// Must contain OK
	if !strings.Contains(response, " OK ") {
		t.Error("LOGOUT must return OK response")
	}
}

// TestLogoutCommand_NoArguments tests that LOGOUT doesn't require arguments
func TestLogoutCommand_NoArguments(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	// LOGOUT takes no arguments, just tag
	srv.HandleLogout(conn, "L006", []string{"L006", "LOGOUT"})

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	if len(lines) != 2 {
		t.Fatalf("Expected 2 response lines, got %d: %v", len(lines), lines)
	}

	// Verify proper response
	if !strings.HasPrefix(lines[0], "* BYE") {
		t.Errorf("Expected BYE response, got: '%s'", lines[0])
	}

	if !strings.Contains(lines[1], "OK LOGOUT completed") {
		t.Errorf("Expected OK LOGOUT completed, got: '%s'", lines[1])
	}
}

// TestLogoutCommand_TagFormat tests various tag formats
func TestLogoutCommand_TagFormat(t *testing.T) {
	testCases := []struct {
		tag         string
		description string
	}{
		{"A001", "Standard alphanumeric tag"},
		{"123", "Numeric only tag"},
		{"XYZ", "Alphabetic only tag"},
		{"a", "Single character tag"},
		{"LOGOUT1", "Tag with command name"},
		{"tag.001", "Tag with dot"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			srv := server.SetupTestServerSimple(t)
			conn := server.NewMockConn()

			srv.HandleLogout(conn, tc.tag, []string{tc.tag, "LOGOUT"})

			response := conn.GetWrittenData()
			lines := strings.Split(strings.TrimSpace(response), "\r\n")

			if len(lines) != 2 {
				t.Fatalf("Expected 2 lines, got %d", len(lines))
			}

			// The OK response must echo the tag
			expectedOK := tc.tag + " OK LOGOUT completed"
			if lines[1] != expectedOK {
				t.Errorf("Expected '%s', got: '%s'", expectedOK, lines[1])
			}
		})
	}
}

// TestLogoutCommand_MessageContent tests the content of BYE message
func TestLogoutCommand_MessageContent(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	srv.HandleLogout(conn, "L007", []string{"L007", "LOGOUT"})

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	if len(lines) < 1 {
		t.Fatal("Expected at least 1 line")
	}

	byeLine := lines[0]

	// Check BYE message structure
	if !strings.HasPrefix(byeLine, "* BYE") {
		t.Errorf("BYE line must start with '* BYE', got: '%s'", byeLine)
	}

	// BYE message should contain meaningful text
	if len(byeLine) <= len("* BYE ") {
		t.Error("BYE message should contain explanatory text")
	}
}
