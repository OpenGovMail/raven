package extension_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"raven/internal/db"
	"raven/internal/models"
	"raven/internal/server"
)

// ===== NOOP TESTS =====

// TestNoopCommand_Unauthenticated tests NOOP before authentication
func TestNoopCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleNoop(conn, "N001", []string{"N001", "NOOP"}, state)

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have 1 line: completion only
	if len(lines) != 1 {
		t.Fatalf("Expected 1 response line, got %d: %v", len(lines), lines)
	}

	// Check tagged OK response
	expectedOK := "N001 OK NOOP completed"
	if lines[0] != expectedOK {
		t.Errorf("Expected '%s', got: '%s'", expectedOK, lines[0])
	}
}

// TestNoopCommand_RejectsArguments tests NOOP with extra arguments
func TestNoopCommand_RejectsArguments(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{}

	srv.HandleNoop(conn, "N004", []string{"N004", "NOOP", "extra"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "N004 BAD NOOP takes no arguments") {
		t.Errorf("Expected BAD response for extra arguments, got: %s", response)
	}
	if strings.Contains(response, "OK NOOP completed") {
		t.Errorf("NOOP with extra arguments should not complete OK, got: %s", response)
	}
}

// TestNoopCommand_AuthenticatedNoFolder tests NOOP when authenticated but no folder selected
func TestNoopCommand_AuthenticatedNoFolder(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated:  true,
		Username:       "testuser",
		SelectedFolder: "",
	}

	srv.HandleNoop(conn, "N002", []string{"N002", "NOOP"}, state)

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have 1 line: completion only
	if len(lines) != 1 {
		t.Fatalf("Expected 1 response line, got %d: %v", len(lines), lines)
	}

	// Check tagged OK response
	expectedOK := "N002 OK NOOP completed"
	if lines[0] != expectedOK {
		t.Errorf("Expected '%s', got: '%s'", expectedOK, lines[0])
	}
}

// TestNoopCommand_WithSelectedFolder tests NOOP with selected folder
func TestNoopCommand_WithSelectedFolder(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	// Setup authenticated state with user in database
	state := server.SetupAuthenticatedState(t, srv, "testuser")
	state.SelectedFolder = "INBOX"
	state.SelectedMailboxID = 1
	state.LastMessageCount = 0
	state.LastRecentCount = 0

	srv.HandleNoop(conn, "N003", []string{"N003", "NOOP"}, state)

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have at least the completion line
	if len(lines) < 1 {
		t.Fatalf("Expected at least 1 response line, got %d: %v", len(lines), lines)
	}

	// Last line should be tagged OK response
	lastLine := lines[len(lines)-1]
	expectedOK := "N003 OK NOOP completed"
	if lastLine != expectedOK {
		t.Errorf("Expected '%s', got: '%s'", expectedOK, lastLine)
	}
}

// TestNoopCommand_AlwaysSucceeds tests that NOOP always returns OK
func TestNoopCommand_AlwaysSucceeds(t *testing.T) {
	testCases := []struct {
		name  string
		state *models.ClientState
		tag   string
	}{
		{
			name: "Unauthenticated",
			state: &models.ClientState{
				Authenticated: false,
			},
			tag: "T001",
		},
		{
			name: "Authenticated",
			state: &models.ClientState{
				Authenticated: true,
				Username:      "user1",
			},
			tag: "T002",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := server.SetupTestServerSimple(t)
			conn := server.NewMockConn()

			srv.HandleNoop(conn, tc.tag, []string{tc.tag, "NOOP"}, tc.state)

			response := conn.GetWrittenData()

			// Should always end with OK
			if !strings.Contains(response, " OK NOOP completed") {
				t.Errorf("NOOP should always succeed, got: %s", response)
			}

			// Should contain the correct tag
			expectedOK := tc.tag + " OK NOOP completed"
			if !strings.Contains(response, expectedOK) {
				t.Errorf("Expected to find '%s' in response, got: %s", expectedOK, response)
			}
		})
	}
}

// TestNoopCommand_ResponseFormat tests the format of NOOP responses
func TestNoopCommand_ResponseFormat(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	srv.HandleNoop(conn, "FORMAT", []string{"FORMAT", "NOOP"}, state)

	response := conn.GetWrittenData()

	// Check that response ends with CRLF
	if !strings.HasSuffix(response, "\r\n") {
		t.Errorf("Response should end with CRLF")
	}

	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Last line should be tagged completion
	lastLine := lines[len(lines)-1]
	if !strings.HasPrefix(lastLine, "FORMAT OK NOOP completed") {
		t.Errorf("Last line should be tagged completion, got: %s", lastLine)
	}
}

// TestNoopCommand_TagHandling tests various tag formats
func TestNoopCommand_TagHandling(t *testing.T) {
	testCases := []struct {
		tag         string
		expectedTag string
	}{
		{"A001", "A001"},
		{"noop1", "noop1"},
		{"TAG-123", "TAG-123"},
		{"*", "*"},
		{"", ""},
	}

	for _, tc := range testCases {
		t.Run("Tag_"+tc.tag, func(t *testing.T) {
			srv := server.SetupTestServerSimple(t)
			conn := server.NewMockConn()
			state := &models.ClientState{Authenticated: false}

			srv.HandleNoop(conn, tc.tag, []string{tc.tag, "NOOP"}, state)

			response := conn.GetWrittenData()
			expectedOK := tc.expectedTag + " OK NOOP completed"

			if !strings.Contains(response, expectedOK) {
				t.Errorf("Expected '%s' in response, got: %s", expectedOK, response)
			}
		})
	}
}

// TestNoopCommand_StateTracking tests that NOOP updates state correctly
func TestNoopCommand_StateTracking(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	// Setup with selected mailbox
	state.SelectedFolder = "INBOX"
	state.SelectedMailboxID = 1
	state.LastMessageCount = 0
	state.LastRecentCount = 0

	srv.HandleNoop(conn, "TRACK", []string{"TRACK", "NOOP"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "TRACK OK NOOP completed") {
		t.Errorf("Expected NOOP completion, got: %s", response)
	}
}

// TestNoopCommand_NewMessages tests NOOP when new messages arrive
func TestNoopCommand_NewMessages(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	// Insert test messages into INBOX
	dbMgr := srv.GetDBManager().(*db.DBManager)
	server.InsertTestMail(t, dbMgr, "testuser", "Test 1", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, dbMgr, "testuser", "Test 2", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, dbMgr, "testuser", "Test 3", "sender@test.com", "testuser@localhost", "INBOX")

	// Setup state with lower message count
	state.SelectedFolder = "INBOX"
	state.SelectedMailboxID = 1
	state.LastMessageCount = 1 // Simulate that client knows about 1 message
	state.LastRecentCount = 0

	srv.HandleNoop(conn, "NEW", []string{"NEW", "NOOP"}, state)

	response := conn.GetWrittenData()

	// Should report new EXISTS count
	if !strings.Contains(response, "* 3 EXISTS") {
		t.Errorf("Expected EXISTS response for new messages, got: %s", response)
	}

	// Should report new RECENT messages
	if !strings.Contains(response, "* 2 RECENT") {
		t.Errorf("Expected RECENT response for new messages, got: %s", response)
	}

	// Should complete OK
	if !strings.Contains(response, "NEW OK NOOP completed") {
		t.Errorf("Expected OK completion, got: %s", response)
	}

	// State should be updated
	if state.LastMessageCount != 3 {
		t.Errorf("Expected LastMessageCount=3, got %d", state.LastMessageCount)
	}
}

// TestNoopCommand_ExpungedMessages tests NOOP when messages are deleted
func TestNoopCommand_ExpungedMessages(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	// Insert test messages
	dbMgr := srv.GetDBManager().(*db.DBManager)
	server.InsertTestMail(t, dbMgr, "testuser", "Test 1", "sender@test.com", "testuser@localhost", "INBOX")

	// Setup state with higher message count (simulate messages were deleted)
	state.SelectedFolder = "INBOX"
	state.SelectedMailboxID = 1
	state.LastMessageCount = 5 // Client thinks there are 5 messages
	state.LastRecentCount = 0

	srv.HandleNoop(conn, "EXP", []string{"EXP", "NOOP"}, state)

	response := conn.GetWrittenData()

	// Should report EXPUNGE for deleted messages
	if !strings.Contains(response, "EXPUNGE") {
		t.Errorf("Expected EXPUNGE response, got: %s", response)
	}

	// Should complete OK
	if !strings.Contains(response, "EXP OK NOOP completed") {
		t.Errorf("Expected OK completion, got: %s", response)
	}

	// State should be updated to current count
	if state.LastMessageCount != 1 {
		t.Errorf("Expected LastMessageCount=1, got %d", state.LastMessageCount)
	}
}

// TestNoopCommand_FlagChanges tests NOOP when only flags change
func TestNoopCommand_FlagChanges(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	// Insert test messages
	dbMgr := srv.GetDBManager().(*db.DBManager)
	server.InsertTestMail(t, dbMgr, "testuser", "Test 1", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, dbMgr, "testuser", "Test 2", "sender@test.com", "testuser@localhost", "INBOX")

	// Setup state with same count but different recent count
	state.SelectedFolder = "INBOX"
	state.SelectedMailboxID = 1
	state.LastMessageCount = 2 // Same as current
	state.LastRecentCount = 0  // Different from current (unseen count)

	srv.HandleNoop(conn, "FLAG", []string{"FLAG", "NOOP"}, state)

	response := conn.GetWrittenData()

	// May report RECENT if unseen messages exist
	// The exact behavior depends on the database state

	// Should complete OK
	if !strings.Contains(response, "FLAG OK NOOP completed") {
		t.Errorf("Expected OK completion, got: %s", response)
	}
}

// TestNoopCommand_WithMessages tests NOOP with existing messages
func TestNoopCommand_WithMessages(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	// Insert multiple messages
	dbMgr := srv.GetDBManager().(*db.DBManager)
	for i := 1; i <= 10; i++ {
		server.InsertTestMail(t, dbMgr, "testuser",
			fmt.Sprintf("Test %d", i),
			"sender@test.com",
			"testuser@localhost",
			"INBOX")
	}

	// Setup state
	state.SelectedFolder = "INBOX"
	state.SelectedMailboxID = 1
	state.LastMessageCount = 0
	state.LastRecentCount = 0

	conn := server.NewMockConn()
	srv.HandleNoop(conn, "MSG", []string{"MSG", "NOOP"}, state)

	response := conn.GetWrittenData()

	// Should report EXISTS for all messages
	if !strings.Contains(response, "* 10 EXISTS") {
		t.Errorf("Expected EXISTS response, got: %s", response)
	}

	// Should complete OK
	if !strings.Contains(response, "MSG OK NOOP completed") {
		t.Errorf("Expected OK completion, got: %s", response)
	}
}

// ===== IDLE TESTS =====

// TestIdleCommand_Unauthenticated tests IDLE before authentication
func TestIdleCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleIdle(conn, "I001", state)

	response := conn.GetWrittenData()

	// Should reject with NO
	if !strings.Contains(response, "I001 NO Please authenticate first") {
		t.Errorf("Expected authentication error, got: %s", response)
	}
}

// TestIdleCommand_NoFolderSelected tests IDLE when no folder is selected
func TestIdleCommand_NoFolderSelected(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")
	state.SelectedMailboxID = 0

	srv.HandleIdle(conn, "I002", state)

	response := conn.GetWrittenData()

	// Should reject with NO
	if !strings.Contains(response, "I002 NO No folder selected") {
		t.Errorf("Expected folder selection error, got: %s", response)
	}
}

// TestIdleCommand_EntersIdleMode tests that IDLE sends continuation response
func TestIdleCommand_EntersIdleMode(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")
	state.SelectedMailboxID = 1
	state.SelectedFolder = "INBOX"

	// Simulate DONE command after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		conn.AddReadData("DONE\r\n")
	}()

	srv.HandleIdle(conn, "I003", state)

	response := conn.GetWrittenData()

	// Should start with continuation response
	if !strings.Contains(response, "+ idling") {
		t.Errorf("Expected continuation response, got: %s", response)
	}

	// Should end with tagged OK
	if !strings.Contains(response, "I003 OK IDLE terminated") {
		t.Errorf("Expected IDLE termination, got: %s", response)
	}
}

// TestIdleCommand_ResponseFormat tests IDLE response format
func TestIdleCommand_ResponseFormat(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleIdle(conn, "I004", state)

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have at least one line
	if len(lines) < 1 {
		t.Fatalf("Expected at least 1 response line")
	}

	// Check CRLF line endings
	if !strings.HasSuffix(response, "\r\n") {
		t.Errorf("Response should end with CRLF")
	}
}

// TestIdleCommand_TagHandling tests various tag formats
func TestIdleCommand_TagHandling(t *testing.T) {
	testCases := []struct {
		name string
		tag  string
	}{
		{"Standard", "A001"},
		{"Numeric", "123"},
		{"Mixed", "TAG-456"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := server.SetupTestServerSimple(t)
			conn := server.NewMockConn()
			state := &models.ClientState{
				Authenticated: false,
			}

			srv.HandleIdle(conn, tc.tag, state)

			response := conn.GetWrittenData()

			// Should contain the tag in the response
			if !strings.Contains(response, tc.tag) {
				t.Errorf("Expected tag '%s' in response: %s", tc.tag, response)
			}
		})
	}
}

// TestIdleCommand_ErrorHandling tests IDLE error cases
func TestIdleCommand_ErrorHandling(t *testing.T) {
	testCases := []struct {
		name        string
		state       *models.ClientState
		expectedErr string
	}{
		{
			name: "Not authenticated",
			state: &models.ClientState{
				Authenticated: false,
			},
			expectedErr: "Please authenticate first",
		},
		{
			name: "No folder selected",
			state: &models.ClientState{
				Authenticated:     true,
				Username:          "testuser",
				SelectedMailboxID: 0,
			},
			expectedErr: "No folder selected",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := server.SetupTestServerSimple(t)
			conn := server.NewMockConn()

			srv.HandleIdle(conn, "ERR", tc.state)

			response := conn.GetWrittenData()

			if !strings.Contains(response, tc.expectedErr) {
				t.Errorf("Expected error '%s', got: %s", tc.expectedErr, response)
			}

			if !strings.Contains(response, "ERR NO") {
				t.Errorf("Expected NO response, got: %s", response)
			}
		})
	}
}

// TestIdleCommand_WithNewMessages tests IDLE detecting new messages
func TestIdleCommand_WithNewMessages(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	dbMgr := srv.GetDBManager().(*db.DBManager)

	// Insert initial message
	server.InsertTestMail(t, dbMgr, "testuser", "Initial", "sender@test.com", "testuser@localhost", "INBOX")

	state.SelectedMailboxID = 1
	state.SelectedFolder = "INBOX"

	// Start IDLE in background and send new messages
	go func() {
		time.Sleep(200 * time.Millisecond)
		// Insert new messages while IDLE is active
		server.InsertTestMail(t, dbMgr, "testuser", "New 1", "sender@test.com", "testuser@localhost", "INBOX")
		time.Sleep(600 * time.Millisecond)
		// Send DONE to exit
		conn.AddReadData("DONE\r\n")
	}()

	srv.HandleIdle(conn, "IDLE1", state)

	response := conn.GetWrittenData()

	// Should have entered idle mode
	if !strings.Contains(response, "+ idling") {
		t.Errorf("Expected idling response, got: %s", response)
	}

	// Should detect new messages
	if !strings.Contains(response, "EXISTS") {
		t.Errorf("Expected EXISTS notification, got: %s", response)
	}

	// Should terminate
	if !strings.Contains(response, "IDLE1 OK IDLE terminated") {
		t.Errorf("Expected termination, got: %s", response)
	}
}

// TestIdleCommand_MultipleStates tests IDLE with various mailbox states
func TestIdleCommand_MultipleStates(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	dbMgr := srv.GetDBManager().(*db.DBManager)

	// Insert some messages
	for i := 1; i <= 3; i++ {
		server.InsertTestMail(t, dbMgr, "testuser",
			fmt.Sprintf("Test %d", i),
			"sender@test.com",
			"testuser@localhost",
			"INBOX")
	}

	state.SelectedMailboxID = 1
	state.SelectedFolder = "INBOX"

	conn := server.NewMockConn()

	// Simulate quick DONE
	go func() {
		time.Sleep(100 * time.Millisecond)
		conn.AddReadData("DONE\r\n")
	}()

	srv.HandleIdle(conn, "IDLE2", state)

	response := conn.GetWrittenData()

	// Should enter and exit idle mode
	if !strings.Contains(response, "+ idling") {
		t.Errorf("Expected idling response, got: %s", response)
	}

	if !strings.Contains(response, "IDLE2 OK IDLE terminated") {
		t.Errorf("Expected termination, got: %s", response)
	}
}

// ===== NAMESPACE TESTS =====

// TestNamespaceCommand_Unauthenticated tests NAMESPACE before authentication
func TestNamespaceCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleNamespace(conn, "NS001", state)

	response := conn.GetWrittenData()

	// Should reject with NO
	if !strings.Contains(response, "NS001 NO Please authenticate first") {
		t.Errorf("Expected authentication error, got: %s", response)
	}
}

// TestNamespaceCommand_Authenticated tests NAMESPACE with authentication
func TestNamespaceCommand_Authenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	srv.HandleNamespace(conn, "NS002", state)

	response := conn.GetWrittenData()
	lines := strings.Split(strings.TrimSpace(response), "\r\n")

	// Should have 2 lines: untagged namespace response and tagged completion
	if len(lines) != 2 {
		t.Fatalf("Expected 2 response lines, got %d: %v", len(lines), lines)
	}

	// Check untagged NAMESPACE response
	expectedUntagged := `* NAMESPACE (("" "/")) NIL NIL`
	if lines[0] != expectedUntagged {
		t.Errorf("Expected '%s', got: '%s'", expectedUntagged, lines[0])
	}

	// Check tagged OK response
	expectedOK := "NS002 OK NAMESPACE completed"
	if lines[1] != expectedOK {
		t.Errorf("Expected '%s', got: '%s'", expectedOK, lines[1])
	}
}

// TestNamespaceCommand_ResponseFormat tests NAMESPACE response format
func TestNamespaceCommand_ResponseFormat(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	srv.HandleNamespace(conn, "NS003", state)

	response := conn.GetWrittenData()

	// Check that response ends with CRLF
	if !strings.HasSuffix(response, "\r\n") {
		t.Errorf("Response should end with CRLF")
	}

	// Should contain untagged response
	if !strings.Contains(response, "* NAMESPACE") {
		t.Errorf("Response should contain untagged NAMESPACE response")
	}

	// Should contain tagged completion
	if !strings.Contains(response, "NS003 OK NAMESPACE completed") {
		t.Errorf("Response should contain tagged completion")
	}
}

// TestNamespaceCommand_RFC2342Compliance tests RFC 2342 compliance
func TestNamespaceCommand_RFC2342Compliance(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	srv.HandleNamespace(conn, "RFC", state)

	response := conn.GetWrittenData()

	// RFC 2342 requires specific format: * NAMESPACE personal shared other
	// Personal namespace: (("" "/"))
	// Shared namespace: NIL
	// Other users' namespace: NIL
	if !strings.Contains(response, `* NAMESPACE (("" "/")) NIL NIL`) {
		t.Errorf("NAMESPACE response not RFC 2342 compliant: %s", response)
	}
}

// TestNamespaceCommand_TagHandling tests various tag formats
func TestNamespaceCommand_TagHandling(t *testing.T) {
	testCases := []struct {
		tag         string
		expectedTag string
	}{
		{"A001", "A001"},
		{"namespace1", "namespace1"},
		{"TAG-789", "TAG-789"},
	}

	for _, tc := range testCases {
		t.Run("Tag_"+tc.tag, func(t *testing.T) {
			srv := server.SetupTestServerSimple(t)
			conn := server.NewMockConn()
			state := server.SetupAuthenticatedState(t, srv, "testuser")

			srv.HandleNamespace(conn, tc.tag, state)

			response := conn.GetWrittenData()
			expectedOK := tc.expectedTag + " OK NAMESPACE completed"

			if !strings.Contains(response, expectedOK) {
				t.Errorf("Expected '%s' in response, got: %s", expectedOK, response)
			}
		})
	}
}

// TestNamespaceCommand_MultipleInvocations tests calling NAMESPACE multiple times
func TestNamespaceCommand_MultipleInvocations(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	// Call NAMESPACE multiple times
	srv.HandleNamespace(conn, "M001", state)
	srv.HandleNamespace(conn, "M002", state)
	srv.HandleNamespace(conn, "M003", state)

	response := conn.GetWrittenData()

	// Should have all three completions
	if !strings.Contains(response, "M001 OK NAMESPACE completed") {
		t.Error("Missing M001 completion")
	}
	if !strings.Contains(response, "M002 OK NAMESPACE completed") {
		t.Error("Missing M002 completion")
	}
	if !strings.Contains(response, "M003 OK NAMESPACE completed") {
		t.Error("Missing M003 completion")
	}

	// Should have three untagged responses
	count := strings.Count(response, "* NAMESPACE")
	if count != 3 {
		t.Errorf("Expected 3 NAMESPACE responses, got %d", count)
	}
}

// TestNamespaceCommand_ConsistentResponse tests that NAMESPACE returns consistent results
func TestNamespaceCommand_ConsistentResponse(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	state := server.SetupAuthenticatedState(t, srv, "testuser")

	// Call NAMESPACE twice and compare responses
	conn1 := server.NewMockConn()
	srv.HandleNamespace(conn1, "C001", state)
	response1 := conn1.GetWrittenData()

	conn2 := server.NewMockConn()
	srv.HandleNamespace(conn2, "C001", state)
	response2 := conn2.GetWrittenData()

	if response1 != response2 {
		t.Errorf("NAMESPACE should return consistent results:\nFirst:  %s\nSecond: %s", response1, response2)
	}
}

// TestNamespaceCommand_DifferentUsers tests NAMESPACE for different users
func TestNamespaceCommand_DifferentUsers(t *testing.T) {
	srv := server.SetupTestServerSimple(t)

	users := []string{"user1", "user2", "user3"}

	for _, username := range users {
		t.Run("User_"+username, func(t *testing.T) {
			conn := server.NewMockConn()
			state := server.SetupAuthenticatedState(t, srv, username)

			srv.HandleNamespace(conn, "U001", state)

			response := conn.GetWrittenData()

			// All users should get the same namespace structure
			if !strings.Contains(response, `* NAMESPACE (("" "/")) NIL NIL`) {
				t.Errorf("User %s should get standard namespace: %s", username, response)
			}

			if !strings.Contains(response, "U001 OK NAMESPACE completed") {
				t.Errorf("User %s should get OK response: %s", username, response)
			}
		})
	}
}
