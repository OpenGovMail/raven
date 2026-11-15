package selection

import (
	"database/sql"
	"fmt"
	"net"
	"strings"

	"raven/internal/blobstorage"
	"raven/internal/db"
	"raven/internal/models"
)

// ServerDeps defines the dependencies that selection handlers need from the server
type ServerDeps interface {
	SendResponse(conn net.Conn, response string)
	GetUserDB(email string) (*sql.DB, error)
	GetS3Storage() *blobstorage.S3BlobStorage
}

func resolveStateEmail(state *models.ClientState) string {
	if state.Email != "" {
		return state.Email
	}
	if state.Username == "" {
		return ""
	}
	if strings.Contains(state.Username, "@") {
		return state.Username
	}
	return state.Username + "@localhost"
}

// ===== SELECT / EXAMINE =====

func HandleSelect(deps ServerDeps, conn net.Conn, tag string, parts []string, state *models.ClientState) {
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	if len(parts) < 3 {
		cmd := strings.ToUpper(parts[1])
		deps.SendResponse(conn, fmt.Sprintf("%s BAD %s requires folder name", tag, cmd))
		return
	}

	folder := strings.Trim(parts[2], "\"")
	state.SelectedFolder = folder
	stateEmail := resolveStateEmail(state)

	var targetDB *sql.DB

	// RFC 3501: INBOX is case-insensitive - normalize all variants to "INBOX"
	normalizeInbox := func(name string) string {
		if strings.EqualFold(name, "INBOX") {
			return "INBOX"
		}
		return name
	}

	// Regular user mailbox
	var err error
	targetDB, err = deps.GetUserDB(stateEmail)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	// Get mailbox ID using new schema
	mailboxID, err := db.GetMailboxByNamePerUser(targetDB, normalizeInbox(folder))
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO [TRYCREATE] Mailbox does not exist", tag))
		return
	}

	state.SelectedMailboxID = mailboxID

	// Get mailbox info (UID validity and next UID)
	uidValidity, uidNext, err := db.GetMailboxInfoPerUser(targetDB, mailboxID)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Server error: cannot get mailbox info", tag))
		return
	}

	state.UIDValidity = uidValidity
	state.UIDNext = uidNext

	// Get message count using new schema
	count, err := db.GetMessageCountPerUser(targetDB, mailboxID)
	if err != nil {
		count = 0
	}

	// Get recent count using the actual \Recent flag, not unseen messages.
	recent, err := db.GetRecentCountPerUser(targetDB, mailboxID)
	if err != nil {
		recent = 0
	}

	// Get the first unseen message sequence number (RFC 3501 requirement)
	var unseenSeqNum int
	query := `
		SELECT seq_num FROM (
			SELECT ROW_NUMBER() OVER (ORDER BY uid ASC) as seq_num, flags
			FROM message_mailbox
			WHERE mailbox_id = ?
		) WHERE flags IS NULL OR flags NOT LIKE '%\Seen%'
		ORDER BY seq_num ASC
		LIMIT 1
	`
	err = targetDB.QueryRow(query, mailboxID).Scan(&unseenSeqNum)
	hasUnseen := (err == nil && unseenSeqNum > 0)

	// Initialize state tracking for NOOP and other commands
	state.LastMessageCount = count
	state.LastRecentCount = recent

	// Determine if this is SELECT or EXAMINE
	cmd := strings.ToUpper(parts[1])
	isExamine := (cmd == "EXAMINE")

	// Send REQUIRED untagged responses in the correct order per RFC 3501
	// For SELECT: FLAGS, EXISTS, RECENT
	// For EXAMINE: EXISTS, RECENT, then FLAGS (per RFC 3501 example)
	if !isExamine {
		deps.SendResponse(conn, "* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)")
	}
	deps.SendResponse(conn, fmt.Sprintf("* %d EXISTS", count))
	deps.SendResponse(conn, fmt.Sprintf("* %d RECENT", recent))

	// Send REQUIRED OK untagged responses
	if hasUnseen {
		deps.SendResponse(conn, fmt.Sprintf("* OK [UNSEEN %d] Message %d is first unseen", unseenSeqNum, unseenSeqNum))
	}
	deps.SendResponse(conn, fmt.Sprintf("* OK [UIDVALIDITY %d] UIDs valid", uidValidity))
	deps.SendResponse(conn, fmt.Sprintf("* OK [UIDNEXT %d] Predicted next UID", uidNext))

	// FLAGS for EXAMINE comes after OK untagged responses
	if isExamine {
		deps.SendResponse(conn, "* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)")
	}

	// PERMANENTFLAGS: Empty for EXAMINE (read-only), full for SELECT
	if isExamine {
		deps.SendResponse(conn, "* OK [PERMANENTFLAGS ()] No permanent flags permitted")
	} else {
		deps.SendResponse(conn, "* OK [PERMANENTFLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft \\*)] Limited")
	}

	// Send tagged completion response
	if cmd == "SELECT" {
		deps.SendResponse(conn, fmt.Sprintf("%s OK [READ-WRITE] SELECT completed", tag))
	} else {
		deps.SendResponse(conn, fmt.Sprintf("%s OK [READ-ONLY] EXAMINE completed", tag))
	}
}

// ===== CLOSE =====

func HandleClose(deps ServerDeps, conn net.Conn, tag string, state *models.ClientState) {
	// CLOSE command requires authentication
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	// CLOSE command requires a selected mailbox (Selected state)
	// Per RFC 3501: CLOSE is only valid in Selected state
	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No mailbox selected", tag))
		return
	}

	// Per RFC 3501: CLOSE permanently removes all messages with \Deleted flag
	// from the currently selected mailbox, and returns to authenticated state
	// No untagged EXPUNGE responses are sent (unlike EXPUNGE command)

	// Important: Per RFC 3501, if mailbox is read-only (selected with EXAMINE),
	// no messages are removed and no error is given.
	// Since we don't currently track read-only state in ClientState,
	// we always perform the expunge operation.
	// TODO: Add ReadOnly field to ClientState to properly handle EXAMINE

	// Get user database
	userDB, err := deps.GetUserDB(resolveStateEmail(state))
	if err != nil {
		// Clear selection and return
		state.SelectedMailboxID = 0
		state.SelectedFolder = ""
		deps.SendResponse(conn, fmt.Sprintf("%s OK CLOSE completed", tag))
		return
	}

	// Delete all messages with \Deleted flag from the mailbox
	// Query for all messages with \Deleted flag in the current mailbox
	rows, err := userDB.Query(`
		SELECT id FROM message_mailbox
		WHERE mailbox_id = ? AND flags LIKE '%\Deleted%'
	`, state.SelectedMailboxID)

	if err == nil {
		defer func() { _ = rows.Close() }()

		// Collect all message_mailbox IDs to delete
		var idsToDelete []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err == nil {
				idsToDelete = append(idsToDelete, id)
			}
		}

		// Delete the messages from message_mailbox table
		// This removes them from the mailbox but keeps the message data
		for _, id := range idsToDelete {
			_, _ = userDB.Exec(`DELETE FROM message_mailbox WHERE id = ?`, id)
		}
	}

	// Return to authenticated state by clearing the selected mailbox
	state.SelectedFolder = ""
	state.SelectedMailboxID = 0
	state.LastMessageCount = 0
	state.LastRecentCount = 0
	state.UIDValidity = 0
	state.UIDNext = 0

	// Always complete successfully per RFC 3501
	deps.SendResponse(conn, fmt.Sprintf("%s OK CLOSE completed", tag))
}

// ===== UNSELECT =====

func HandleUnselect(deps ServerDeps, conn net.Conn, tag string, state *models.ClientState) {
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No folder selected", tag))
		return
	}

	// Close mailbox without expunging messages
	state.SelectedFolder = ""
	state.SelectedMailboxID = 0
	// Reset state tracking
	state.LastMessageCount = 0
	state.LastRecentCount = 0
	state.UIDValidity = 0
	state.UIDNext = 0
	deps.SendResponse(conn, fmt.Sprintf("%s OK UNSELECT completed", tag))
}
