package db

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func testEmailFromID(id int64) string {
	return fmt.Sprintf("user-%d@example.com", id)
}

func TestNewDBManager(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	if manager == nil {
		t.Fatal("Expected non-nil DBManager")
	}

	if manager.sharedDB == nil {
		t.Error("Expected non-nil shared database")
	}

	if manager.basePath != tmpDir {
		t.Errorf("Expected base path %s, got %s", tmpDir, manager.basePath)
	}
}

func TestNewDBManager_InvalidPath(t *testing.T) {
	_, err := NewDBManager("/invalid/nonexistent/path")
	if err == nil {
		t.Error("Expected error for invalid path")
	}
}

func TestGetSharedDB(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	sharedDB := manager.GetSharedDB()
	if sharedDB == nil {
		t.Fatal("Expected non-nil shared database")
	}

	var count int
	err = sharedDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='blobs'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query blobs table: %v", err)
	}

	if count != 1 {
		t.Error("Expected blobs table to exist in shared database")
	}
}

func TestGetUserDB(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	userID := int64(123)
	userDB, err := manager.GetUserDB(testEmailFromID(userID))
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}

	if userDB == nil {
		t.Fatal("Expected non-nil user database")
	}

	dbPath := filepath.Join(tmpDir, fmt.Sprintf("user_%s.db", testEmailFromID(123)))
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Expected user database file to be created")
	}

	var count int
	err = userDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='mailboxes'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query mailboxes table: %v", err)
	}

	if count != 1 {
		t.Error("Expected mailboxes table to exist in user database")
	}
}

func TestGetUserDB_RoleMailboxIdentityPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	identity := "role_testrole@testorg.example.db"
	userDB, err := manager.GetUserDB(identity)
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}
	if userDB == nil {
		t.Fatal("Expected non-nil user database")
	}

	roleDBPath := filepath.Join(tmpDir, identity)
	if _, err := os.Stat(roleDBPath); os.IsNotExist(err) {
		t.Errorf("Expected role database file to be created at %s", roleDBPath)
	}

	legacyPath := filepath.Join(tmpDir, fmt.Sprintf("user_%s.db", identity))
	if _, err := os.Stat(legacyPath); err == nil {
		t.Errorf("Did not expect legacy user-prefixed path to exist: %s", legacyPath)
	}
}

func TestGetUserDB_DomainCaseOpensSameMailbox(t *testing.T) {
	manager, err := NewDBManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	lower, err := manager.GetUserDB("alice@silver.example.lk")
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}
	mixed, err := manager.GetUserDB("alice@Silver.EXAMPLE.lk")
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}
	if lower != mixed {
		t.Error("alice@Silver.EXAMPLE.lk opened a different mailbox from alice@silver.example.lk")
	}
}

func TestGetUserDB_Caching(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	userID := int64(123)
	userDB1, err := manager.GetUserDB(testEmailFromID(userID))
	if err != nil {
		t.Fatalf("First GetUserDB failed: %v", err)
	}

	userDB2, err := manager.GetUserDB(testEmailFromID(userID))
	if err != nil {
		t.Fatalf("Second GetUserDB failed: %v", err)
	}

	if userDB1 != userDB2 {
		t.Error("Expected same database connection from cache")
	}

	if len(manager.userDBCache) != 1 {
		t.Errorf("Expected 1 cached user database, got %d", len(manager.userDBCache))
	}
}

func TestGetUserDB_DefaultMailboxes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	userID := int64(123)
	userDB, err := manager.GetUserDB(testEmailFromID(userID))
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}

	expectedMailboxes := []string{"INBOX", "Sent", "Drafts", "Trash"}
	for _, mailboxName := range expectedMailboxes {
		var count int
		err = userDB.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE name = ?", mailboxName).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query mailbox %s: %v", mailboxName, err)
		}

		if count != 1 {
			t.Errorf("Expected mailbox %s to be created by default", mailboxName)
		}
	}
}

func TestClose(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}

	_, _ = manager.GetUserDB(testEmailFromID(1))
	_, _ = manager.GetUserDB(testEmailFromID(2))

	err = manager.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if len(manager.userDBCache) != 0 {
		t.Errorf("Expected empty user DB cache after close, got %d entries", len(manager.userDBCache))
	}
}

func TestClose_MultipleUserDBs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}

	for i := int64(1); i <= 5; i++ {
		_, err := manager.GetUserDB(testEmailFromID(i))
		if err != nil {
			t.Fatalf("GetUserDB(%d) failed: %v", i, err)
		}
	}

	if len(manager.userDBCache) != 5 {
		t.Errorf("Expected 5 cached user databases, got %d", len(manager.userDBCache))
	}

	err = manager.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if len(manager.userDBCache) != 0 {
		t.Errorf("Expected empty user DB cache after close, got %d entries", len(manager.userDBCache))
	}
}

func TestSharedDBTables(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	sharedDB := manager.GetSharedDB()

	expectedTables := []string{"blobs"}
	for _, tableName := range expectedTables {
		var count int
		err = sharedDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query table %s: %v", tableName, err)
		}

		if count != 1 {
			t.Errorf("Expected table %s to exist in shared database", tableName)
		}
	}
}

func TestUserDBTables(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	userDB, err := manager.GetUserDB(testEmailFromID(1))
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}

	expectedTables := []string{
		"mailboxes", "aliases", "messages",
		"subscriptions", "addresses", "message_parts",
		"deliveries", "message_mailbox", "message_headers",
		"outbound_queue",
	}
	for _, tableName := range expectedTables {
		var count int
		err = userDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query table %s: %v", tableName, err)
		}

		if count != 1 {
			t.Errorf("Expected table %s to exist in user database", tableName)
		}
	}
}

func TestSharedDBIndexes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	sharedDB := manager.GetSharedDB()

	expectedIndexes := []string{
		"idx_blobs_hash",
	}

	for _, indexName := range expectedIndexes {
		var count int
		err = sharedDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query index %s: %v", indexName, err)
		}

		if count != 1 {
			t.Errorf("Expected index %s to exist in shared database", indexName)
		}
	}
}

func TestUserDBIndexes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	userDB, err := manager.GetUserDB(testEmailFromID(1))
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}

	expectedIndexes := []string{
		"idx_mailboxes_parent",
		"idx_messages_date",
		"idx_messages_thread",
		"idx_addresses_message",
		"idx_addresses_email",
		"idx_message_parts_message",
		"idx_message_parts_blob",
		"idx_message_mailbox_mailbox",
		"idx_message_mailbox_message",
		"idx_message_mailbox_uid",
		"idx_message_headers_message",
		"idx_deliveries_message",
		"idx_deliveries_status",
		"idx_outbound_status",
	}

	for _, indexName := range expectedIndexes {
		var count int
		err = userDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query index %s: %v", indexName, err)
		}

		if count != 1 {
			t.Errorf("Expected index %s to exist in user database", indexName)
		}
	}
}

func TestGetUserDB_ExistingDatabase(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager1, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("First NewDBManager failed: %v", err)
	}

	userID := int64(123)
	userDB1, err := manager1.GetUserDB(testEmailFromID(userID))
	if err != nil {
		t.Fatalf("First GetUserDB failed: %v", err)
	}

	_, err = CreateMailboxPerUser(userDB1, "CustomMailbox", "")
	if err != nil {
		t.Fatalf("Failed to create custom mailbox: %v", err)
	}

	_ = manager1.Close()

	manager2, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("Second NewDBManager failed: %v", err)
	}
	defer func() { _ = manager2.Close() }()

	userDB2, err := manager2.GetUserDB(testEmailFromID(userID))
	if err != nil {
		t.Fatalf("Second GetUserDB failed: %v", err)
	}

	var count int
	err = userDB2.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE name = ?", "CustomMailbox").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query custom mailbox: %v", err)
	}

	if count != 1 {
		t.Error("Expected custom mailbox to exist in reopened database")
	}
}

func TestForeignKeyConstraints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "db_manager_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	manager, err := NewDBManager(tmpDir)
	if err != nil {
		t.Fatalf("NewDBManager failed: %v", err)
	}
	defer func() { _ = manager.Close() }()

	sharedDB := manager.GetSharedDB()

	var fkEnabled int
	err = sharedDB.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("Failed to check foreign keys on shared DB: %v", err)
	}

	if fkEnabled != 1 {
		t.Error("Expected foreign keys to be enabled on shared database")
	}

	userDB, err := manager.GetUserDB(testEmailFromID(1))
	if err != nil {
		t.Fatalf("GetUserDB failed: %v", err)
	}

	err = userDB.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("Failed to check foreign keys on user DB: %v", err)
	}

	if fkEnabled != 1 {
		t.Error("Expected foreign keys to be enabled on user database")
	}
}
