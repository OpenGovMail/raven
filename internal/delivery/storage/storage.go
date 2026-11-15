package storage

import (
	"fmt"
	"log"
	"strings"
	"time"

	"raven/internal/blobstorage"
	"raven/internal/db"
	"raven/internal/delivery/parser"
)

func isValidRecipient(recipient string) bool {
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return false
	}
	if strings.Count(recipient, "@") != 1 {
		return false
	}
	parts := strings.SplitN(recipient, "@", 2)
	if parts[0] == "" || parts[1] == "" {
		return false
	}
	if strings.ContainsAny(recipient, " \t\r\n") {
		return false
	}
	return true
}

// isSpamByHeaders checks if a message should be classified as spam based on Rspamd headers
func isSpamByHeaders(headers map[string]string) bool {
	// Primary check: X-Rspamd-Action header
	if action, ok := headers["X-Rspamd-Action"]; ok {
		actionLower := strings.ToLower(strings.TrimSpace(action))
		// Treat these actions as spam
		if actionLower == "reject" || actionLower == "rewrite subject" || actionLower == "add header" {
			return true
		}
	}

	// Secondary check: X-Spam-Status header
	if status, ok := headers["X-Spam-Status"]; ok {
		statusLower := strings.ToLower(strings.TrimSpace(status))
		// Check if status starts with "yes" (case-insensitive)
		if strings.HasPrefix(statusLower, "yes") {
			return true
		}
	}

	// Not spam based on Rspamd headers
	return false
}

// determineTargetFolder determines which folder to deliver the message to based on spam headers
func determineTargetFolder(headers map[string]string, defaultFolder string) string {
	if isSpamByHeaders(headers) {
		return "Spam"
	}
	return defaultFolder
}

// Storage handles message storage operations
type Storage struct {
	dbManager *db.DBManager
	s3Storage *blobstorage.S3BlobStorage
}

// NewStorage creates a new storage handler
func NewStorage(dbManager *db.DBManager) *Storage {
	return &Storage{
		dbManager: dbManager,
		s3Storage: nil,
	}
}

// NewStorageWithS3 creates a new storage handler with S3 blob storage
func NewStorageWithS3(dbManager *db.DBManager, s3Storage *blobstorage.S3BlobStorage) *Storage {
	return &Storage{
		dbManager: dbManager,
		s3Storage: s3Storage,
	}
}

// DeliverMessage stores a message for a recipient
func (s *Storage) DeliverMessage(recipient string, msg *parser.Message, folder string) error {
	if !isValidRecipient(recipient) {
		return fmt.Errorf("invalid recipient: %q", recipient)
	}

	// Determine target folder based on spam detection
	targetFolder := determineTargetFolder(msg.Headers, folder)
	if targetFolder == "Spam" && folder != "Spam" {
		log.Printf("Message classified as spam, routing to Spam folder (X-Rspamd-Action: %s, X-Spam-Status: %s)",
			msg.Headers["X-Rspamd-Action"], msg.Headers["X-Spam-Status"])
	}

	sharedDB := s.dbManager.GetSharedDB()

	// Deliver to the recipient's mailbox database (identified by email from IDP)
	targetDB, err := s.dbManager.GetUserDB(recipient)
	if err != nil {
		return fmt.Errorf("failed to get user database: %w", err)
	}

	// Get or create the target mailbox in the per-user database
	mailboxID, err := db.GetMailboxByNamePerUser(targetDB, targetFolder)
	if err != nil {
		// Mailbox doesn't exist, create it
		// Determine special use flag for the mailbox
		specialUse := ""
		if targetFolder == "Spam" {
			specialUse = "\\Junk"
		}
		mailboxID, err = db.CreateMailboxPerUser(targetDB, targetFolder, specialUse)
		if err != nil {
			return fmt.Errorf("failed to create mailbox: %w", err)
		}
	}

	// Parse the message into MIME structure
	parsed, err := parser.ParseMIMEMessage(msg.RawMessage)
	if err != nil {
		return fmt.Errorf("failed to parse message: %w", err)
	}

	// Store the message in the target database (user or role mailbox) with S3 support and shared blob deduplication
	// sharedDB is already obtained earlier for domain/user operations
	messageID, err := parser.StoreMessagePerUserWithSharedDBAndS3(sharedDB, targetDB, parsed, s.s3Storage)
	if err != nil {
		return fmt.Errorf("failed to store message: %w", err)
	}

	// Add the message to the mailbox
	internalDate := msg.Date
	if internalDate.IsZero() {
		internalDate = time.Now()
	}

	err = db.AddMessageToMailboxPerUser(targetDB, messageID, mailboxID, "", internalDate)
	if err != nil {
		return fmt.Errorf("failed to add message to mailbox: %w", err)
	}

	// Record delivery
	err = db.RecordDeliveryPerUser(targetDB, messageID, recipient, msg.From, "delivered", "250 OK")
	if err != nil {
		// Log but don't fail - delivery tracking is not critical
		fmt.Printf("Warning: failed to record delivery: %v\n", err)
	}

	return nil
}

// DeliverToMultipleRecipients delivers a message to multiple recipients
func (s *Storage) DeliverToMultipleRecipients(recipients []string, msg *parser.Message, folder string) map[string]error {
	results := make(map[string]error)

	for _, recipient := range recipients {
		err := s.DeliverMessage(recipient, msg, folder)
		if err != nil {
			results[recipient] = err
		} else {
			results[recipient] = nil
		}
	}

	return results
}

// CheckUserExists checks if a user exists in the system.
// Since user identity is managed by the IDP, we always return true here.
// Actual validation of credentials is done by the IDP at authentication time.
func (s *Storage) CheckUserExists(username string) (bool, error) {
	return true, nil
}

// GetUserQuota retrieves the current quota usage for a user (by email address)
func (s *Storage) GetUserQuota(email string) (int64, error) {
	userDB, err := s.dbManager.GetUserDB(email)
	if err != nil {
		return 0, fmt.Errorf("failed to get user database: %w", err)
	}

	// Calculate total size of all messages in the per-user DB
	query := `
		SELECT COALESCE(SUM(m.size_bytes), 0)
		FROM messages m
		JOIN message_mailbox mm ON m.id = mm.message_id
		JOIN mailboxes mb ON mm.mailbox_id = mb.id
	`

	var totalSize int64
	err = userDB.QueryRow(query).Scan(&totalSize)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate quota: %w", err)
	}

	return totalSize, nil
}

// CheckQuota checks if a user has enough quota for a message
func (s *Storage) CheckQuota(username string, messageSize int64, quotaLimit int64) error {
	currentUsage, err := s.GetUserQuota(username)
	if err != nil {
		return fmt.Errorf("failed to get quota: %w", err)
	}

	if currentUsage+messageSize > quotaLimit {
		return fmt.Errorf("quota exceeded: current=%d, limit=%d, message=%d",
			currentUsage, quotaLimit, messageSize)
	}

	return nil
}

// GetMessageCount returns the total number of messages for a user (by email address)
func (s *Storage) GetMessageCount(email string) (int, error) {
	userDB, err := s.dbManager.GetUserDB(email)
	if err != nil {
		return 0, fmt.Errorf("failed to get user database: %w", err)
	}

	// Count messages in all mailboxes
	query := `
		SELECT COUNT(DISTINCT mm.message_id)
		FROM message_mailbox mm
		JOIN mailboxes mb ON mm.mailbox_id = mb.id
	`

	var count int
	err = userDB.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetMessageCountInFolder returns the number of messages in a specific folder for a user (by email address)
func (s *Storage) GetMessageCountInFolder(email string, folder string) (int, error) {
	userDB, err := s.dbManager.GetUserDB(email)
	if err != nil {
		return 0, fmt.Errorf("failed to get user database: %w", err)
	}

	// Get mailbox ID
	mailboxID, err := db.GetMailboxByNamePerUser(userDB, folder)
	if err != nil {
		return 0, nil // Mailbox doesn't exist
	}

	// Count messages in the mailbox
	count, err := db.GetMessageCountPerUser(userDB, mailboxID)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// CreateUserIfNotExists initializes a user database if it doesn't exist yet.
// Email should be the full email address (from IDP).
func (s *Storage) CreateUserIfNotExists(email string) error {
	// GetUserDB creates the DB and default mailboxes on first access
	_, err := s.dbManager.GetUserDB(email)
	if err != nil {
		return fmt.Errorf("failed to initialize user database: %w", err)
	}
	return nil
}
