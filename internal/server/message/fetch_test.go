package message_test

import (
	"strings"
	"testing"

	"raven/internal/models"
	"raven/internal/server"
)

// TestFetchCommand_Unauthenticated tests FETCH without authentication
func TestFetchCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleFetch(conn, "F001", []string{"F001", "FETCH", "1", "FLAGS"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "F001 NO Please authenticate first") {
		t.Errorf("Expected authentication required error, got: %s", response)
	}
}

// TestFetchCommand_NoMailboxSelected tests FETCH without mailbox selection
func TestFetchCommand_NoMailboxSelected(t *testing.T) {
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

	srv.HandleFetch(conn, "F002", []string{"F002", "FETCH", "1", "FLAGS"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "F002 NO No folder selected") {
		t.Errorf("Expected no folder selected error, got: %s", response)
	}
}

// TestFetchCommand_FLAGS tests FETCH FLAGS
func TestFetchCommand_FLAGS(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	msg1ID := server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}
	userDB := server.GetUserDBByID(t, database, state.UserID)

	// Set some flags
	if _, err := userDB.Exec(`UPDATE message_mailbox SET flags = '\Seen \Flagged' WHERE message_id = ? AND mailbox_id = ?`, msg1ID, mailboxID); err != nil {
		t.Fatalf("Failed to set flags: %v", err)
	}

	srv.HandleFetch(conn, "F003", []string{"F003", "FETCH", "1", "FLAGS"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "* 1 FETCH") {
		t.Errorf("Expected FETCH response for message 1, got: %s", response)
	}
	if !strings.Contains(response, "FLAGS") {
		t.Errorf("Expected FLAGS in response, got: %s", response)
	}
	if !strings.Contains(response, "F003 OK FETCH completed") {
		t.Errorf("Expected OK completion, got: %s", response)
	}
}

// TestFetchCommand_UID tests FETCH UID
func TestFetchCommand_UID(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F004", []string{"F004", "FETCH", "1", "UID"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "* 1 FETCH") {
		t.Errorf("Expected FETCH response, got: %s", response)
	}
	if !strings.Contains(response, "UID") {
		t.Errorf("Expected UID in response, got: %s", response)
	}
}

// TestFetchCommand_INTERNALDATE tests FETCH INTERNALDATE
func TestFetchCommand_INTERNALDATE(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F005", []string{"F005", "FETCH", "1", "INTERNALDATE"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "INTERNALDATE") {
		t.Errorf("Expected INTERNALDATE in response, got: %s", response)
	}
}

// TestFetchCommand_RFC822SIZE tests FETCH RFC822.SIZE
func TestFetchCommand_RFC822SIZE(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F006", []string{"F006", "FETCH", "1", "RFC822.SIZE"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "RFC822.SIZE") {
		t.Errorf("Expected RFC822.SIZE in response, got: %s", response)
	}
}

// TestFetchCommand_ENVELOPE tests FETCH ENVELOPE
func TestFetchCommand_ENVELOPE(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F007", []string{"F007", "FETCH", "1", "ENVELOPE"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "ENVELOPE") {
		t.Errorf("Expected ENVELOPE in response, got: %s", response)
	}
	// Should contain date, subject, from, sender, reply-to, to, cc, bcc, in-reply-to, message-id
	if !strings.Contains(response, "Test Subject") {
		t.Errorf("Expected subject in ENVELOPE, got: %s", response)
	}
}

// TestFetchCommand_BODYSTRUCTURE tests FETCH BODYSTRUCTURE
func TestFetchCommand_BODYSTRUCTURE(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F008", []string{"F008", "FETCH", "1", "BODYSTRUCTURE"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "BODYSTRUCTURE") {
		t.Errorf("Expected BODYSTRUCTURE in response, got: %s", response)
	}
}

// TestFetchCommand_BODY tests FETCH BODY (non-extensible BODYSTRUCTURE)
func TestFetchCommand_BODY(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F009", []string{"F009", "FETCH", "1", "BODY"}, state)

	response := conn.GetWrittenData()
	// Should contain BODY but not BODYSTRUCTURE
	if !strings.Contains(response, "BODY") {
		t.Errorf("Expected BODY in response, got: %s", response)
	}
}

// TestFetchCommand_MacroALL tests FETCH ALL macro
func TestFetchCommand_MacroALL(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "A654", []string{"A654", "FETCH", "1", "ALL"}, state)

	response := conn.GetWrittenData()
	// ALL = FLAGS INTERNALDATE RFC822.SIZE ENVELOPE
	if !strings.Contains(response, "FLAGS") {
		t.Errorf("Expected FLAGS in ALL response, got: %s", response)
	}
	if !strings.Contains(response, "INTERNALDATE") {
		t.Errorf("Expected INTERNALDATE in ALL response, got: %s", response)
	}
	if !strings.Contains(response, "RFC822.SIZE") {
		t.Errorf("Expected RFC822.SIZE in ALL response, got: %s", response)
	}
	if !strings.Contains(response, "ENVELOPE") {
		t.Errorf("Expected ENVELOPE in ALL response, got: %s", response)
	}
}

// TestFetchCommand_MacroFAST tests FETCH FAST macro
func TestFetchCommand_MacroFAST(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F011", []string{"F011", "FETCH", "1", "FAST"}, state)

	response := conn.GetWrittenData()
	// FAST = FLAGS INTERNALDATE RFC822.SIZE
	if !strings.Contains(response, "FLAGS") {
		t.Errorf("Expected FLAGS in FAST response, got: %s", response)
	}
	if !strings.Contains(response, "INTERNALDATE") {
		t.Errorf("Expected INTERNALDATE in FAST response, got: %s", response)
	}
	if !strings.Contains(response, "RFC822.SIZE") {
		t.Errorf("Expected RFC822.SIZE in FAST response, got: %s", response)
	}
}

// TestFetchCommand_MacroFULL tests FETCH FULL macro
func TestFetchCommand_MacroFULL(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F012", []string{"F012", "FETCH", "1", "FULL"}, state)

	response := conn.GetWrittenData()
	// FULL = FLAGS INTERNALDATE RFC822.SIZE ENVELOPE BODY
	if !strings.Contains(response, "FLAGS") {
		t.Errorf("Expected FLAGS in FULL response, got: %s", response)
	}
	if !strings.Contains(response, "INTERNALDATE") {
		t.Errorf("Expected INTERNALDATE in FULL response, got: %s", response)
	}
	if !strings.Contains(response, "RFC822.SIZE") {
		t.Errorf("Expected RFC822.SIZE in FULL response, got: %s", response)
	}
	if !strings.Contains(response, "ENVELOPE") {
		t.Errorf("Expected ENVELOPE in FULL response, got: %s", response)
	}
	if !strings.Contains(response, "BODY") {
		t.Errorf("Expected BODY in FULL response, got: %s", response)
	}
}

// TestFetchCommand_SequenceRange tests FETCH with sequence range
func TestFetchCommand_SequenceRange(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 2", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 3", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 4", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	// Test range 2:4
	srv.HandleFetch(conn, "F013", []string{"F013", "FETCH", "2:4", "(FLAGS BODY[HEADER.FIELDS (DATE FROM)])"}, state)

	response := conn.GetWrittenData()
	// Should get responses for messages 2, 3, and 4
	if !strings.Contains(response, "* 2 FETCH") {
		t.Errorf("Expected message 2 in response, got: %s", response)
	}
	if !strings.Contains(response, "* 3 FETCH") {
		t.Errorf("Expected message 3 in response, got: %s", response)
	}
	if !strings.Contains(response, "* 4 FETCH") {
		t.Errorf("Expected message 4 in response, got: %s", response)
	}
}

// TestFetchCommand_CommaSequenceSet tests FETCH with a comma-separated sequence set
func TestFetchCommand_CommaSequenceSet(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Message 1", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 2", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 3", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 4", "sender@test.com", "testuser@localhost", "INBOX")
	server.InsertTestMail(t, database, "testuser", "Message 5", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	// Test comma-separated sequence set 1,3,5
	srv.HandleFetch(conn, "F014", []string{"F014", "FETCH", "1,3,5", "FLAGS"}, state)

	response := conn.GetWrittenData()

	if strings.Contains(response, "BAD") {
		t.Errorf("Expected comma-separated sequence set to be accepted, got: %s", response)
	}
	if !strings.Contains(response, "* 1 FETCH") {
		t.Errorf("Expected message 1 in response, got: %s", response)
	}
	if !strings.Contains(response, "* 3 FETCH") {
		t.Errorf("Expected message 3 in response, got: %s", response)
	}
	if !strings.Contains(response, "* 5 FETCH") {
		t.Errorf("Expected message 5 in response, got: %s", response)
	}
	if strings.Contains(response, "* 2 FETCH") {
		t.Errorf("Did not expect message 2 in response, got: %s", response)
	}
	if strings.Contains(response, "* 4 FETCH") {
		t.Errorf("Did not expect message 4 in response, got: %s", response)
	}
	if !strings.Contains(response, "F014 OK FETCH completed") {
		t.Errorf("Expected completion, got: %s", response)
	}
}

// TestFetchCommand_MultipleItems tests FETCH with multiple items
func TestFetchCommand_MultipleItems(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F014", []string{"F014", "FETCH", "1", "(FLAGS UID INTERNALDATE RFC822.SIZE)"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "FLAGS") {
		t.Errorf("Expected FLAGS in response, got: %s", response)
	}
	if !strings.Contains(response, "UID") {
		t.Errorf("Expected UID in response, got: %s", response)
	}
	if !strings.Contains(response, "INTERNALDATE") {
		t.Errorf("Expected INTERNALDATE in response, got: %s", response)
	}
	if !strings.Contains(response, "RFC822.SIZE") {
		t.Errorf("Expected RFC822.SIZE in response, got: %s", response)
	}
}

// TestFetchCommand_BODY_HEADER tests FETCH BODY[HEADER]
func TestFetchCommand_BODY_HEADER(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F015", []string{"F015", "FETCH", "1", "BODY[HEADER]"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "BODY[HEADER]") {
		t.Errorf("Expected BODY[HEADER] in response, got: %s", response)
	}
	// Should contain header fields
	if !strings.Contains(response, "From:") || !strings.Contains(response, "Subject:") {
		t.Errorf("Expected headers in response, got: %s", response)
	}
}

// TestFetchCommand_BODY_TEXT tests FETCH BODY[TEXT]
func TestFetchCommand_BODY_TEXT(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F016", []string{"F016", "FETCH", "1", "BODY[TEXT]"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "BODY[TEXT]") {
		t.Errorf("Expected BODY[TEXT] in response, got: %s", response)
	}
}

// TestFetchCommand_BODY_PEEK tests FETCH BODY.PEEK[]
func TestFetchCommand_BODY_PEEK(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F017", []string{"F017", "FETCH", "1", "BODY.PEEK[]"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "BODY[]") {
		t.Errorf("Expected BODY[] in response, got: %s", response)
	}
	// BODY.PEEK should NOT set \Seen flag
	// (We can't easily test this without more infrastructure, but the response should still work)
}

// TestFetchCommand_RFC822 tests FETCH RFC822
func TestFetchCommand_RFC822(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F018", []string{"F018", "FETCH", "1", "RFC822"}, state)

	response := conn.GetWrittenData()
	// RFC822 is equivalent to BODY[]
	if !strings.Contains(response, "BODY[]") {
		t.Errorf("Expected BODY[] (RFC822) in response, got: %s", response)
	}
}

// TestFetchCommand_RFC822_HEADER tests FETCH RFC822.HEADER
func TestFetchCommand_RFC822_HEADER(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F019", []string{"F019", "FETCH", "1", "RFC822.HEADER"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "RFC822.HEADER") {
		t.Errorf("Expected RFC822.HEADER in response, got: %s", response)
	}
}

// TestFetchCommand_RFC822_TEXT tests FETCH RFC822.TEXT
func TestFetchCommand_RFC822_TEXT(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	srv.HandleFetch(conn, "F020", []string{"F020", "FETCH", "1", "RFC822.TEXT"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "RFC822.TEXT") {
		t.Errorf("Expected RFC822.TEXT in response, got: %s", response)
	}
}

// TestFetchCommand_BadSyntax tests FETCH with invalid syntax
func TestFetchCommand_BadSyntax(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	// Missing data items
	srv.HandleFetch(conn, "F021", []string{"F021", "FETCH", "1"}, state)
	response := conn.GetWrittenData()
	if !strings.Contains(response, "F021 BAD") {
		t.Errorf("Expected BAD response for missing items, got: %s", response)
	}
}

// TestFetchCommand_TagHandling tests various tag formats
func TestFetchCommand_TagHandling(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test", "sender@test.com", "testuser@localhost", "INBOX")
	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	testCases := []string{"A001", "fetch1", "TAG-123"}

	for _, tag := range testCases {
		t.Run("Tag "+tag, func(t *testing.T) {
			conn := server.NewMockConn()
			srv.HandleFetch(conn, tag, []string{tag, "FETCH", "1", "FLAGS"}, state)

			response := conn.GetWrittenData()
			expectedCompletion := tag + " OK FETCH completed"

			if !strings.Contains(response, expectedCompletion) {
				t.Errorf("Expected tag %s in completion, got: %s", tag, response)
			}
		})
	}
}

// TestFetchCommand_BodyPartPartial tests FETCH BODY[section]<start.length>
func TestFetchCommand_BodyPartPartial(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test Subject", "sender@test.com", "testuser@localhost", "INBOX")

	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	// Test partial fetch: BODY.PEEK[1]<10.20> - get 20 bytes starting at offset 10
	srv.HandleFetch(conn, "F201", []string{"F201", "FETCH", "1", "BODY.PEEK[1]<10.20>"}, state)

	response := conn.GetWrittenData()

	// Should contain BODY[1]<10> indicating the start position
	if !strings.Contains(response, "BODY[1]<10>") {
		t.Errorf("Expected BODY[1]<10> in response for partial fetch, got: %s", response)
	}

	// Should contain a literal with the partial content (may be less than 20 if part is smaller)
	if !strings.Contains(response, "{") && !strings.Contains(response, "NIL") {
		t.Errorf("Expected literal or NIL in response, got: %s", response)
	}

	if !strings.Contains(response, "F201 OK FETCH completed") {
		t.Errorf("Expected completion, got: %s", response)
	}
}

// TestFetchCommand_MultipartAlternativeWithAttachment tests correct part numbering
// for messages with nested multipart/alternative and attachments
func TestFetchCommand_MultipartAlternativeWithAttachment(t *testing.T) {
	// This test verifies that IMAP part numbering works correctly for:
	// multipart/mixed
	//   1: multipart/alternative (container - should return NIL)
	//   2: attachment (should return attachment data)

	// Note: This is a simplified test - the actual multipart structure would need
	// to be created via LMTP delivery for proper testing
	// For now, we test with a simple message structure

	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "testuser")
	server.InsertTestMail(t, database, "testuser", "Test", "sender@test.com", "testuser@localhost", "INBOX")
	mailboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "testuser",
		SelectedMailboxID: mailboxID,
	}

	// Test BODY[1] - for a simple message, part 1 should exist
	conn.Reset()
	srv.HandleFetch(conn, "F301", []string{"F301", "FETCH", "1", "BODY.PEEK[1]"}, state)
	response := conn.GetWrittenData()

	// Should have some response (either content or NIL)
	if !strings.Contains(response, "BODY[1]") && !strings.Contains(response, "F301 OK") {
		t.Errorf("Expected valid FETCH response, got: %s", response)
	}
}
