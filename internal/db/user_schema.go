package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func normalizeMailboxName(name string) string {
	if strings.EqualFold(name, "INBOX") {
		return "INBOX"
	}
	return name
}

// Per-user table creation functions
// Per-user databases are scoped by email, so they do not store user IDs.

func createMailboxesTablePerUser(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS mailboxes (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		parent_id INTEGER,
		uid_validity INTEGER NOT NULL,
		uid_next INTEGER NOT NULL,
		special_use TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (parent_id) REFERENCES mailboxes(id),
		UNIQUE(name)
	);
	`
	_, err := db.Exec(schema)
	return err
}

func createAliasesTablePerUser(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS aliases (
		id INTEGER PRIMARY KEY,
		alias TEXT NOT NULL,
		destination_email TEXT NOT NULL,
		enabled BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(alias)
	);
	`
	_, err := db.Exec(schema)
	return err
}

func createSubscriptionsTablePerUser(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS subscriptions (
		id INTEGER PRIMARY KEY,
		mailbox_name TEXT NOT NULL,
		subscribed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(mailbox_name)
	);
	`
	_, err := db.Exec(schema)
	return err
}

func createDeliveriesTablePerUser(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS deliveries (
		id INTEGER PRIMARY KEY,
		message_id INTEGER NOT NULL,
		recipient TEXT NOT NULL,
		sender TEXT NOT NULL,
		status TEXT NOT NULL,
		delivered_at TIMESTAMP,
		smtp_response TEXT,
		FOREIGN KEY (message_id) REFERENCES messages(id)
	);
	`
	_, err := db.Exec(schema)
	return err
}

func createOutboundQueueTablePerUser(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS outbound_queue (
		id INTEGER PRIMARY KEY,
		message_id INTEGER NOT NULL,
		sender TEXT NOT NULL,
		recipient TEXT NOT NULL,
		retry_count INTEGER DEFAULT 0,
		max_retries INTEGER DEFAULT 5,
		next_retry_at TIMESTAMP,
		status TEXT NOT NULL,
		last_error TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		sent_at TIMESTAMP,
		FOREIGN KEY (message_id) REFERENCES messages(id)
	);
	`
	_, err := db.Exec(schema)
	return err
}

// Mailbox management functions for per-user databases

func CreateMailboxPerUser(db *sql.DB, name string, specialUse string) (int64, error) {
	name = normalizeMailboxName(name)

	// Validate mailbox name
	if name == "" {
		return 0, fmt.Errorf("mailbox name cannot be empty")
	}

	// Generate UID validity (Unix timestamp)
	uidValidity := time.Now().Unix()

	// Insert mailbox record
	result, err := db.Exec(`
		INSERT INTO mailboxes (name, uid_validity, uid_next, special_use)
		VALUES (?, ?, ?, ?)
	`, name, uidValidity, 1, specialUse)

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("mailbox already exists")
		}
		return 0, err
	}

	return result.LastInsertId()
}

func GetMailboxByNamePerUser(db *sql.DB, name string) (int64, error) {
	name = normalizeMailboxName(name)

	var id int64
	err := db.QueryRow("SELECT id FROM mailboxes WHERE name = ?", name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("mailbox not found")
	}
	return id, err
}

func GetMailboxInfoPerUser(db *sql.DB, mailboxID int64) (uidValidity, uidNext int64, err error) {
	err = db.QueryRow("SELECT uid_validity, uid_next FROM mailboxes WHERE id = ?", mailboxID).Scan(&uidValidity, &uidNext)
	return
}

func IncrementUIDNextPerUser(db *sql.DB, mailboxID int64) (int64, error) {
	var currentUID int64
	err := db.QueryRow("SELECT uid_next FROM mailboxes WHERE id = ?", mailboxID).Scan(&currentUID)
	if err != nil {
		return 0, err
	}

	newUID := currentUID
	_, err = db.Exec("UPDATE mailboxes SET uid_next = uid_next + 1 WHERE id = ?", mailboxID)
	return newUID, err
}

func MailboxExistsPerUser(db *sql.DB, mailboxName string) (bool, error) {
	mailboxName = normalizeMailboxName(mailboxName)

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE name = ?", mailboxName).Scan(&count)
	return count > 0, err
}

func GetUserMailboxesPerUser(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM mailboxes ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var mailboxes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			mailboxes = append(mailboxes, name)
		}
	}

	return mailboxes, rows.Err()
}

func DeleteMailboxPerUser(db *sql.DB, mailboxName string) error {
	// Cannot delete INBOX
	if strings.ToUpper(mailboxName) == "INBOX" {
		return fmt.Errorf("cannot delete INBOX")
	}

	mailboxID, err := GetMailboxByNamePerUser(db, mailboxName)
	if err != nil {
		return fmt.Errorf("mailbox does not exist")
	}

	// Check for child mailboxes (both by parent_id and by hierarchical naming)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE parent_id = ?", mailboxID).Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("mailbox has inferior hierarchical names")
	}

	// Also check for hierarchical children by naming convention (mailboxName/*)
	hierarchyPattern := mailboxName + "/%"
	err = db.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE name LIKE ?", hierarchyPattern).Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("mailbox has inferior hierarchical names")
	}

	// Prevent deletion of default mailboxes (except via special operations)
	defaultMailboxes := []string{"Sent", "Drafts", "Trash"}
	for _, defaultMbx := range defaultMailboxes {
		if strings.EqualFold(mailboxName, defaultMbx) {
			return fmt.Errorf("cannot delete default mailbox %s", mailboxName)
		}
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Delete message_mailbox entries
	_, err = tx.Exec("DELETE FROM message_mailbox WHERE mailbox_id = ?", mailboxID)
	if err != nil {
		return err
	}

	// Delete mailbox
	_, err = tx.Exec("DELETE FROM mailboxes WHERE id = ?", mailboxID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func RenameMailboxPerUser(db *sql.DB, oldName, newName string) error {
	// Cannot rename TO INBOX
	if strings.ToUpper(newName) == "INBOX" {
		return fmt.Errorf("cannot rename to INBOX")
	}

	// Handle INBOX renaming (special case)
	if strings.ToUpper(oldName) == "INBOX" {
		return renameInboxPerUser(db, newName)
	}

	// Check if source mailbox exists
	mailboxID, err := GetMailboxByNamePerUser(db, oldName)
	if err != nil {
		return fmt.Errorf("source mailbox does not exist")
	}

	// Check if destination mailbox already exists
	exists, err := MailboxExistsPerUser(db, newName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("destination mailbox already exists")
	}

	// Create intermediate hierarchies if needed (RFC 3501 requirement)
	if strings.Contains(newName, "/") {
		parts := strings.Split(newName, "/")
		for i := 0; i < len(parts)-1; i++ {
			parentPath := strings.Join(parts[:i+1], "/")
			exists, err := MailboxExistsPerUser(db, parentPath)
			if err != nil {
				return err
			}
			if !exists {
				_, err = CreateMailboxPerUser(db, parentPath, "")
				if err != nil && !strings.Contains(err.Error(), "already exists") {
					return fmt.Errorf("failed to create parent hierarchy %s: %v", parentPath, err)
				}
			}
		}
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Rename the mailbox
	_, err = tx.Exec("UPDATE mailboxes SET name = ? WHERE id = ?", newName, mailboxID)
	if err != nil {
		return err
	}

	// Rename all hierarchical children
	hierarchyPattern := oldName + "/%"
	rows, err := tx.Query("SELECT id, name FROM mailboxes WHERE name LIKE ?", hierarchyPattern)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type mailboxUpdate struct {
		id      int64
		newName string
	}
	var updates []mailboxUpdate

	for rows.Next() {
		var id int64
		var childName string
		if err := rows.Scan(&id, &childName); err != nil {
			return err
		}
		newChildName := newName + childName[len(oldName):]
		updates = append(updates, mailboxUpdate{id: id, newName: newChildName})
	}
	_ = rows.Close()

	// Apply all updates
	for _, update := range updates {
		_, err = tx.Exec("UPDATE mailboxes SET name = ? WHERE id = ?", update.newName, update.id)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func renameInboxPerUser(db *sql.DB, newName string) error {
	// Check if destination mailbox already exists
	exists, err := MailboxExistsPerUser(db, newName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("destination mailbox already exists")
	}

	// Get INBOX mailbox ID
	inboxID, err := GetMailboxByNamePerUser(db, "INBOX")
	if err != nil {
		return err
	}

	// Create new mailbox
	newMailboxID, err := CreateMailboxPerUser(db, newName, "")
	if err != nil {
		return err
	}

	// Move all messages from INBOX to new mailbox
	_, err = db.Exec(`
		UPDATE message_mailbox
		SET mailbox_id = ?
		WHERE mailbox_id = ?
	`, newMailboxID, inboxID)

	return err
}

// Message management functions for per-user databases

func AddMessageToMailboxPerUser(db *sql.DB, messageID, mailboxID int64, flags string, internalDate time.Time) error {
	// Get next UID for this mailbox
	uid, err := IncrementUIDNextPerUser(db, mailboxID)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
		INSERT INTO message_mailbox (message_id, mailbox_id, uid, flags, internal_date)
		VALUES (?, ?, ?, ?, ?)
	`, messageID, mailboxID, uid, flags, internalDate)
	return err
}

func GetMessagesByMailboxPerUser(db *sql.DB, mailboxID int64) ([]int64, error) {
	rows, err := db.Query(`
		SELECT message_id FROM message_mailbox
		WHERE mailbox_id = ?
		ORDER BY uid ASC
	`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var messageIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			messageIDs = append(messageIDs, id)
		}
	}

	return messageIDs, rows.Err()
}

func GetMessageCountPerUser(db *sql.DB, mailboxID int64) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM message_mailbox WHERE mailbox_id = ?", mailboxID).Scan(&count)
	return count, err
}

func GetUnseenCountPerUser(db *sql.DB, mailboxID int64) (int, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM message_mailbox
		WHERE mailbox_id = ? AND (flags IS NULL OR flags NOT LIKE '%\Seen%')
	`, mailboxID).Scan(&count)
	return count, err
}

func GetRecentCountPerUser(db *sql.DB, mailboxID int64) (int, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM message_mailbox
		WHERE mailbox_id = ? AND flags LIKE '%\Recent%'
	`, mailboxID).Scan(&count)
	return count, err
}

func UpdateMessageFlagsPerUser(db *sql.DB, mailboxID, messageID int64, flags string) error {
	_, err := db.Exec(`
		UPDATE message_mailbox
		SET flags = ?
		WHERE mailbox_id = ? AND message_id = ?
	`, flags, mailboxID, messageID)
	return err
}

func GetMessageFlagsPerUser(db *sql.DB, mailboxID, messageID int64) (string, error) {
	var flags sql.NullString
	err := db.QueryRow(`
		SELECT flags FROM message_mailbox
		WHERE mailbox_id = ? AND message_id = ?
	`, mailboxID, messageID).Scan(&flags)
	if err != nil {
		return "", err
	}
	return flags.String, nil
}

// Subscription management functions for per-user databases

func SubscribeToMailboxPerUser(db *sql.DB, mailboxName string) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO subscriptions (mailbox_name)
		VALUES (?)
	`, mailboxName)
	return err
}

func UnsubscribeFromMailboxPerUser(db *sql.DB, mailboxName string) error {
	result, err := db.Exec(`
		DELETE FROM subscriptions
		WHERE mailbox_name = ?
	`, mailboxName)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("subscription does not exist")
	}

	return nil
}

func GetUserSubscriptionsPerUser(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`
		SELECT mailbox_name
		FROM subscriptions
		ORDER BY mailbox_name
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var subscriptions []string
	for rows.Next() {
		var mailboxName string
		if err := rows.Scan(&mailboxName); err == nil {
			subscriptions = append(subscriptions, mailboxName)
		}
	}

	return subscriptions, rows.Err()
}

func IsMailboxSubscribedPerUser(db *sql.DB, mailboxName string) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM subscriptions
		WHERE mailbox_name = ?
	`, mailboxName).Scan(&count)
	return count > 0, err
}

// Delivery management functions for per-user databases

func RecordDeliveryPerUser(db *sql.DB, messageID int64, recipient, sender, status string, smtpResponse string) error {
	_, err := db.Exec(`
		INSERT INTO deliveries (message_id, recipient, sender, status, delivered_at, smtp_response)
		VALUES (?, ?, ?, ?, ?, ?)
	`, messageID, recipient, sender, status, time.Now(), smtpResponse)
	return err
}

// Outbound queue management functions for per-user databases

func QueueOutboundMessagePerUser(db *sql.DB, messageID int64, sender, recipient string, maxRetries int) error {
	_, err := db.Exec(`
		INSERT INTO outbound_queue (message_id, sender, recipient, max_retries, status, next_retry_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, messageID, sender, recipient, maxRetries, "pending", time.Now())
	return err
}

func GetPendingOutboundMessagesPerUser(db *sql.DB, limit int) ([]map[string]interface{}, error) {
	rows, err := db.Query(`
		SELECT id, message_id, sender, recipient, retry_count, next_retry_at
		FROM outbound_queue
		WHERE status = 'pending' AND next_retry_at <= ?
		ORDER BY next_retry_at
		LIMIT ?
	`, time.Now(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var messages []map[string]interface{}
	for rows.Next() {
		var id, messageID, retryCount int64
		var sender, recipient string
		var nextRetryAt time.Time

		if err := rows.Scan(&id, &messageID, &sender, &recipient, &retryCount, &nextRetryAt); err == nil {
			messages = append(messages, map[string]interface{}{
				"id":            id,
				"message_id":    messageID,
				"sender":        sender,
				"recipient":     recipient,
				"retry_count":   retryCount,
				"next_retry_at": nextRetryAt,
			})
		}
	}

	return messages, rows.Err()
}

func UpdateOutboundStatusPerUser(db *sql.DB, queueID int64, status, lastError string) error {
	_, err := db.Exec(`
		UPDATE outbound_queue
		SET status = ?, last_error = ?, sent_at = ?
		WHERE id = ?
	`, status, lastError, time.Now(), queueID)
	return err
}

func RetryOutboundMessagePerUser(db *sql.DB, queueID int64, nextRetryDelay time.Duration) error {
	_, err := db.Exec(`
		UPDATE outbound_queue
		SET retry_count = retry_count + 1, next_retry_at = ?
		WHERE id = ?
	`, time.Now().Add(nextRetryDelay), queueID)
	return err
}
