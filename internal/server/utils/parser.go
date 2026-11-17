package utils

import (
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"raven/internal/db"
)

// ParseQuotedString parses a quoted string argument, handling both quoted and unquoted strings
func ParseQuotedString(arg string) string {
	if len(arg) == 0 {
		return ""
	}

	// Handle quoted strings
	if arg[0] == '"' && len(arg) >= 2 && arg[len(arg)-1] == '"' {
		return arg[1 : len(arg)-1]
	}

	// Handle unquoted strings (including empty string represented as "")
	if arg == `""` {
		return ""
	}

	return arg
}

// ParseSequenceSetWithDB parses a sequence set and returns message sequence numbers
func ParseSequenceSetWithDB(sequenceSet string, mailboxID int64, userDB *sql.DB) []int {
	totalMessages, err := db.GetMessageCountPerUser(userDB, mailboxID)
	if err != nil {
		return nil
	}
	return ParseSequenceSet(sequenceSet, totalMessages)
}

// ParseSequenceSet expands a sequence set against a mailbox of totalMessages messages.
func ParseSequenceSet(sequenceSet string, totalMessages int) []int {
	var sequences []int
	if totalMessages == 0 {
		return sequences
	}

	// Handle * (last message)
	sequenceSet = strings.ReplaceAll(sequenceSet, "*", fmt.Sprintf("%d", totalMessages))

	// Split by comma for multiple sequences
	parts := strings.Split(sequenceSet, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)

		// Check if it's a range (e.g., "2:4")
		if strings.Contains(part, ":") {
			rangeParts := strings.Split(part, ":")
			if len(rangeParts) == 2 {
				start, err1 := strconv.Atoi(rangeParts[0])
				end, err2 := strconv.Atoi(rangeParts[1])

				if err1 == nil && err2 == nil && start > 0 && end > 0 {
					if start > end {
						start, end = end, start
					}
					for i := start; i <= end && i <= totalMessages; i++ {
						sequences = append(sequences, i)
					}
				}
			}
		} else {
			// Single message
			num, err := strconv.Atoi(part)
			if err == nil && num > 0 && num <= totalMessages {
				sequences = append(sequences, num)
			}
		}
	}

	// Overlapping sets (1:2,2:3) list a message once, so COPY doesn't copy it twice.
	slices.Sort(sequences)
	return slices.Compact(sequences)
}

// Contains checks if a slice contains a string
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ParseUIDSequenceSetWithDB parses a UID sequence set using a provided database connection
// Handles: single (443), ranges (100:200), star (*), ranges with star (559:*)
func ParseUIDSequenceSetWithDB(sequenceSet string, mailboxID int64, userDB *sql.DB) []int {
	var uids []int

	// Get highest UID in mailbox for * handling
	var maxUID int
	err := userDB.QueryRow(`
		SELECT COALESCE(MAX(uid), 0)
		FROM message_mailbox
		WHERE mailbox_id = ?
	`, mailboxID).Scan(&maxUID)

	if err != nil || maxUID == 0 {
		return uids
	}

	// Split by comma for multiple sequences
	parts := strings.Split(sequenceSet, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)

		if part == "*" {
			// * means highest UID
			uids = append(uids, maxUID)
		} else if strings.Contains(part, ":") {
			// Range
			rangeParts := strings.Split(part, ":")
			if len(rangeParts) != 2 {
				continue
			}

			start := 0
			end := 0

			if rangeParts[0] == "*" {
				start = maxUID
			} else {
				start, _ = strconv.Atoi(rangeParts[0])
			}

			if rangeParts[1] == "*" {
				end = maxUID
			} else {
				end, _ = strconv.Atoi(rangeParts[1])
			}

			// Ensure start <= end (RFC 3501: contents of range independent of order)
			if start > end {
				start, end = end, start
			}

			// Get all UIDs in range
			rows, err := userDB.Query(`
				SELECT uid
				FROM message_mailbox
				WHERE mailbox_id = ? AND uid >= ? AND uid <= ?
				ORDER BY uid
			`, mailboxID, start, end)

			if err != nil {
				continue
			}

			for rows.Next() {
				var uid int
				_ = rows.Scan(&uid)
				uids = append(uids, uid)
			}
			_ = rows.Close()
		} else {
			// Single UID
			uid, err := strconv.Atoi(part)
			if err != nil {
				continue
			}

			// Check if UID exists
			var count int
			_ = userDB.QueryRow(`
				SELECT COUNT(*)
				FROM message_mailbox
				WHERE mailbox_id = ? AND uid = ?
			`, mailboxID, uid).Scan(&count)

			if count > 0 {
				uids = append(uids, uid)
			}
		}
	}

	return uids
}
