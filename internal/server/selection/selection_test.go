package selection_test

import (
	"strings"
	"testing"

	"raven/internal/models"
	"raven/internal/server"
)

// ============================================================================
// SELECT Command Tests
// ============================================================================

func TestSelectCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleSelect(conn, "A001", []string{"A001", "SELECT", "INBOX"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A001 NO Please authenticate first") {
		t.Errorf("Expected authentication error, got: %s", response)
	}
}

func TestSelectCommand_MissingFolderName(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleSelect(conn, "A002", []string{"A002", "SELECT"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A002 BAD SELECT requires folder name") {
		t.Errorf("Expected BAD response for missing folder, got: %s", response)
	}
}

func TestSelectCommand_NonExistentMailbox(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleSelect(conn, "A003", []string{"A003", "SELECT", "NonExistent"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A003 NO [TRYCREATE] Mailbox does not exist") {
		t.Errorf("Expected TRYCREATE error, got: %s", response)
	}
}

func TestSelectCommand_Success(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleSelect(conn, "A004", []string{"A004", "SELECT", "INBOX"}, state)

	response := conn.GetWrittenData()

	// Check for required responses per RFC 3501
	if !strings.Contains(response, "* FLAGS") {
		t.Errorf("Missing FLAGS response, got: %s", response)
	}
	if !strings.Contains(response, "EXISTS") {
		t.Errorf("Missing EXISTS response, got: %s", response)
	}
	if !strings.Contains(response, "RECENT") {
		t.Errorf("Missing RECENT response, got: %s", response)
	}
	if !strings.Contains(response, "UIDVALIDITY") {
		t.Errorf("Missing UIDVALIDITY response, got: %s", response)
	}
	if !strings.Contains(response, "UIDNEXT") {
		t.Errorf("Missing UIDNEXT response, got: %s", response)
	}
	if !strings.Contains(response, "PERMANENTFLAGS") {
		t.Errorf("Missing PERMANENTFLAGS response, got: %s", response)
	}
	if !strings.Contains(response, "A004 OK [READ-WRITE] SELECT completed") {
		t.Errorf("Expected successful SELECT completion, got: %s", response)
	}

	// Verify state is updated
	if state.SelectedMailboxID == 0 {
		t.Error("SelectedMailboxID should be set")
	}
	if state.SelectedFolder != "INBOX" {
		t.Errorf("Expected SelectedFolder to be INBOX, got: %s", state.SelectedFolder)
	}
}

func TestSelectCommand_WithQuotedName(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleSelect(conn, "A005", []string{"A005", "SELECT", "\"INBOX\""}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A005 OK [READ-WRITE] SELECT completed") {
		t.Errorf("Expected successful SELECT with quoted name, got: %s", response)
	}
	// Folder name should be stripped of quotes
	if state.SelectedFolder != "INBOX" {
		t.Errorf("Expected SelectedFolder to be INBOX, got: %s", state.SelectedFolder)
	}
}

func TestSelectCommand_EmptyMailbox(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleSelect(conn, "A006", []string{"A006", "SELECT", "INBOX"}, state)

	response := conn.GetWrittenData()
	// Empty mailbox should have 0 EXISTS and 0 RECENT
	if !strings.Contains(response, "* 0 EXISTS") {
		t.Errorf("Expected 0 EXISTS for empty mailbox, got: %s", response)
	}
	if !strings.Contains(response, "* 0 RECENT") {
		t.Errorf("Expected 0 RECENT for empty mailbox, got: %s", response)
	}
	if !strings.Contains(response, "A006 OK [READ-WRITE] SELECT completed") {
		t.Errorf("Expected successful completion, got: %s", response)
	}
}

func TestSelectCommand_MultipleMessages(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 2", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 3", "sender@test.com", "testuser@localhost", "INBOX")

	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleSelect(conn, "A007", []string{"A007", "SELECT", "INBOX"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "* 3 EXISTS") {
		t.Errorf("Expected 3 EXISTS, got: %s", response)
	}
}

func TestSelectCommand_StateTracking(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 2", "sender@test.com", "testuser@localhost", "INBOX")

	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleSelect(conn, "A008", []string{"A008", "SELECT", "INBOX"}, state)

	// Verify state tracking fields are set
	if state.LastMessageCount != 2 {
		t.Errorf("Expected LastMessageCount to be 2, got: %d", state.LastMessageCount)
	}
	if state.UIDValidity == 0 {
		t.Error("UIDValidity should be set")
	}
	if state.UIDNext == 0 {
		t.Error("UIDNext should be set")
	}
}

// ============================================================================
// EXAMINE Command Tests
// ============================================================================

func TestExamineCommand_Success(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleExamine(conn, "A009", []string{"A009", "EXAMINE", "INBOX"}, state)

	response := conn.GetWrittenData()

	// EXAMINE should return READ-ONLY
	if !strings.Contains(response, "A009 OK [READ-ONLY] EXAMINE completed") {
		t.Errorf("Expected READ-ONLY completion for EXAMINE, got: %s", response)
	}

	// EXAMINE should have empty PERMANENTFLAGS
	if !strings.Contains(response, "[PERMANENTFLAGS ()]") {
		t.Errorf("Expected empty PERMANENTFLAGS for EXAMINE, got: %s", response)
	}

	// Should still have FLAGS response
	if !strings.Contains(response, "* FLAGS") {
		t.Errorf("Missing FLAGS response, got: %s", response)
	}
}

func TestExamineCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleExamine(conn, "A010", []string{"A010", "EXAMINE", "INBOX"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A010 NO Please authenticate first") {
		t.Errorf("Expected authentication error, got: %s", response)
	}
}

func TestExamineCommand_MissingFolderName(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	srv.HandleExamine(conn, "A011", []string{"A011", "EXAMINE"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "A011 BAD EXAMINE requires folder name") {
		t.Errorf("Expected BAD response for missing folder, got: %s", response)
	}
}

// ============================================================================
// CLOSE Command Tests
// ============================================================================

func TestCloseCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleClose(conn, "C001", state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "C001 NO Please authenticate first") {
		t.Errorf("Expected authentication error, got: %s", response)
	}
}

func TestCloseCommand_NoMailboxSelected(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: 0, // No mailbox selected
	}

	srv.HandleClose(conn, "C002", state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "C002 NO No mailbox selected") {
		t.Errorf("Expected no mailbox error, got: %s", response)
	}
}

func TestCloseCommand_Success(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
		SelectedFolder:    "INBOX",
		LastMessageCount:  1,
		LastRecentCount:   1,
		UIDValidity:       123,
		UIDNext:           456,
	}

	srv.HandleClose(conn, "C003", state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "C003 OK CLOSE completed") {
		t.Errorf("Expected successful CLOSE, got: %s", response)
	}

	// Verify state is reset to authenticated state
	if state.SelectedMailboxID != 0 {
		t.Error("SelectedMailboxID should be 0 after CLOSE")
	}
	if state.SelectedFolder != "" {
		t.Error("SelectedFolder should be empty after CLOSE")
	}
	if state.LastMessageCount != 0 {
		t.Error("LastMessageCount should be 0 after CLOSE")
	}
	if state.LastRecentCount != 0 {
		t.Error("LastRecentCount should be 0 after CLOSE")
	}
	if state.UIDValidity != 0 {
		t.Error("UIDValidity should be 0 after CLOSE")
	}
	if state.UIDNext != 0 {
		t.Error("UIDNext should be 0 after CLOSE")
	}
	// Should still be authenticated
	if !state.Authenticated {
		t.Error("Should still be authenticated after CLOSE")
	}
}

func TestCloseCommand_NoExpungeResponses(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
		SelectedFolder:    "INBOX",
	}

	srv.HandleClose(conn, "C004", state)

	response := conn.GetWrittenData()
	// Per RFC 3501, CLOSE should not send EXPUNGE responses
	if strings.Contains(response, "EXPUNGE") {
		t.Errorf("CLOSE should not send EXPUNGE responses, got: %s", response)
	}
}

// ============================================================================
// UNSELECT Command Tests
// ============================================================================

func TestUnselectCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleUnselect(conn, "U001", state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U001 NO Please authenticate first") {
		t.Errorf("Expected authentication error, got: %s", response)
	}
}

func TestUnselectCommand_NoMailboxSelected(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: 0, // No mailbox selected
	}

	srv.HandleUnselect(conn, "U002", state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U002 NO No folder selected") {
		t.Errorf("Expected no folder error, got: %s", response)
	}
}

func TestUnselectCommand_Success(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
		SelectedFolder:    "INBOX",
		LastMessageCount:  1,
		LastRecentCount:   1,
		UIDValidity:       123,
		UIDNext:           456,
	}

	srv.HandleUnselect(conn, "U003", state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U003 OK UNSELECT completed") {
		t.Errorf("Expected successful UNSELECT, got: %s", response)
	}

	// Verify state is reset (similar to CLOSE)
	if state.SelectedMailboxID != 0 {
		t.Error("SelectedMailboxID should be 0 after UNSELECT")
	}
	if state.SelectedFolder != "" {
		t.Error("SelectedFolder should be empty after UNSELECT")
	}
	if state.LastMessageCount != 0 {
		t.Error("LastMessageCount should be 0 after UNSELECT")
	}
	if state.LastRecentCount != 0 {
		t.Error("LastRecentCount should be 0 after UNSELECT")
	}
	if state.UIDValidity != 0 {
		t.Error("UIDValidity should be 0 after UNSELECT")
	}
	if state.UIDNext != 0 {
		t.Error("UIDNext should be 0 after UNSELECT")
	}
	// Should still be authenticated
	if !state.Authenticated {
		t.Error("Should still be authenticated after UNSELECT")
	}
}

func TestUnselectCommand_NoExpunge(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
		SelectedFolder:    "INBOX",
	}

	srv.HandleUnselect(conn, "U004", state)

	response := conn.GetWrittenData()
	// UNSELECT should not expunge messages (unlike CLOSE)
	if strings.Contains(response, "EXPUNGE") {
		t.Errorf("UNSELECT should not send EXPUNGE responses, got: %s", response)
	}
}

// ============================================================================
// Additional Coverage Tests
// ============================================================================

func TestCloseCommand_DeletesMarkedMessages(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")

	// Insert multiple messages
	msg1ID := server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 2", "sender@test.com", "testuser@localhost", "INBOX")
	msg3ID := server.InsertTestMail(t, database, "testuser", "Message 3", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")
	userDB := server.GetUserDBByID(t, database, userID)

	// Mark messages 1 and 3 as deleted
	_, _ = userDB.Exec(`UPDATE message_mailbox SET flags = '\Deleted' WHERE message_id = ? AND mailbox_id = ?`, msg1ID, mailboxID)
	_, _ = userDB.Exec(`UPDATE message_mailbox SET flags = '\Deleted' WHERE message_id = ? AND mailbox_id = ?`, msg3ID, mailboxID)

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
		SelectedFolder:    "INBOX",
	}

	// Count messages before CLOSE
	var countBefore int
	_ = userDB.QueryRow(`SELECT COUNT(*) FROM message_mailbox WHERE mailbox_id = ?`, mailboxID).Scan(&countBefore)
	if countBefore != 3 {
		t.Fatalf("Expected 3 messages before CLOSE, got %d", countBefore)
	}

	srv.HandleClose(conn, "C005", state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "C005 OK CLOSE completed") {
		t.Errorf("Expected successful CLOSE, got: %s", response)
	}

	// Count messages after CLOSE - should have only 1 left (message 2)
	var countAfter int
	_ = userDB.QueryRow(`SELECT COUNT(*) FROM message_mailbox WHERE mailbox_id = ?`, mailboxID).Scan(&countAfter)
	if countAfter != 1 {
		t.Errorf("Expected 1 message after CLOSE (deleted messages removed), got %d", countAfter)
	}

	// Verify no messages with \Deleted flag remain
	var deletedCount int
	_ = userDB.QueryRow(`SELECT COUNT(*) FROM message_mailbox WHERE mailbox_id = ? AND flags LIKE '%\\Deleted%'`, mailboxID).Scan(&deletedCount)
	if deletedCount != 0 {
		t.Errorf("Expected 0 deleted messages after CLOSE, got %d", deletedCount)
	}
}

func TestSelectCommand_SwitchingMailboxes(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Inbox Message", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Sent Message 1", "sender@test.com", "testuser@localhost", "Sent")
	server.InsertTestMail(t, database, "testuser", "Sent Message 2", "sender@test.com", "testuser@localhost", "Sent")

	state := &models.ClientState{
		Authenticated: true,
		UserID:        userID,
		Username:      "testuser",
	}

	// First, select INBOX
	srv.HandleSelect(conn, "A014", []string{"A014", "SELECT", "INBOX"}, state)
	response1 := conn.GetWrittenData()

	if !strings.Contains(response1, "* 1 EXISTS") {
		t.Errorf("Expected 1 message in INBOX, got: %s", response1)
	}
	if state.SelectedFolder != "INBOX" {
		t.Errorf("Expected SelectedFolder to be INBOX, got: %s", state.SelectedFolder)
	}

	firstMailboxID := state.SelectedMailboxID

	// Clear buffer and select Sent
	conn.ClearWriteBuffer()
	srv.HandleSelect(conn, "A015", []string{"A015", "SELECT", "Sent"}, state)
	response2 := conn.GetWrittenData()

	if !strings.Contains(response2, "* 2 EXISTS") {
		t.Errorf("Expected 2 messages in Sent, got: %s", response2)
	}
	if state.SelectedFolder != "Sent" {
		t.Errorf("Expected SelectedFolder to be Sent, got: %s", state.SelectedFolder)
	}

	// SelectedMailboxID should have changed
	if state.SelectedMailboxID == firstMailboxID {
		t.Error("SelectedMailboxID should change when switching mailboxes")
	}
}

func TestCloseCommand_DatabaseError(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            999999, // Invalid user ID to trigger DB error
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
		SelectedFolder:    "INBOX",
	}

	srv.HandleClose(conn, "C006", state)

	response := conn.GetWrittenData()
	// Should complete even with DB error (per implementation)
	if !strings.Contains(response, "C006 OK CLOSE completed") {
		t.Errorf("Expected CLOSE to complete even with DB error, got: %s", response)
	}

	// State should still be reset
	if state.SelectedMailboxID != 0 {
		t.Error("SelectedMailboxID should be 0 after CLOSE")
	}
}
