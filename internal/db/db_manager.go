package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// DBManager manages database connections for shared and per-user databases
type DBManager struct {
	basePath    string
	sharedDB    *sql.DB
	userDBCache map[string]*sql.DB
	cacheMutex  sync.RWMutex
}

// NewDBManager creates a new database manager
func NewDBManager(basePath string) (*DBManager, error) {
	// Create base directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0750); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %v", err)
	}

	manager := &DBManager{
		basePath:    basePath,
		userDBCache: make(map[string]*sql.DB),
	}

	// Initialize shared database
	if err := manager.initSharedDB(); err != nil {
		return nil, fmt.Errorf("failed to initialize shared database: %v", err)
	}

	return manager, nil
}

// GetSharedDB returns the shared database connection
func (m *DBManager) GetSharedDB() *sql.DB {
	return m.sharedDB
}

// GetUserDB returns a database connection for a specific user identified by email
func (m *DBManager) GetUserDB(email string) (*sql.DB, error) {
	email = mailboxKey(email)

	// Check cache first
	m.cacheMutex.RLock()
	if db, exists := m.userDBCache[email]; exists {
		m.cacheMutex.RUnlock()
		return db, nil
	}
	m.cacheMutex.RUnlock()

	// Create or open user database
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()

	// Double-check after acquiring write lock
	if db, exists := m.userDBCache[email]; exists {
		return db, nil
	}

	dbPath := m.getUserDBPath(email)

	// Check if database file exists
	exists := false
	if _, err := os.Stat(dbPath); err == nil {
		exists = true
	}

	// Open database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open user database: %v", err)
	}

	// Enable foreign key constraints
	if _, err = db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %v", err)
	}

	// Initialize schema if this is a new database
	if !exists {
		if err := m.initUserDB(db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to initialize user database: %v", err)
		}
	}

	// Cache the connection
	m.userDBCache[email] = db

	return db, nil
}

// initSharedDB initializes the shared database
func (m *DBManager) initSharedDB() error {
	sharedPath := filepath.Join(m.basePath, "shared.db")

	db, err := sql.Open("sqlite3", sharedPath)
	if err != nil {
		return err
	}

	// Enable foreign key constraints
	if _, err = db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return err
	}

	// Create blobs table in shared database for deduplication across all users
	if err := createBlobsTable(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to create blobs table: %v", err)
	}

	// Create shared database indexes
	if err := createSharedIndexes(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to create shared indexes: %v", err)
	}

	m.sharedDB = db
	return nil
}

// initUserDB initializes a per-user database
func (m *DBManager) initUserDB(db *sql.DB) error {
	// Create user tables
	// Note: blobs table is now in shared database for cross-user deduplication

	if err := createMailboxesTablePerUser(db); err != nil {
		return fmt.Errorf("failed to create mailboxes table: %v", err)
	}

	if err := createAliasesTablePerUser(db); err != nil {
		return fmt.Errorf("failed to create aliases table: %v", err)
	}

	if err := createMessagesTable(db); err != nil {
		return fmt.Errorf("failed to create messages table: %v", err)
	}

	if err := createSubscriptionsTablePerUser(db); err != nil {
		return fmt.Errorf("failed to create subscriptions table: %v", err)
	}

	if err := createAddressesTable(db); err != nil {
		return fmt.Errorf("failed to create addresses table: %v", err)
	}

	if err := createMessagePartsTablePerUser(db); err != nil {
		return fmt.Errorf("failed to create message_parts table: %v", err)
	}

	if err := createDeliveriesTablePerUser(db); err != nil {
		return fmt.Errorf("failed to create deliveries table: %v", err)
	}

	if err := createMessageMailboxTable(db); err != nil {
		return fmt.Errorf("failed to create message_mailbox table: %v", err)
	}

	if err := createMessageHeadersTable(db); err != nil {
		return fmt.Errorf("failed to create message_headers table: %v", err)
	}

	if err := createOutboundQueueTablePerUser(db); err != nil {
		return fmt.Errorf("failed to create outbound_queue table: %v", err)
	}

	// Create user database indexes
	if err := createUserIndexes(db); err != nil {
		return fmt.Errorf("failed to create user indexes: %v", err)
	}

	// Create default mailboxes for this per-user database.
	if err := createDefaultMailboxes(db); err != nil {
		return fmt.Errorf("failed to create default mailboxes: %v", err)
	}

	return nil
}

// mailboxKey lowercases the domain, which is case-insensitive, so login and delivery open the
// same mailbox whatever the spelling. The local part is kept: Thunder usernames are
// case-sensitive.
func mailboxKey(email string) string {
	i := strings.LastIndex(email, "@")
	if i < 0 {
		return email
	}
	return email[:i] + strings.ToLower(email[i:])
}

// getUserDBPath returns the file path for a user's database
func (m *DBManager) getUserDBPath(email string) string {
	if strings.HasSuffix(email, ".db") {
		// Preserve explicit mailbox identity paths (for example role_<name>@<domain>.db).
		return filepath.Join(m.basePath, email)
	}

	return filepath.Join(m.basePath, fmt.Sprintf("user_%s.db", email))
}

// Close closes all database connections
func (m *DBManager) Close() error {
	var lastErr error

	// Close shared database
	if m.sharedDB != nil {
		if err := m.sharedDB.Close(); err != nil {
			lastErr = err
		}
	}

	// Close all user databases
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()

	for email, db := range m.userDBCache {
		if err := db.Close(); err != nil {
			lastErr = err
		}
		delete(m.userDBCache, email)
	}

	return lastErr
}
