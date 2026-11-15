package message

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"raven/internal/blobstorage"
	"raven/internal/db"
	"raven/internal/delivery/parser"
	"raven/internal/models"
	"raven/internal/server/utils"
)

// ServerDeps defines the dependencies that message handlers need from the server
type ServerDeps interface {
	SendResponse(conn net.Conn, response string)
	GetUserDB(email string) (*sql.DB, error)
	GetSelectedDB(state *models.ClientState) (*sql.DB, error)
	GetSharedDB() *sql.DB
	GetDBManager() *db.DBManager
	GetS3Storage() *blobstorage.S3BlobStorage
}

// ===== SEARCH =====

// messageInfo holds metadata about a message for search operations
type messageInfo struct {
	messageID    int64
	uid          int64
	flags        string
	internalDate time.Time
	seqNum       int
}

// SearchMatch describes a mailbox message that matched SEARCH criteria.
type SearchMatch struct {
	UID    int64
	SeqNum int
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

func HandleSearch(deps ServerDeps, conn net.Conn, tag string, parts []string, state *models.ClientState) {
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No folder selected", tag))
		return
	}

	// Get appropriate database (user or role mailbox)
	targetDB, err := deps.GetSelectedDB(state)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	// Parse search criteria
	if len(parts) < 3 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD SEARCH requires search criteria", tag))
		return
	}

	// Check for CHARSET specification
	searchStart := 2
	charset := "US-ASCII"
	if len(parts) > 3 && strings.ToUpper(parts[2]) == "CHARSET" {
		charset = strings.ToUpper(parts[3])
		searchStart = 4

		// RFC 3501: US-ASCII MUST be supported, other charsets MAY be supported
		if charset != "US-ASCII" && charset != "UTF-8" {
			// Return tagged NO with BADCHARSET response code
			deps.SendResponse(conn, fmt.Sprintf("%s NO [BADCHARSET (US-ASCII UTF-8)] Charset not supported", tag))
			return
		}
	}

	if searchStart >= len(parts) {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD SEARCH requires search criteria", tag))
		return
	}

	email := resolveStateEmail(state)
	matches, err := SearchMailboxMatchesTokens(targetDB, state.SelectedMailboxID, parts[searchStart:], charset, email, deps)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Search failed: %v", tag, err))
		return
	}

	// Build response
	if len(matches) > 0 {
		var results []string
		for _, match := range matches {
			results = append(results, strconv.Itoa(match.SeqNum))
		}
		deps.SendResponse(conn, fmt.Sprintf("* SEARCH %s", strings.Join(results, " ")))
	} else {
		deps.SendResponse(conn, "* SEARCH")
	}
	deps.SendResponse(conn, fmt.Sprintf("%s OK SEARCH completed", tag))
}

// SearchMailboxMatches evaluates IMAP SEARCH criteria against a mailbox and
// returns the matching sequence numbers and UIDs from the selected mailbox.
func SearchMailboxMatches(targetDB *sql.DB, mailboxID int64, criteria string, charset string, email string, deps ServerDeps) ([]SearchMatch, error) {
	return SearchMailboxMatchesTokens(targetDB, mailboxID, parseSearchTokens(criteria), charset, email, deps)
}

// SearchMailboxMatchesTokens evaluates already-tokenized IMAP SEARCH criteria.
// This preserves multi-word string arguments that were parsed by the command layer.
func SearchMailboxMatchesTokens(targetDB *sql.DB, mailboxID int64, tokens []string, charset string, email string, deps ServerDeps) ([]SearchMatch, error) {
	query := `
		SELECT mm.message_id, mm.uid, mm.flags, mm.internal_date,
		       ROW_NUMBER() OVER (ORDER BY mm.uid ASC) as seq_num
		FROM message_mailbox mm
		WHERE mm.mailbox_id = ?
		ORDER BY mm.uid ASC
	`
	rows, err := targetDB.Query(query, mailboxID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var messages []messageInfo
	for rows.Next() {
		var msg messageInfo
		var flagsStr sql.NullString
		var internalDate sql.NullTime
		if err := rows.Scan(&msg.messageID, &msg.uid, &flagsStr, &internalDate, &msg.seqNum); err != nil {
			continue
		}
		if flagsStr.Valid {
			msg.flags = flagsStr.String
		}
		if internalDate.Valid {
			msg.internalDate = internalDate.Time
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(tokens) == 0 {
		tokens = []string{"ALL"}
	}

	matches := make([]SearchMatch, 0, len(messages))
	for _, msg := range messages {
		if matchesSearchCriteria(msg, tokens, charset, email, deps) {
			matches = append(matches, SearchMatch{
				UID:    msg.uid,
				SeqNum: msg.seqNum,
			})
		}
	}

	return matches, nil
}

// parseSearchTokens tokenizes search criteria
func parseSearchTokens(criteria string) []string {
	var tokens []string
	var current strings.Builder
	inQuotes := false
	inParens := 0

	for i := 0; i < len(criteria); i++ {
		ch := criteria[i]

		switch ch {
		case '"':
			inQuotes = !inQuotes
			current.WriteByte(ch)
		case '(':
			if !inQuotes {
				inParens++
			}
			current.WriteByte(ch)
		case ')':
			if !inQuotes {
				inParens--
			}
			current.WriteByte(ch)
		case ' ', '\t':
			if inQuotes || inParens > 0 {
				current.WriteByte(ch)
			} else if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// matchesSearchCriteria checks if a message matches the search criteria
func matchesSearchCriteria(msg messageInfo, tokens []string, charset string, email string, deps ServerDeps) bool {
	// Default to ALL - match everything
	if len(tokens) == 0 {
		return true
	}

	// Process tokens (AND logic by default)
	return evaluateTokens(msg, tokens, charset, email, deps)
}

// evaluateTokens evaluates a list of search tokens
func evaluateTokens(msg messageInfo, tokens []string, charset string, email string, deps ServerDeps) bool {
	i := 0
	for i < len(tokens) {
		token := strings.ToUpper(tokens[i])

		// Handle sequence set (numbers and ranges)
		if isSequenceSet(token) {
			if !matchesSequenceSet(msg.seqNum, token) {
				return false
			}
			i++
			continue
		}

		switch token {
		case "ALL":
			// Matches all messages
			i++

		case "ANSWERED":
			if !strings.Contains(msg.flags, "\\Answered") {
				return false
			}
			i++

		case "DELETED":
			if !strings.Contains(msg.flags, "\\Deleted") {
				return false
			}
			i++

		case "DRAFT":
			if !strings.Contains(msg.flags, "\\Draft") {
				return false
			}
			i++

		case "FLAGGED":
			if !strings.Contains(msg.flags, "\\Flagged") {
				return false
			}
			i++

		case "NEW":
			// NEW = RECENT UNSEEN
			if !strings.Contains(msg.flags, "\\Recent") || strings.Contains(msg.flags, "\\Seen") {
				return false
			}
			i++

		case "OLD":
			// OLD = NOT RECENT
			if strings.Contains(msg.flags, "\\Recent") {
				return false
			}
			i++

		case "RECENT":
			if !strings.Contains(msg.flags, "\\Recent") {
				return false
			}
			i++

		case "SEEN":
			if !strings.Contains(msg.flags, "\\Seen") {
				return false
			}
			i++

		case "UNANSWERED":
			if strings.Contains(msg.flags, "\\Answered") {
				return false
			}
			i++

		case "UNDELETED":
			if strings.Contains(msg.flags, "\\Deleted") {
				return false
			}
			i++

		case "UNDRAFT":
			if strings.Contains(msg.flags, "\\Draft") {
				return false
			}
			i++

		case "UNFLAGGED":
			if strings.Contains(msg.flags, "\\Flagged") {
				return false
			}
			i++

		case "UNSEEN":
			if strings.Contains(msg.flags, "\\Seen") {
				return false
			}
			i++

		case "NOT":
			// NOT <search-key>
			if i+1 >= len(tokens) {
				return false
			}
			i++
			// Evaluate next token and negate result
			nextTokens := []string{tokens[i]}
			// Handle NOT with arguments (e.g., NOT FROM "Smith")
			if i+1 < len(tokens) && requiresArgument(strings.ToUpper(tokens[i])) {
				i++
				nextTokens = append(nextTokens, tokens[i])
			}
			if evaluateTokens(msg, nextTokens, charset, email, deps) {
				return false
			}
			i++

		case "OR":
			// OR <search-key1> <search-key2>
			if i+2 >= len(tokens) {
				return false
			}
			i++
			key1Tokens := []string{tokens[i]}
			if i+1 < len(tokens) && requiresArgument(strings.ToUpper(tokens[i])) {
				i++
				key1Tokens = append(key1Tokens, tokens[i])
			}
			i++
			key2Tokens := []string{tokens[i]}
			if i+1 < len(tokens) && requiresArgument(strings.ToUpper(tokens[i])) {
				i++
				key2Tokens = append(key2Tokens, tokens[i])
			}
			if !evaluateTokens(msg, key1Tokens, charset, email, deps) && !evaluateTokens(msg, key2Tokens, charset, email, deps) {
				return false
			}
			i++

		case "BCC", "CC", "FROM", "SUBJECT", "TO", "BODY", "TEXT":
			// These require a string argument
			if i+1 >= len(tokens) {
				return false
			}
			i++
			searchStr := unquote(tokens[i])
			if !matchesHeaderOrBody(msg, token, searchStr, charset, email, deps) {
				return false
			}
			i++

		case "HEADER":
			// HEADER <field-name> <string>
			if i+2 >= len(tokens) {
				return false
			}
			i++
			fieldName := unquote(tokens[i])
			i++
			searchStr := unquote(tokens[i])
			if !matchesHeader(msg, fieldName, searchStr, charset, email, deps) {
				return false
			}
			i++

		case "KEYWORD":
			// KEYWORD <flag>
			if i+1 >= len(tokens) {
				return false
			}
			i++
			keyword := unquote(tokens[i])
			if !strings.Contains(msg.flags, keyword) {
				return false
			}
			i++

		case "UNKEYWORD":
			// UNKEYWORD <flag>
			if i+1 >= len(tokens) {
				return false
			}
			i++
			keyword := unquote(tokens[i])
			if strings.Contains(msg.flags, keyword) {
				return false
			}
			i++

		case "LARGER":
			// LARGER <n>
			if i+1 >= len(tokens) {
				return false
			}
			i++
			size, err := strconv.Atoi(tokens[i])
			if err != nil || !matchesSize(msg, size, true, email, deps) {
				return false
			}
			i++

		case "SMALLER":
			// SMALLER <n>
			if i+1 >= len(tokens) {
				return false
			}
			i++
			size, err := strconv.Atoi(tokens[i])
			if err != nil || !matchesSize(msg, size, false, email, deps) {
				return false
			}
			i++

		case "UID":
			// UID <sequence set>
			if i+1 >= len(tokens) {
				return false
			}
			i++
			if !matchesUIDSet(int(msg.uid), tokens[i]) {
				return false
			}
			i++

		case "BEFORE", "ON", "SINCE":
			// Date-based searches on internal date
			if i+1 >= len(tokens) {
				return false
			}
			i++
			dateStr := unquote(tokens[i])
			if !matchesDate(msg.internalDate, dateStr, token) {
				return false
			}
			i++

		case "SENTBEFORE", "SENTON", "SENTSINCE":
			// Date-based searches on Date: header
			if i+1 >= len(tokens) {
				return false
			}
			i++
			dateStr := unquote(tokens[i])
			if !matchesSentDate(msg, dateStr, token, email, deps) {
				return false
			}
			i++

		default:
			// Unknown search keys must not silently match everything.
			return false
		}
	}

	return true
}

// Helper functions for search criteria evaluation

func isSequenceSet(token string) bool {
	// Check if token looks like a sequence number or range (e.g., "1", "2:4", "1:*", "*")
	if token == "*" {
		return true
	}
	for _, ch := range token {
		if ch != ':' && ch != '*' && (ch < '0' || ch > '9') {
			return false
		}
	}
	return len(token) > 0 && (token[0] >= '0' && token[0] <= '9' || token[0] == '*')
}

func matchesSequenceSet(seqNum int, set string) bool {
	// Handle single number
	if !strings.Contains(set, ":") && set != "*" {
		num, err := strconv.Atoi(set)
		return err == nil && num == seqNum
	}

	// Handle * (highest sequence number) - for now, just return true
	if set == "*" {
		return true
	}

	// Handle range
	parts := strings.Split(set, ":")
	if len(parts) != 2 {
		return false
	}

	start, end := 0, 0
	if parts[0] == "*" {
		start = seqNum // Will match if seqNum is the highest
	} else {
		start, _ = strconv.Atoi(parts[0])
	}

	if parts[1] == "*" {
		end = 999999 // Effectively infinity
	} else {
		end, _ = strconv.Atoi(parts[1])
	}

	return seqNum >= start && seqNum <= end
}

func matchesUIDSet(uid int, set string) bool {
	// Similar to sequence set but for UIDs
	return matchesSequenceSet(uid, set)
}

func matchesHeaderOrBody(msg messageInfo, field string, searchStr string, charset string, email string, deps ServerDeps) bool {
	// Get user database
	userDB, err := deps.GetUserDB(email)
	if err != nil {
		return false
	}

	// Get shared database for blob access
	sharedDB := deps.GetSharedDB()
	s3Storage := deps.GetS3Storage()

	// Reconstruct message to search in headers/body
	rawMsg, err := parser.ReconstructMessageWithSharedDBAndS3(sharedDB, userDB, msg.messageID, s3Storage)
	if err != nil {
		return false
	}

	searchStrUpper := strings.ToUpper(searchStr)

	switch field {
	case "FROM":
		return headerContains(rawMsg, "From", searchStrUpper)
	case "TO":
		return headerContains(rawMsg, "To", searchStrUpper)
	case "CC":
		return headerContains(rawMsg, "Cc", searchStrUpper)
	case "BCC":
		return headerContains(rawMsg, "Bcc", searchStrUpper)
	case "SUBJECT":
		return headerContains(rawMsg, "Subject", searchStrUpper)
	case "BODY":
		// Search only in message body
		headerEnd := strings.Index(rawMsg, "\r\n\r\n")
		if headerEnd == -1 {
			headerEnd = strings.Index(rawMsg, "\n\n")
		}
		if headerEnd != -1 {
			body := rawMsg[headerEnd:]
			return strings.Contains(strings.ToUpper(body), searchStrUpper)
		}
		return false
	case "TEXT":
		// Search in entire message (headers + body)
		return strings.Contains(strings.ToUpper(rawMsg), searchStrUpper)
	}

	return false
}

func matchesHeader(msg messageInfo, fieldName string, searchStr string, charset string, email string, deps ServerDeps) bool {
	// Get user database
	userDB, err := deps.GetUserDB(email)
	if err != nil {
		return false
	}

	// Get shared database for blob access
	sharedDB := deps.GetSharedDB()
	s3Storage := deps.GetS3Storage()

	rawMsg, err := parser.ReconstructMessageWithSharedDBAndS3(sharedDB, userDB, msg.messageID, s3Storage)
	if err != nil {
		return false
	}

	// Special case: empty search string matches any message with that header
	if searchStr == "" {
		return hasHeader(rawMsg, fieldName)
	}

	return headerContains(rawMsg, fieldName, strings.ToUpper(searchStr))
}

func hasHeader(rawMsg string, fieldName string) bool {
	lines := strings.Split(rawMsg, "\n")
	fieldNameUpper := strings.ToUpper(fieldName)

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break // End of headers
		}
		if strings.HasPrefix(strings.ToUpper(line), fieldNameUpper+":") {
			return true
		}
	}
	return false
}

func headerContains(rawMsg string, fieldName string, searchStr string) bool {
	lines := strings.Split(rawMsg, "\n")
	fieldNameUpper := strings.ToUpper(fieldName)
	searchStrUpper := strings.ToUpper(searchStr)

	inTargetHeader := false
	var headerValue strings.Builder

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break // End of headers
		}

		// Check if this is a continuation line (starts with space or tab)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if inTargetHeader {
				headerValue.WriteString(" ")
				headerValue.WriteString(strings.TrimSpace(line))
			}
			continue
		}

		// New header line
		if strings.HasPrefix(strings.ToUpper(line), fieldNameUpper+":") {
			inTargetHeader = true
			colonIdx := strings.Index(line, ":")
			if colonIdx != -1 {
				headerValue.WriteString(strings.TrimSpace(line[colonIdx+1:]))
			}
		} else {
			inTargetHeader = false
		}
	}

	return strings.Contains(strings.ToUpper(headerValue.String()), searchStrUpper)
}

func matchesSize(msg messageInfo, size int, larger bool, email string, deps ServerDeps) bool {
	// Get user database
	userDB, err := deps.GetUserDB(email)
	if err != nil {
		return false
	}

	// Get shared database for blob access
	sharedDB := deps.GetSharedDB()
	s3Storage := deps.GetS3Storage()

	rawMsg, err := parser.ReconstructMessageWithSharedDBAndS3(sharedDB, userDB, msg.messageID, s3Storage)
	if err != nil {
		return false
	}

	msgSize := len(rawMsg)
	if larger {
		return msgSize > size
	}
	return msgSize < size
}

func matchesDate(internalDate time.Time, dateStr string, comparison string) bool {
	// Parse RFC 3501 date format: "1-Feb-1994" or "01-Feb-1994"
	targetDate, err := parseIMAPDate(dateStr)
	if err != nil {
		return false
	}

	// Compare dates (disregarding time and timezone)
	msgDate := time.Date(internalDate.Year(), internalDate.Month(), internalDate.Day(), 0, 0, 0, 0, time.UTC)
	targetDate = time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, time.UTC)

	switch comparison {
	case "BEFORE":
		return msgDate.Before(targetDate)
	case "ON":
		return msgDate.Equal(targetDate)
	case "SINCE":
		return msgDate.Equal(targetDate) || msgDate.After(targetDate)
	}

	return false
}

func matchesSentDate(msg messageInfo, dateStr string, comparison string, email string, deps ServerDeps) bool {
	// Get user database
	userDB, err := deps.GetUserDB(email)
	if err != nil {
		return false
	}

	// Get shared database for blob access
	sharedDB := deps.GetSharedDB()
	s3Storage := deps.GetS3Storage()

	// Get Date: header from message
	rawMsg, err := parser.ReconstructMessageWithSharedDBAndS3(sharedDB, userDB, msg.messageID, s3Storage)
	if err != nil {
		return false
	}

	lines := strings.Split(rawMsg, "\n")
	var dateHeader string

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToUpper(line), "DATE:") {
			colonIdx := strings.Index(line, ":")
			if colonIdx != -1 {
				dateHeader = strings.TrimSpace(line[colonIdx+1:])
			}
			break
		}
	}

	if dateHeader == "" {
		return false
	}

	// Parse the Date: header (RFC 2822 format)
	sentDate, err := time.Parse(time.RFC1123Z, dateHeader)
	if err != nil {
		// Try RFC1123
		sentDate, err = time.Parse(time.RFC1123, dateHeader)
		if err != nil {
			return false
		}
	}

	// Use the date matching logic
	comparisonType := strings.TrimPrefix(comparison, "SENT")
	return matchesDate(sentDate, dateStr, comparisonType)
}

func parseIMAPDate(dateStr string) (time.Time, error) {
	// RFC 3501 date format: "1-Feb-1994" or "01-Feb-1994"
	// Try both formats
	t, err := time.Parse("2-Jan-2006", dateStr)
	if err != nil {
		t, err = time.Parse("02-Jan-2006", dateStr)
	}
	return t, err
}

func unquote(str string) string {
	str = strings.TrimSpace(str)
	if len(str) >= 2 && str[0] == '"' && str[len(str)-1] == '"' {
		return str[1 : len(str)-1]
	}
	return str
}

func requiresArgument(token string) bool {
	switch token {
	case "BCC", "CC", "FROM", "SUBJECT", "TO", "BODY", "TEXT",
		"KEYWORD", "UNKEYWORD", "LARGER", "SMALLER", "UID",
		"BEFORE", "ON", "SINCE", "SENTBEFORE", "SENTON", "SENTSINCE":
		return true
	case "HEADER":
		return true // Actually requires 2 arguments, but handle separately
	}
	return false
}

// ===== STORE =====

func HandleStore(deps ServerDeps, conn net.Conn, tag string, parts []string, state *models.ClientState) {
	// RFC 3501: STORE requires authentication and selected mailbox
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No mailbox selected", tag))
		return
	}

	// Parse command: STORE sequence data-item value
	if len(parts) < 4 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD STORE requires sequence set, data item, and value", tag))
		return
	}

	sequenceSet := parts[2]
	dataItem := strings.ToUpper(parts[3])

	// Check if .SILENT suffix is used
	silent := strings.HasSuffix(dataItem, ".SILENT")
	if silent {
		dataItem = strings.TrimSuffix(dataItem, ".SILENT")
	}

	// Parse flags from remaining parts
	flagsPart := strings.Join(parts[4:], " ")
	flagsPart = strings.Trim(flagsPart, "()")
	newFlags := strings.Fields(flagsPart)

	// Validate data item
	if dataItem != "FLAGS" && dataItem != "+FLAGS" && dataItem != "-FLAGS" {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD Invalid data item: %s", tag, parts[3]))
		return
	}

	// Get user database
	userDB, err := deps.GetUserDB(resolveStateEmail(state))
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	// Parse sequence set
	sequences := utils.ParseSequenceSetWithDB(sequenceSet, state.SelectedMailboxID, userDB)
	if len(sequences) == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD Invalid sequence set", tag))
		return
	}

	// Process each message in the sequence
	for _, seqNum := range sequences {
		// Get message by sequence number
		query := `
			SELECT mm.message_id, mm.uid, mm.flags, mm.internal_date
			FROM message_mailbox mm
			WHERE mm.mailbox_id = ?
			ORDER BY mm.uid ASC
			LIMIT 1 OFFSET ?
		`
		var messageID, uid int64
		var currentFlags, internalDate string
		err := userDB.QueryRow(query, state.SelectedMailboxID, seqNum-1).Scan(&messageID, &uid, &currentFlags, &internalDate)
		if err != nil {
			// Message not found - skip
			continue
		}

		// Calculate new flags based on operation
		updatedFlags := CalculateNewFlags(currentFlags, newFlags, dataItem)

		// Check if Junk or NonJunk flags were added
		currentFlagsSet := parseFlagsToSet(currentFlags)
		updatedFlagsSet := parseFlagsToSet(updatedFlags)

		junkAdded := !currentFlagsSet["Junk"] && updatedFlagsSet["Junk"]
		nonJunkAdded := !currentFlagsSet["NonJunk"] && updatedFlagsSet["NonJunk"]

		// Auto-move messages based on Junk/NonJunk flags
		// These flags are mutually exclusive - ensure only one is set
		if junkAdded {
			// Remove NonJunk flag if present (mutually exclusive with Junk)
			cleanedFlags := removeFlagFromSet(updatedFlagsSet, "NonJunk")
			cleanedFlagsStr := flagSetToString(cleanedFlags)

			// Move to Spam folder, saving the current mailbox as original
			err = MoveMessageToMailbox(userDB, messageID, state.SelectedMailboxID, "Spam", state.UserID, cleanedFlagsStr, internalDate, &state.SelectedMailboxID)
			if err != nil {
				log.Printf("Failed to move message %d to Spam: %v", messageID, err)
			} else {
				log.Printf("Auto-moved message %d to Spam folder (Junk flag added)", messageID)
				// Send EXPUNGE notification to tell client the message is gone from this mailbox
				if !silent {
					deps.SendResponse(conn, fmt.Sprintf("* %d EXPUNGE", seqNum))
				}
				// Message was moved - don't send FETCH response since it's no longer in this mailbox
				continue
			}
		} else if nonJunkAdded {
			// Remove Junk flag if present (mutually exclusive with NonJunk)
			cleanedFlags := removeFlagFromSet(updatedFlagsSet, "Junk")
			cleanedFlagsStr := flagSetToString(cleanedFlags)

			// Determine restoration target
			targetFolder := "INBOX"
			var prevMailboxID sql.NullInt64
			dbErr := userDB.QueryRow("SELECT previous_mailbox_id FROM message_mailbox WHERE message_id = ? AND mailbox_id = ?", messageID, state.SelectedMailboxID).Scan(&prevMailboxID)
			if dbErr != nil && dbErr != sql.ErrNoRows {
				log.Printf("Failed to query previous_mailbox_id for message %d: %v", messageID, dbErr)
			}

			if prevMailboxID.Valid {
				// Try to get the name of the previous mailbox
				var prevName string
				dbErr = userDB.QueryRow("SELECT name FROM mailboxes WHERE id = ?", prevMailboxID.Int64).Scan(&prevName)
				if dbErr != nil && dbErr != sql.ErrNoRows {
					log.Printf("Failed to get name for previous mailbox ID %d: %v", prevMailboxID.Int64, dbErr)
				} else if dbErr == nil {
					targetFolder = prevName
				}
			}

			// Move to target folder (original or INBOX)
			err = MoveMessageToMailbox(userDB, messageID, state.SelectedMailboxID, targetFolder, state.UserID, cleanedFlagsStr, internalDate, nil)
			if err != nil {
				log.Printf("Failed to move message %d to %s: %v", messageID, targetFolder, err)
			} else {
				log.Printf("Auto-moved message %d to %s (NonJunk flag added)", messageID, targetFolder)
				// Send EXPUNGE notification to tell client the message is gone from this mailbox
				if !silent {
					deps.SendResponse(conn, fmt.Sprintf("* %d EXPUNGE", seqNum))
				}
				// Message was moved - don't send FETCH response since it's no longer in this mailbox
				continue
			}
		}

		// Update flags in database (only if message wasn't moved)
		updateQuery := "UPDATE message_mailbox SET flags = ? WHERE message_id = ? AND mailbox_id = ?"
		_, err = userDB.Exec(updateQuery, updatedFlags, messageID, state.SelectedMailboxID)
		if err != nil {
			log.Printf("Failed to update flags for message %d: %v", messageID, err)
			continue
		}

		// Send untagged FETCH response unless .SILENT
		if !silent {
			flagsFormatted := "()"
			if updatedFlags != "" {
				flagsFormatted = fmt.Sprintf("(%s)", updatedFlags)
			}
			deps.SendResponse(conn, fmt.Sprintf("* %d FETCH (FLAGS %s)", seqNum, flagsFormatted))
		}
	}

	deps.SendResponse(conn, fmt.Sprintf("%s OK STORE completed", tag))
}

// parseFlagsToSet converts a space-separated flags string into a set (map)
func parseFlagsToSet(flags string) map[string]bool {
	flagSet := make(map[string]bool)
	if flags != "" {
		for _, flag := range strings.Fields(flags) {
			flagSet[flag] = true
		}
	}
	return flagSet
}

// removeFlagFromSet removes a specific flag from the flag set
func removeFlagFromSet(flagSet map[string]bool, flagToRemove string) map[string]bool {
	newSet := make(map[string]bool)
	for flag, val := range flagSet {
		if flag != flagToRemove {
			newSet[flag] = val
		}
	}
	return newSet
}

// flagSetToString converts a flag set back to a space-separated string
func flagSetToString(flagSet map[string]bool) string {
	var flags []string
	for flag := range flagSet {
		flags = append(flags, flag)
	}
	return strings.Join(flags, " ")
}

// CalculateNewFlags determines the new flags based on the operation
func CalculateNewFlags(currentFlags string, newFlags []string, operation string) string {
	// Parse current flags into a map
	flagMap := make(map[string]bool)
	if currentFlags != "" {
		for _, flag := range strings.Fields(currentFlags) {
			flagMap[flag] = true
		}
	}

	switch operation {
	case "FLAGS":
		// Replace all flags (except \Recent which server manages)
		flagMap = make(map[string]bool)
		for _, flag := range newFlags {
			if flag != "\\Recent" {
				flagMap[flag] = true
			}
		}

	case "+FLAGS":
		// Add flags
		for _, flag := range newFlags {
			if flag != "\\Recent" {
				flagMap[flag] = true
			}
		}

	case "-FLAGS":
		// Remove flags
		for _, flag := range newFlags {
			if flag != "\\Recent" {
				delete(flagMap, flag)
			}
		}
	}

	// Convert map back to string
	var flags []string
	for flag := range flagMap {
		flags = append(flags, flag)
	}

	return strings.Join(flags, " ")
}

// ===== COPY =====

// handleCopy implements the COPY command (RFC 3501 Section 6.4.7)
// Syntax: COPY sequence-set mailbox-name
func HandleCopy(deps ServerDeps, conn net.Conn, tag string, parts []string, state *models.ClientState) {
	// Check authentication
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	// Check if mailbox is selected
	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No mailbox selected", tag))
		return
	}

	// Parse command: COPY sequence-set mailbox-name
	// Some tests call handler directly with parts starting at COPY,
	// while server command dispatch includes the tag as parts[0].
	var sequenceSet string
	var destMailbox string
	switch {
	case len(parts) >= 3 && strings.EqualFold(parts[0], "COPY"):
		sequenceSet = parts[1]
		destMailbox = strings.Trim(strings.Join(parts[2:], " "), "\"")
	case len(parts) >= 4 && strings.EqualFold(parts[1], "COPY"):
		sequenceSet = parts[2]
		destMailbox = strings.Trim(strings.Join(parts[3:], " "), "\"")
	default:
		deps.SendResponse(conn, fmt.Sprintf("%s BAD Invalid COPY command syntax", tag))
		return
	}

	// Use selected database so COPY works for both regular and role mailboxes
	targetDB, err := deps.GetSelectedDB(state)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	// Parse sequence set
	sequences := utils.ParseSequenceSetWithDB(sequenceSet, state.SelectedMailboxID, targetDB)
	if len(sequences) == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD Invalid sequence set", tag))
		return
	}

	// Check if destination mailbox exists
	var destMailboxID int64
	err = targetDB.QueryRow(`
		SELECT id FROM mailboxes
		WHERE name = ?
	`, destMailbox).Scan(&destMailboxID)

	if err != nil {
		// Destination mailbox doesn't exist - return NO with [TRYCREATE]
		deps.SendResponse(conn, fmt.Sprintf("%s NO [TRYCREATE] Destination mailbox does not exist", tag))
		return
	}

	// Begin transaction to ensure atomicity
	tx, err := targetDB.Begin()
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO COPY failed: %v", tag, err))
		return
	}
	defer func() { _ = tx.Rollback() }()

	// Get the next UID for destination mailbox
	var nextUID int64
	err = tx.QueryRow(`
		SELECT COALESCE(MAX(uid), 0) + 1
		FROM message_mailbox
		WHERE mailbox_id = ?
	`, destMailboxID).Scan(&nextUID)

	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO COPY failed: %v", tag, err))
		return
	}

	// Copy each message in the sequence
	for _, seqNum := range sequences {
		// Get message details from source mailbox
		var messageID int64
		var flags, internalDate string

		err = tx.QueryRow(`
			SELECT mm.message_id, mm.flags, mm.internal_date
			FROM message_mailbox mm
			WHERE mm.mailbox_id = ?
			ORDER BY mm.uid
			LIMIT 1 OFFSET ?
		`, state.SelectedMailboxID, seqNum-1).Scan(&messageID, &flags, &internalDate)

		if err != nil {
			deps.SendResponse(conn, fmt.Sprintf("%s NO COPY failed: %v", tag, err))
			return
		}

		// Prepare flags for copy - preserve existing flags and add \Recent
		copyFlags := flags
		if !strings.Contains(copyFlags, `\Recent`) {
			if copyFlags == "" {
				copyFlags = `\Recent`
			} else {
				copyFlags = copyFlags + ` \Recent`
			}
		}

		// Insert message into destination mailbox
		_, err = tx.Exec(`
			INSERT INTO message_mailbox (message_id, mailbox_id, uid, flags, internal_date)
			VALUES (?, ?, ?, ?, ?)
		`, messageID, destMailboxID, nextUID, copyFlags, internalDate)

		if err != nil {
			deps.SendResponse(conn, fmt.Sprintf("%s NO COPY failed: %v", tag, err))
			return
		}

		nextUID++
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO COPY failed: %v", tag, err))
		return
	}

	deps.SendResponse(conn, fmt.Sprintf("%s OK COPY completed", tag))
}

// MoveMessageToMailbox moves a message from the current mailbox to a destination mailbox
// Returns the new sequence number in the destination mailbox, or 0 if failed
func MoveMessageToMailbox(userDB *sql.DB, messageID int64, sourceMailboxID int64, destMailboxName string, userID int64, flags string, internalDate string, previousMailboxID *int64) error {
	// Get destination mailbox ID
	var destMailboxID int64
	err := userDB.QueryRow(`
		SELECT id FROM mailboxes
		WHERE name = ?
	`, destMailboxName).Scan(&destMailboxID)

	if err != nil {
		return fmt.Errorf("destination mailbox not found: %w", err)
	}

	// Don't move if already in the destination mailbox
	if sourceMailboxID == destMailboxID {
		return nil
	}

	// Begin transaction
	tx, err := userDB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Get the next UID for destination mailbox
	var nextUID int64
	err = tx.QueryRow(`
		SELECT COALESCE(MAX(uid), 0) + 1
		FROM message_mailbox
		WHERE mailbox_id = ?
	`, destMailboxID).Scan(&nextUID)

	if err != nil {
		return fmt.Errorf("failed to get next UID: %w", err)
	}

	// Insert message into destination mailbox (preserve flags and internal date)
	_, err = tx.Exec(`
		INSERT INTO message_mailbox (message_id, mailbox_id, uid, flags, internal_date, previous_mailbox_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`, messageID, destMailboxID, nextUID, flags, internalDate, previousMailboxID)

	if err != nil {
		return fmt.Errorf("failed to insert into destination: %w", err)
	}

	// Delete message from source mailbox
	_, err = tx.Exec(`
		DELETE FROM message_mailbox
		WHERE message_id = ? AND mailbox_id = ?
	`, messageID, sourceMailboxID)

	if err != nil {
		return fmt.Errorf("failed to delete from source: %w", err)
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ===== APPEND =====

// handleAppendWithReader handles the APPEND command with a buffered reader to properly read literal data
func HandleAppendWithReader(deps ServerDeps, reader io.Reader, conn net.Conn, tag string, parts []string, fullLine string, state *models.ClientState) {
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	if len(parts) < 3 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD APPEND requires folder name", tag))
		return
	}

	// Get user database
	userDB, err := deps.GetUserDB(resolveStateEmail(state))
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	// Parse folder name (could be quoted)
	folder := strings.Trim(parts[2], "\"")

	// Validate folder exists using the database with new schema
	mailboxID, err := db.GetMailboxByNamePerUser(userDB, folder)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO [TRYCREATE] Folder does not exist", tag))
		return
	}

	// Parse optional flags and date/time
	// Format: tag APPEND folder [(flags)] [date-time] {size}
	var flags string

	// Look for flags in parentheses
	if strings.Contains(fullLine, "(") && strings.Contains(fullLine, ")") {
		startIdx := strings.Index(fullLine, "(")
		endIdx := strings.Index(fullLine, ")")
		if startIdx < endIdx {
			flags = fullLine[startIdx+1 : endIdx]
		}
	}

	// Look for literal size indicator {size} or {size+}
	literalStartIdx := strings.Index(fullLine, "{")
	literalEndIdx := strings.Index(fullLine, "}")

	if literalStartIdx == -1 || literalEndIdx == -1 || literalStartIdx > literalEndIdx {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD APPEND requires message size", tag))
		return
	}

	// Extract the size and check for LITERAL+ (RFC 4466)
	sizeStr := fullLine[literalStartIdx+1 : literalEndIdx]
	isLiteralPlus := strings.HasSuffix(sizeStr, "+")
	if isLiteralPlus {
		sizeStr = strings.TrimSuffix(sizeStr, "+")
	}

	var messageSize int
	_, _ = fmt.Sscanf(sizeStr, "%d", &messageSize)

	if messageSize <= 0 || messageSize > 50*1024*1024 { // Max 50MB
		deps.SendResponse(conn, fmt.Sprintf("%s NO Message size invalid or too large", tag))
		return
	}

	// Send continuation response only for synchronizing literals
	// RFC 4466: LITERAL+ ({size+}) means client sends data immediately without waiting
	if !isLiteralPlus {
		deps.SendResponse(conn, "+ Ready for literal data")
	}

	// Read the message data using io.ReadFull from the buffered reader
	messageData := make([]byte, messageSize)

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	log.Printf("APPEND expecting %d bytes literal", messageSize)

	n, err := io.ReadFull(reader, messageData)
	if err != nil {
		log.Printf("Error reading message data: expected %d bytes, read %d bytes, error: %v", messageSize, n, err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO Failed to read message data", tag))
		return
	}

	log.Printf("APPEND successfully read %d bytes", n)

	// Read and discard the trailing CRLF after the literal data
	// RFC 3501: The client sends CRLF after the literal data
	// Use a short timeout to avoid delays
	crlfBuf := make([]byte, 2)
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err = reader.Read(crlfBuf)
	if err != nil && err != io.EOF {
		log.Printf("Warning: Failed to read trailing CRLF after literal data: %v", err)
		// Continue anyway - some clients might not send it
	} else if n > 0 && len(crlfBuf) > 0 && (crlfBuf[0] != '\r' && crlfBuf[0] != '\n') {
		log.Printf("Warning: Expected CRLF after literal data, got: %v", crlfBuf[:n])
		// Continue anyway - be lenient with protocol violations
	}

	rawMessage := string(messageData)

	// Ensure message has CRLF line endings
	if !strings.Contains(rawMessage, "\r\n") {
		rawMessage = strings.ReplaceAll(rawMessage, "\n", "\r\n")
	}

	// Parse and store message using new schema
	parsed, err := parser.ParseMIMEMessage(rawMessage)
	if err != nil {
		log.Printf("Failed to parse message: %v", err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO [SERVERBUG] Failed to parse message", tag))
		return
	}

	// Get shared database for blob deduplication
	sharedDB := deps.GetSharedDB()

	// Store message in database with S3 support and shared blob deduplication
	s3Storage := deps.GetS3Storage()
	messageID, err := parser.StoreMessagePerUserWithSharedDBAndS3(sharedDB, userDB, parsed, s3Storage)
	if err != nil {
		log.Printf("Failed to store message: %v", err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO [SERVERBUG] Failed to save message", tag))
		return
	}

	// Add message to mailbox
	internalDate := time.Now()
	err = db.AddMessageToMailboxPerUser(userDB, messageID, mailboxID, flags, internalDate)
	if err != nil {
		log.Printf("Failed to add message to mailbox: %v", err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO [SERVERBUG] Failed to add message to mailbox", tag))
		return
	}

	// Get UID validity for APPENDUID response
	uidValidity, _, err := db.GetMailboxInfoPerUser(userDB, mailboxID)
	if err != nil {
		uidValidity = 1
	}

	// Get the UID assigned to the message
	var newUID int64
	query := "SELECT uid FROM message_mailbox WHERE message_id = ? AND mailbox_id = ?"
	err = userDB.QueryRow(query, messageID, mailboxID).Scan(&newUID)
	if err != nil {
		log.Printf("Failed to get new UID: %v", err)
		newUID = 1
	}

	log.Printf("Message appended to folder '%s' with UID %d", folder, newUID)

	// Send success response with APPENDUID (RFC 4315 - UIDPLUS extension)
	deps.SendResponse(conn, fmt.Sprintf("%s OK [APPENDUID %d %d] APPEND completed", tag, uidValidity, newUID))
}

// handleAppend handles the APPEND command to add a message to a mailbox (legacy - kept for compatibility)
func HandleAppend(deps ServerDeps, conn net.Conn, tag string, parts []string, fullLine string, state *models.ClientState) {
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	if len(parts) < 3 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD APPEND requires folder name", tag))
		return
	}

	// Get user database
	userDB, err := deps.GetUserDB(resolveStateEmail(state))
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	// Parse folder name (could be quoted)
	folder := strings.Trim(parts[2], "\"")

	// Validate folder exists using the database with new schema
	mailboxID, err := db.GetMailboxByNamePerUser(userDB, folder)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO [TRYCREATE] Folder does not exist", tag))
		return
	}

	// Parse optional flags and date/time
	// Format: tag APPEND folder [(flags)] [date-time] {size}
	var flags string

	// Look for flags in parentheses
	if strings.Contains(fullLine, "(") && strings.Contains(fullLine, ")") {
		startIdx := strings.Index(fullLine, "(")
		endIdx := strings.Index(fullLine, ")")
		if startIdx < endIdx {
			flags = fullLine[startIdx+1 : endIdx]
		}
	}

	// Look for literal size indicator {size} or {size+}
	literalStartIdx := strings.Index(fullLine, "{")
	literalEndIdx := strings.Index(fullLine, "}")

	if literalStartIdx == -1 || literalEndIdx == -1 || literalStartIdx > literalEndIdx {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD APPEND requires message size", tag))
		return
	}

	// Extract the size and check for LITERAL+ (RFC 4466)
	sizeStr := fullLine[literalStartIdx+1 : literalEndIdx]
	isLiteralPlus := strings.HasSuffix(sizeStr, "+")
	if isLiteralPlus {
		sizeStr = strings.TrimSuffix(sizeStr, "+")
	}

	var messageSize int
	_, _ = fmt.Sscanf(sizeStr, "%d", &messageSize)

	if messageSize <= 0 || messageSize > 50*1024*1024 { // Max 50MB
		deps.SendResponse(conn, fmt.Sprintf("%s NO Message size invalid or too large", tag))
		return
	}

	// Send continuation response only for synchronizing literals
	// RFC 4466: LITERAL+ ({size+}) means client sends data immediately without waiting
	if !isLiteralPlus {
		deps.SendResponse(conn, "+ Ready for literal data")
	}

	// Read the message data using io.ReadFull to ensure we read exactly messageSize bytes
	messageData := make([]byte, messageSize)

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	log.Printf("APPEND expecting %d bytes literal", messageSize)

	n, err := io.ReadFull(conn, messageData)
	if err != nil {
		log.Printf("Error reading message data: expected %d bytes, read %d bytes, error: %v", messageSize, n, err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO Failed to read message data", tag))
		return
	}

	log.Printf("APPEND successfully read %d bytes", n)

	// Read and discard the trailing CRLF after the literal data
	// RFC 3501: The client sends CRLF after the literal data
	// Use a short timeout to avoid delays
	crlfBuf := make([]byte, 2)
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err = conn.Read(crlfBuf)
	if err != nil {
		log.Printf("Warning: Failed to read trailing CRLF after literal data: %v", err)
		// Continue anyway - some clients might not send it
	} else if n > 0 && len(crlfBuf) > 0 && (crlfBuf[0] != '\r' && crlfBuf[0] != '\n') {
		log.Printf("Warning: Expected CRLF after literal data, got: %v", crlfBuf[:n])
		// Continue anyway - be lenient with protocol violations
	}

	rawMessage := string(messageData)

	// Ensure message has CRLF line endings
	if !strings.Contains(rawMessage, "\r\n") {
		rawMessage = strings.ReplaceAll(rawMessage, "\n", "\r\n")
	}

	// Parse and store message using new schema
	parsed, err := parser.ParseMIMEMessage(rawMessage)
	if err != nil {
		log.Printf("Failed to parse message: %v", err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO [SERVERBUG] Failed to parse message", tag))
		return
	}

	// Get shared database for blob deduplication
	sharedDB := deps.GetSharedDB()

	// Store message in database with S3 support and shared blob deduplication
	s3Storage := deps.GetS3Storage()
	messageID, err := parser.StoreMessagePerUserWithSharedDBAndS3(sharedDB, userDB, parsed, s3Storage)
	if err != nil {
		log.Printf("Failed to store message: %v", err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO [SERVERBUG] Failed to save message", tag))
		return
	}

	// Add message to mailbox
	internalDate := time.Now()
	err = db.AddMessageToMailboxPerUser(userDB, messageID, mailboxID, flags, internalDate)
	if err != nil {
		log.Printf("Failed to add message to mailbox: %v", err)
		deps.SendResponse(conn, fmt.Sprintf("%s NO [SERVERBUG] Failed to add message to mailbox", tag))
		return
	}

	// Get UID validity for APPENDUID response
	uidValidity, _, err := db.GetMailboxInfoPerUser(userDB, mailboxID)
	if err != nil {
		uidValidity = 1
	}

	// Get the UID assigned to the message
	var newUID int64
	query := "SELECT uid FROM message_mailbox WHERE message_id = ? AND mailbox_id = ?"
	err = userDB.QueryRow(query, messageID, mailboxID).Scan(&newUID)
	if err != nil {
		log.Printf("Failed to get new UID: %v", err)
		newUID = 1
	}

	log.Printf("Message appended to folder '%s' with UID %d", folder, newUID)

	// Send success response with APPENDUID (RFC 4315 - UIDPLUS extension)
	deps.SendResponse(conn, fmt.Sprintf("%s OK [APPENDUID %d %d] APPEND completed", tag, uidValidity, newUID))
}

// ===== EXPUNGE =====

func HandleExpunge(deps ServerDeps, conn net.Conn, tag string, state *models.ClientState) {
	// EXPUNGE command requires authentication
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	// EXPUNGE command requires a selected mailbox (Selected state)
	// Per RFC 3501: EXPUNGE is only valid in Selected state
	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No mailbox selected", tag))
		return
	}

	// Per RFC 3501: EXPUNGE permanently removes all messages with \Deleted flag
	// Before returning OK, an untagged EXPUNGE response is sent for each message removed
	// The key difference from CLOSE: EXPUNGE sends untagged responses showing which
	// messages were deleted

	// Important: Per RFC 3501, if mailbox is read-only (selected with EXAMINE),
	// EXPUNGE should return NO
	// TODO: Add ReadOnly field to ClientState to properly handle EXAMINE

	// Get user database
	userDB, err := deps.GetUserDB(resolveStateEmail(state))
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	// Query for all messages with \Deleted flag, ordered by sequence number
	// We need to get the sequence numbers before deletion
	rows, err := userDB.Query(`
		SELECT id, uid FROM message_mailbox
		WHERE mailbox_id = ? AND flags LIKE '%\Deleted%'
		ORDER BY uid ASC
	`, state.SelectedMailboxID)

	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO EXPUNGE failed: %v", tag, err))
		return
	}
	defer func() { _ = rows.Close() }()

	// Collect messages to delete with their UIDs
	type messageToDelete struct {
		id  int64
		uid int64
	}
	var messagesToDelete []messageToDelete
	for rows.Next() {
		var msg messageToDelete
		if err := rows.Scan(&msg.id, &msg.uid); err == nil {
			messagesToDelete = append(messagesToDelete, msg)
		}
	}
	_ = rows.Close()

	// If no messages to delete, just return OK
	if len(messagesToDelete) == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s OK EXPUNGE completed", tag))
		return
	}

	// Get all messages in the mailbox to calculate sequence numbers
	allRows, err := userDB.Query(`
		SELECT id, uid FROM message_mailbox
		WHERE mailbox_id = ?
		ORDER BY uid ASC
	`, state.SelectedMailboxID)

	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO EXPUNGE failed: %v", tag, err))
		return
	}
	defer func() { _ = allRows.Close() }()

	// Build a map of message IDs to sequence numbers
	sequenceMap := make(map[int64]int)
	seqNum := 1
	for allRows.Next() {
		var id, uid int64
		if err := allRows.Scan(&id, &uid); err == nil {
			sequenceMap[id] = seqNum
			seqNum++
		}
	}
	_ = allRows.Close()

	// Delete messages and send EXPUNGE responses
	// Important: As we delete messages, sequence numbers change for subsequent messages
	// We need to account for this by tracking how many messages we've deleted
	deletedCount := 0
	for _, msg := range messagesToDelete {
		// Get the original sequence number for this message
		originalSeqNum := sequenceMap[msg.id]

		// Adjust for previously deleted messages in this EXPUNGE operation
		// When we delete message N, all messages after it shift down by 1
		adjustedSeqNum := originalSeqNum - deletedCount

		// Send untagged EXPUNGE response with the adjusted sequence number
		deps.SendResponse(conn, fmt.Sprintf("* %d EXPUNGE", adjustedSeqNum))

		// Delete the message from the mailbox
		_, _ = userDB.Exec(`DELETE FROM message_mailbox WHERE id = ?`, msg.id)

		deletedCount++
	}

	// Update state tracking
	state.LastMessageCount -= len(messagesToDelete)
	if state.LastMessageCount < 0 {
		state.LastMessageCount = 0
	}

	// Send completion response
	deps.SendResponse(conn, fmt.Sprintf("%s OK EXPUNGE completed", tag))
}

// ===== CHECK =====

func HandleCheck(deps ServerDeps, conn net.Conn, tag string, state *models.ClientState) {
	// CHECK command requires authentication
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	// CHECK command requires a selected mailbox (Selected state)
	// Per RFC 3501: CHECK is only valid in Selected state
	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No mailbox selected", tag))
		return
	}

	// Get user database
	userDB, err := deps.GetUserDB(resolveStateEmail(state))
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s OK CHECK completed", tag))
		return
	}

	// Perform checkpoint operations for the currently selected mailbox
	// This involves resolving the server's in-memory state with the state on disk
	// In our implementation, this is similar to NOOP but emphasizes housekeeping

	// Get current mailbox state
	currentCount, err := db.GetMessageCountPerUser(userDB, state.SelectedMailboxID)
	if err != nil {
		// If there's a database error, still complete normally per RFC 3501
		// CHECK should always succeed even if housekeeping fails
		deps.SendResponse(conn, fmt.Sprintf("%s OK CHECK completed", tag))
		return
	}

	currentRecent, err := db.GetRecentCountPerUser(userDB, state.SelectedMailboxID)
	if err != nil {
		currentRecent = 0
	}

	// Update state tracking to ensure in-memory state matches database
	// This is the "checkpoint" - synchronizing cached state with actual state
	state.LastMessageCount = currentCount
	state.LastRecentCount = currentRecent

	// Note: Unlike NOOP, CHECK does not guarantee sending EXISTS responses
	// Per RFC 3501: "There is no guarantee that an EXISTS untagged response
	// will happen as a result of CHECK. NOOP, not CHECK, SHOULD be used for
	// new message polling."
	// Therefore, we do NOT send untagged responses here

	// Always complete successfully per RFC 3501
	deps.SendResponse(conn, fmt.Sprintf("%s OK CHECK completed", tag))
}
