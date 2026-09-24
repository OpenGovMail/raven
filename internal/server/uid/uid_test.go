package uid_test

import (
	"fmt"
	"strings"
	"testing"

	"raven/internal/models"
	"raven/internal/server"
)

// TestUIDCommand_Unauthenticated tests UID command without authentication
func TestUIDCommand_Unauthenticated(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()

	state := &models.ClientState{
		Authenticated: false,
	}

	srv.HandleUID(conn, "U001", []string{"UID", "UID", "FETCH", "1", "FLAGS"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U001 NO Please authenticate first") {
		t.Errorf("Expected authentication error, got: %s", response)
	}
}

// TestUIDCommand_NoMailboxSelected tests UID command without mailbox selection
func TestUIDCommand_NoMailboxSelected(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: 0,
	}

	srv.HandleUID(conn, "U002", []string{"UID", "UID", "FETCH", "1", "FLAGS"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U002 NO No mailbox selected") {
		t.Errorf("Expected no mailbox selected error, got: %s", response)
	}
}

// TestUIDFetch_RFC3501Example tests RFC 3501 example: UID FETCH 4827313:4828442 FLAGS
func TestUIDFetch_RFC3501Example(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")

	// Insert messages with specific UIDs
	msgID1 := server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	msgID2 := server.InsertTestMail(t, database, "uiduser", "Message 2", "sender@test.com", "uiduser@localhost", "INBOX")
	msgID3 := server.InsertTestMail(t, database, "uiduser", "Message 3", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}
	userDB := server.GetUserDBByID(t, database, state.UserID)

	// Set specific UIDs (simulating real UIDs)
	if _, err := userDB.Exec("UPDATE message_mailbox SET uid = 4827313, flags = '\\Seen' WHERE message_id = ?", msgID1); err != nil {
		t.Fatalf("Failed to set uid for msg1: %v", err)
	}
	if _, err := userDB.Exec("UPDATE message_mailbox SET uid = 4827943, flags = '\\Seen' WHERE message_id = ?", msgID2); err != nil {
		t.Fatalf("Failed to set uid for msg2: %v", err)
	}
	if _, err := userDB.Exec("UPDATE message_mailbox SET uid = 4828442, flags = '\\Seen' WHERE message_id = ?", msgID3); err != nil {
		t.Fatalf("Failed to set uid for msg3: %v", err)
	}

	// UID FETCH 4827313:4828442 FLAGS
	srv.HandleUID(conn, "A999", []string{"UID", "UID", "FETCH", "4827313:4828442", "FLAGS"}, state)

	response := conn.GetWrittenData()

	// Should have 3 FETCH responses
	if !strings.Contains(response, "* 1 FETCH") {
		t.Errorf("Expected message 1 FETCH response, got: %s", response)
	}
	if !strings.Contains(response, "UID 4827313") {
		t.Errorf("Expected UID 4827313, got: %s", response)
	}
	if !strings.Contains(response, "* 2 FETCH") {
		t.Errorf("Expected message 2 FETCH response, got: %s", response)
	}
	if !strings.Contains(response, "UID 4827943") {
		t.Errorf("Expected UID 4827943, got: %s", response)
	}
	if !strings.Contains(response, "* 3 FETCH") {
		t.Errorf("Expected message 3 FETCH response, got: %s", response)
	}
	if !strings.Contains(response, "UID 4828442") {
		t.Errorf("Expected UID 4828442, got: %s", response)
	}
	if !strings.Contains(response, "A999 OK UID FETCH completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDFetch_AlwaysIncludesUID tests that UID is always included in response
func TestUIDFetch_AlwaysIncludesUID(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID FETCH 1 FLAGS (UID not explicitly requested)
	srv.HandleUID(conn, "U003", []string{"UID", "UID", "FETCH", "1", "FLAGS"}, state)

	response := conn.GetWrittenData()

	// UID must be included even though not requested
	if !strings.Contains(response, "UID 1") {
		t.Errorf("Expected UID to be included in response, got: %s", response)
	}
	if !strings.Contains(response, "FLAGS") {
		t.Errorf("Expected FLAGS in response, got: %s", response)
	}
}

// TestUIDFetch_NonExistentUID tests that non-existent UIDs are silently ignored
func TestUIDFetch_NonExistentUID(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID FETCH 999 FLAGS (non-existent UID)
	srv.HandleUID(conn, "U004", []string{"UID", "UID", "FETCH", "999", "FLAGS"}, state)

	response := conn.GetWrittenData()

	// Should return OK without error or data
	if !strings.Contains(response, "U004 OK UID FETCH completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
	// Should not have FETCH response
	if strings.Contains(response, "* ") && strings.Contains(response, "FETCH") {
		t.Errorf("Expected no FETCH response for non-existent UID, got: %s", response)
	}
}

// TestUIDFetch_StarRange tests UID FETCH with * (highest UID)
func TestUIDFetch_StarRange(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 2", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 3", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID FETCH * FLAGS
	srv.HandleUID(conn, "U005", []string{"UID", "UID", "FETCH", "*", "FLAGS"}, state)

	response := conn.GetWrittenData()

	// Should fetch only the last message
	if !strings.Contains(response, "* 3 FETCH") {
		t.Errorf("Expected message 3 FETCH response, got: %s", response)
	}
	if !strings.Contains(response, "UID 3") {
		t.Errorf("Expected UID 3, got: %s", response)
	}
}

// TestUIDSearch_ALL tests UID SEARCH ALL
func TestUIDSearch_ALL(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 2", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 3", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID SEARCH ALL
	srv.HandleUID(conn, "U006", []string{"UID", "UID", "SEARCH", "ALL"}, state)

	response := conn.GetWrittenData()

	// Should return UIDs (not sequence numbers)
	if !strings.Contains(response, "* SEARCH 1 2 3") {
		t.Errorf("Expected UIDs 1 2 3, got: %s", response)
	}
	if !strings.Contains(response, "U006 OK UID SEARCH completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDSearch_UIDRange tests UID SEARCH with UID range
func TestUIDSearch_UIDRange(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")

	// Insert messages with specific UIDs
	msgID1 := server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	msgID2 := server.InsertTestMail(t, database, "uiduser", "Message 2", "sender@test.com", "uiduser@localhost", "INBOX")
	msgID3 := server.InsertTestMail(t, database, "uiduser", "Message 3", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}
	userDB := server.GetUserDBByID(t, database, state.UserID)

	// Set specific UIDs
	if _, err := userDB.Exec("UPDATE message_mailbox SET uid = 443 WHERE message_id = ?", msgID1); err != nil {
		t.Fatalf("Failed to set uid 443: %v", err)
	}
	if _, err := userDB.Exec("UPDATE message_mailbox SET uid = 495 WHERE message_id = ?", msgID2); err != nil {
		t.Fatalf("Failed to set uid 495: %v", err)
	}
	if _, err := userDB.Exec("UPDATE message_mailbox SET uid = 557 WHERE message_id = ?", msgID3); err != nil {
		t.Fatalf("Failed to set uid 557: %v", err)
	}

	// UID SEARCH 1:100 UID 443:557
	srv.HandleUID(conn, "U007", []string{"UID", "UID", "SEARCH", "1:100", "UID", "443:557"}, state)

	response := conn.GetWrittenData()

	// Should return UIDs in the UID range
	if !strings.Contains(response, "443") && !strings.Contains(response, "495") && !strings.Contains(response, "557") {
		t.Errorf("Expected UIDs 443, 495, 557, got: %s", response)
	}
}

// TestUIDSearch_SeenUnseen tests UID SEARCH SEEN and UNSEEN.
func TestUIDSearch_SeenUnseen(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	msg1ID := server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 2", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "uiduser",
		SelectedMailboxID: inboxID,
	}
	userDB := server.GetUserDBByID(t, database, state.UserID)

	if _, err := userDB.Exec(`UPDATE message_mailbox SET flags = '\Seen' WHERE message_id = ? AND mailbox_id = ?`, msg1ID, inboxID); err != nil {
		t.Fatalf("Failed to mark message as seen: %v", err)
	}

	srv.HandleUID(conn, "U027", []string{"UID", "UID", "SEARCH", "SEEN"}, state)
	response := conn.GetWrittenData()
	if !strings.Contains(response, "* SEARCH 1") {
		t.Errorf("Expected UID 1 in SEEN results, got: %s", response)
	}

	conn.ClearWriteBuffer()

	srv.HandleUID(conn, "U028", []string{"UID", "UID", "SEARCH", "UNSEEN"}, state)
	response = conn.GetWrittenData()
	if !strings.Contains(response, "* SEARCH 2") {
		t.Errorf("Expected UID 2 in UNSEEN results, got: %s", response)
	}
}

// TestUIDStore_FLAGS tests UID STORE FLAGS operation
func TestUIDStore_FLAGS(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID STORE 1 FLAGS (\Deleted)
	srv.HandleUID(conn, "U008", []string{"UID", "UID", "STORE", "1", "FLAGS", "(\\Deleted)"}, state)

	response := conn.GetWrittenData()

	// Should return FETCH with UID
	if !strings.Contains(response, "* 1 FETCH") {
		t.Errorf("Expected FETCH response, got: %s", response)
	}
	if !strings.Contains(response, "UID 1") {
		t.Errorf("Expected UID in response, got: %s", response)
	}
	if !strings.Contains(response, "\\Deleted") {
		t.Errorf("Expected \\Deleted flag, got: %s", response)
	}
	if !strings.Contains(response, "U008 OK UID STORE completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDStore_SILENT tests UID STORE with .SILENT suffix
func TestUIDStore_SILENT(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID STORE 1 FLAGS.SILENT (\Deleted)
	srv.HandleUID(conn, "U009", []string{"UID", "UID", "STORE", "1", "FLAGS.SILENT", "(\\Deleted)"}, state)

	response := conn.GetWrittenData()

	// Should NOT return FETCH response
	if strings.Contains(response, "* 1 FETCH") {
		t.Errorf("Expected no FETCH response with .SILENT, got: %s", response)
	}
	if !strings.Contains(response, "U009 OK UID STORE completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDStore_NonExistentUID tests UID STORE with non-existent UID
func TestUIDStore_NonExistentUID(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID STORE 999 FLAGS (\Deleted) - non-existent UID
	srv.HandleUID(conn, "U010", []string{"UID", "UID", "STORE", "999", "FLAGS", "(\\Deleted)"}, state)

	response := conn.GetWrittenData()

	// Should return OK without error
	if !strings.Contains(response, "U010 OK UID STORE completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDCopy_SingleMessage tests UID COPY with single UID
func TestUIDCopy_SingleMessage(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.CreateMailbox(t, database, "uiduser", "Sent")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")
	sentID, _ := server.GetMailboxID(t, database, userID, "Sent")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}
	userDB := server.GetUserDBByID(t, database, state.UserID)

	// UID COPY 1 Sent
	srv.HandleUID(conn, "U011", []string{"UID", "UID", "COPY", "1", "Sent"}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "U011 OK UID COPY completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}

	// Verify message was copied
	var count int
	if err := userDB.QueryRow("SELECT COUNT(*) FROM message_mailbox WHERE mailbox_id = ?", sentID).Scan(&count); err != nil {
		t.Fatalf("Failed to query sent mailbox count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 message in Sent folder, got %d", count)
	}
}

// TestUIDCopy_UIDRange tests UID COPY with UID range
func TestUIDCopy_UIDRange(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 2", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 3", "sender@test.com", "uiduser@localhost", "INBOX")
	server.CreateMailbox(t, database, "uiduser", "Archive")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")
	archiveID, _ := server.GetMailboxID(t, database, userID, "Archive")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}
	userDB := server.GetUserDBByID(t, database, state.UserID)

	// UID COPY 1:2 Archive
	srv.HandleUID(conn, "U012", []string{"UID", "UID", "COPY", "1:2", "Archive"}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "U012 OK UID COPY completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}

	// Verify 2 messages were copied
	var count int
	if err := userDB.QueryRow("SELECT COUNT(*) FROM message_mailbox WHERE mailbox_id = ?", archiveID).Scan(&count); err != nil {
		t.Fatalf("Failed to query archive mailbox count: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 messages in Archive folder, got %d", count)
	}
}

// TestUIDCopy_NonExistentDestination tests UID COPY to non-existent mailbox
func TestUIDCopy_NonExistentDestination(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID COPY 1 NonExistent
	srv.HandleUID(conn, "U013", []string{"UID", "UID", "COPY", "1", "NonExistent"}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "U013 NO [TRYCREATE]") {
		t.Errorf("Expected TRYCREATE response, got: %s", response)
	}
}

// TestUIDCopy_NonExistentUID tests UID COPY with non-existent UID
func TestUIDCopy_NonExistentUID(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.CreateMailbox(t, database, "uiduser", "Sent")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID COPY 999 Sent (non-existent UID)
	srv.HandleUID(conn, "U014", []string{"UID", "UID", "COPY", "999", "Sent"}, state)

	response := conn.GetWrittenData()

	// Should return OK without error
	if !strings.Contains(response, "U014 OK UID COPY completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDCommand_BadSubCommand tests UID with unknown sub-command
func TestUIDCommand_BadSubCommand(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID INVALID
	srv.HandleUID(conn, "U015", []string{"UID", "UID", "INVALID"}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "U015 BAD Unknown UID command: INVALID") {
		t.Errorf("Expected BAD response, got: %s", response)
	}
}

// TestUIDCommand_TagHandling tests various tag formats
func TestUIDCommand_TagHandling(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	testCases := []struct {
		tag         string
		expectedTag string
	}{
		{"A001", "A001"},
		{"uid1", "uid1"},
		{"TAG-123", "TAG-123"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Tag_%s", tc.tag), func(t *testing.T) {
			conn.ClearWriteBuffer()
			srv.HandleUID(conn, tc.tag, []string{"UID", "UID", "FETCH", "1", "FLAGS"}, state)

			response := conn.GetWrittenData()
			if !strings.Contains(response, fmt.Sprintf("%s OK UID FETCH completed", tc.expectedTag)) {
				t.Errorf("Expected tag %s in response, got: %s", tc.expectedTag, response)
			}
		})
	}
}

// ============================================================================
// Additional Coverage Tests
// ============================================================================

// TestUIDCommand_MissingSubCommand tests UID without sub-command
func TestUIDCommand_MissingSubCommand(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID without sub-command
	srv.HandleUID(conn, "U016", []string{"UID", "UID"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U016 BAD UID requires sub-command") {
		t.Errorf("Expected BAD response for missing sub-command, got: %s", response)
	}
}

// TestUIDFetch_MissingArguments tests UID FETCH without required arguments
func TestUIDFetch_MissingArguments(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID FETCH without UID sequence
	srv.HandleUID(conn, "U017", []string{"UID", "UID", "FETCH"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U017 BAD UID FETCH requires UID sequence and items") {
		t.Errorf("Expected BAD response, got: %s", response)
	}
}

// TestUIDSearch_MissingCriteria tests UID SEARCH without criteria
func TestUIDSearch_MissingCriteria(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID SEARCH without criteria
	srv.HandleUID(conn, "U018", []string{"UID", "UID", "SEARCH"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U018 BAD UID SEARCH requires search criteria") {
		t.Errorf("Expected BAD response, got: %s", response)
	}
}

// TestUIDStore_MissingArguments tests UID STORE without required arguments
func TestUIDStore_MissingArguments(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID STORE without flags
	srv.HandleUID(conn, "U019", []string{"UID", "UID", "STORE", "1"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U019 BAD UID STORE requires UID sequence, operation, and flags") {
		t.Errorf("Expected BAD response, got: %s", response)
	}
}

// TestUIDStore_InvalidDataItem tests UID STORE with invalid data item
func TestUIDStore_InvalidDataItem(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID STORE with invalid data item
	srv.HandleUID(conn, "U020", []string{"UID", "UID", "STORE", "1", "INVALID", "(\\Deleted)"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U020 BAD Invalid data item: INVALID") {
		t.Errorf("Expected BAD response for invalid data item, got: %s", response)
	}
}

// TestUIDStore_AddFlags tests UID STORE +FLAGS operation
func TestUIDStore_AddFlags(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")
	userDB := server.GetUserDBByID(t, database, userID)

	// Set initial flag
	if _, err := userDB.Exec("UPDATE message_mailbox SET flags = '\\Seen' WHERE mailbox_id = ?", inboxID); err != nil {
		t.Fatalf("Failed to set initial flag: %v", err)
	}

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID STORE 1 +FLAGS (\Deleted)
	srv.HandleUID(conn, "U021", []string{"UID", "UID", "STORE", "1", "+FLAGS", "(\\Deleted)"}, state)

	response := conn.GetWrittenData()

	// Should have both \Seen and \Deleted
	if !strings.Contains(response, "\\Seen") || !strings.Contains(response, "\\Deleted") {
		t.Errorf("Expected both \\Seen and \\Deleted flags, got: %s", response)
	}
	if !strings.Contains(response, "U021 OK UID STORE completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDStore_RemoveFlags tests UID STORE -FLAGS operation
func TestUIDStore_RemoveFlags(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")
	userDB := server.GetUserDBByID(t, database, userID)

	// Set multiple flags
	if _, err := userDB.Exec("UPDATE message_mailbox SET flags = '\\Seen \\Deleted' WHERE mailbox_id = ?", inboxID); err != nil {
		t.Fatalf("Failed to set multiple flags: %v", err)
	}

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID STORE 1 -FLAGS (\Deleted)
	srv.HandleUID(conn, "U022", []string{"UID", "UID", "STORE", "1", "-FLAGS", "(\\Deleted)"}, state)

	response := conn.GetWrittenData()

	// Should have \Seen but not \Deleted
	if !strings.Contains(response, "\\Seen") {
		t.Errorf("Expected \\Seen flag to remain, got: %s", response)
	}
	if strings.Contains(response, "\\Deleted") {
		t.Errorf("Expected \\Deleted flag to be removed, got: %s", response)
	}
	if !strings.Contains(response, "U022 OK UID STORE completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDCopy_MissingDestination tests UID COPY without destination
func TestUIDCopy_MissingDestination(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID COPY without destination
	srv.HandleUID(conn, "U023", []string{"UID", "UID", "COPY"}, state)

	response := conn.GetWrittenData()
	if !strings.Contains(response, "U023 BAD UID COPY requires UID sequence and destination mailbox") {
		t.Errorf("Expected BAD response, got: %s", response)
	}
}

// TestUIDCopy_QuotedDestination tests UID COPY with quoted mailbox name
func TestUIDCopy_QuotedDestination(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.CreateMailbox(t, database, "uiduser", "Sent Items")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID COPY 1 "Sent Items"
	srv.HandleUID(conn, "U024", []string{"UID", "UID", "COPY", "1", "\"Sent Items\""}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "U024 OK UID COPY completed") {
		t.Errorf("Expected OK response for quoted mailbox name, got: %s", response)
	}
}

// TestUIDCopy_PreservesFlags tests that UID COPY preserves flags
func TestUIDCopy_PreservesFlags(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.CreateMailbox(t, database, "uiduser", "Archive")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")
	archiveID, _ := server.GetMailboxID(t, database, userID, "Archive")
	userDB := server.GetUserDBByID(t, database, userID)

	// Set flags on the message
	if _, err := userDB.Exec("UPDATE message_mailbox SET flags = '\\Seen \\Flagged' WHERE mailbox_id = ?", inboxID); err != nil {
		t.Fatalf("Failed to set flags: %v", err)
	}

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID COPY 1 Archive
	srv.HandleUID(conn, "U025", []string{"UID", "UID", "COPY", "1", "Archive"}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "U025 OK UID COPY completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}

	// Check that flags are preserved and \Recent is added
	var flags string
	if err := userDB.QueryRow("SELECT flags FROM message_mailbox WHERE mailbox_id = ?", archiveID).Scan(&flags); err != nil {
		t.Fatalf("Failed to query flags from archive mailbox: %v", err)
	}
	if !strings.Contains(flags, "\\Seen") || !strings.Contains(flags, "\\Flagged") {
		t.Errorf("Expected flags to be preserved, got: %s", flags)
	}
	if !strings.Contains(flags, "\\Recent") {
		t.Errorf("Expected \\Recent flag to be added, got: %s", flags)
	}
}

// TestUIDSearch_DefaultBehavior tests UID SEARCH with unrecognized criteria.
func TestUIDSearch_TEXTMultiWord(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Subject One", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Subject Two", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		Username:          "uiduser",
		SelectedMailboxID: inboxID,
	}

	srv.HandleUID(conn, "U026", []string{"UID", "UID", "SEARCH", "TEXT", "Subject One"}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "* SEARCH 1") {
		t.Errorf("Expected UID SEARCH TEXT to find UID 1, got: %s", response)
	}
	if strings.Contains(response, "* SEARCH 2") {
		t.Errorf("Did not expect UID SEARCH TEXT to match UID 2, got: %s", response)
	}
	if !strings.Contains(response, "U026 OK UID SEARCH completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDSearch_DefaultBehavior tests UID SEARCH with unrecognized criteria.
func TestUIDSearch_DefaultBehavior(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "uiduser")
	server.InsertTestMail(t, database, "uiduser", "Message 1", "sender@test.com", "uiduser@localhost", "INBOX")
	server.InsertTestMail(t, database, "uiduser", "Message 2", "sender@test.com", "uiduser@localhost", "INBOX")

	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	// UID SEARCH UNRECOGNIZED should not match all messages.
	srv.HandleUID(conn, "U027", []string{"UID", "UID", "SEARCH", "UNRECOGNIZED"}, state)

	response := conn.GetWrittenData()

	if !strings.Contains(response, "* SEARCH\r\n") {
		t.Errorf("Expected empty SEARCH response, got: %s", response)
	}
	if strings.Contains(response, "* SEARCH 1") || strings.Contains(response, "* SEARCH 2") {
		t.Errorf("Unexpected UID match for unrecognized criteria, got: %s", response)
	}
	if !strings.Contains(response, "U027 OK UID SEARCH completed") {
		t.Errorf("Expected OK response, got: %s", response)
	}
}

// TestUIDFetch_BodySectionOrdering tests that each BODY[section]'s literal
// appears immediately after its own item name, not batched after all item
// names (regression test for issue #298).
func TestUIDFetch_BodySectionOrdering(t *testing.T) {
	srv := server.SetupTestServerSimple(t)
	conn := server.NewMockConn()
	database := server.GetDatabaseFromServer(srv)

	userID := server.CreateTestUser(t, database, "orderuser")
	server.InsertTestMail(t, database, "orderuser", "Test Subject", "sender@test.com", "orderuser@localhost", "INBOX")
	inboxID, _ := server.GetMailboxID(t, database, userID, "INBOX")

	state := &models.ClientState{
		Authenticated:     true,
		UserID:            userID,
		SelectedMailboxID: inboxID,
	}

	srv.HandleUID(conn, "A001", []string{"UID", "UID", "FETCH", "1", "(BODY.PEEK[1] BODY.PEEK[1.MIME])"}, state)
	response := conn.GetWrittenData()

	body := "Test message body" // InsertTestMail's fixed raw body text
	wantOrder := fmt.Sprintf("BODY[1] {%d}\r\n%s BODY[1.MIME] {", len(body), body)
	if !strings.Contains(response, wantOrder) {
		t.Errorf("expected BODY[1]'s literal immediately after its own name, before BODY[1.MIME]; got: %s", response)
	}
	if strings.Contains(response, "BODY[1] BODY[1.MIME] {") {
		t.Errorf("names batched before literals (old bug), got: %s", response)
	}
}
