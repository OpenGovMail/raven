package message

import (
	"database/sql"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"raven/internal/db"
	"raven/internal/delivery/parser"
	"raven/internal/models"
	"raven/internal/server/response"
	"raven/internal/server/utils"
)

// ===== FETCH =====

// HandleFetchForUIDs handles FETCH for a list of UIDs (used by UID FETCH command)
func HandleFetchForUIDs(deps ServerDeps, conn net.Conn, tag string, uids []int, items string, state *models.ClientState) {
	// Get appropriate database (user or role mailbox)
	targetDB, err := deps.GetSelectedDB(state)
	if err != nil {
		return
	}

	for _, uid := range uids {
		// Get message details by UID
		var messageID int64
		var seqNum int
		var flags sql.NullString

		err := targetDB.QueryRow(`
			SELECT mm.message_id, mm.flags,
				(SELECT COUNT(*) FROM message_mailbox mm2
				 WHERE mm2.mailbox_id = mm.mailbox_id AND mm2.uid <= mm.uid) as seq_num
			FROM message_mailbox mm
			WHERE mm.mailbox_id = ? AND mm.uid = ?
		`, state.SelectedMailboxID, uid).Scan(&messageID, &flags, &seqNum)

		if err != nil {
			// Non-existent UID is silently ignored
			continue
		}

		// Process this message using the same logic as handleFetch
		processFetchForMessage(deps, conn, messageID, int64(uid), seqNum, flags.String, items, state)
	}
}

func HandleFetch(deps ServerDeps, conn net.Conn, tag string, parts []string, state *models.ClientState) {
	if !state.Authenticated {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Please authenticate first", tag))
		return
	}

	if state.SelectedMailboxID == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s NO No folder selected", tag))
		return
	}

	if len(parts) < 4 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD FETCH requires sequence and items", tag))
		return
	}

	// Get appropriate database (user or role mailbox)
	targetDB, err := deps.GetSelectedDB(state)
	if err != nil {
		deps.SendResponse(conn, fmt.Sprintf("%s NO Database error", tag))
		return
	}

	sequence := parts[2]
	items := strings.Join(parts[3:], " ")

	// Handle FETCH macros: ALL, FAST, FULL
	itemsUpper := strings.ToUpper(strings.TrimSpace(items))
	switch itemsUpper {
	case "ALL":
		items = "FLAGS INTERNALDATE RFC822.SIZE ENVELOPE"
	case "FAST":
		items = "FLAGS INTERNALDATE RFC822.SIZE"
	case "FULL":
		items = "FLAGS INTERNALDATE RFC822.SIZE ENVELOPE BODY"
	default:
		// Remove parentheses if present
		items = strings.Trim(items, "()")
	}

	// Support sequence sets: single numbers, ranges (1:5, 1:*, *), and comma-separated
	// combinations of both (1,3,5 / 1:3,7,9:*)
	seqNums := utils.ParseSequenceSetWithDB(sequence, state.SelectedMailboxID, targetDB)
	if len(seqNums) == 0 {
		deps.SendResponse(conn, fmt.Sprintf("%s BAD Invalid sequence number", tag))
		return
	}

	for _, seqNum := range seqNums {
		var messageID int64
		var uid int64
		var flagsStr sql.NullString
		query := `SELECT mm.message_id, mm.uid, mm.flags
		          FROM message_mailbox mm
		          WHERE mm.mailbox_id = ?
		          ORDER BY mm.uid ASC LIMIT 1 OFFSET ?`
		err := targetDB.QueryRow(query, state.SelectedMailboxID, seqNum-1).Scan(&messageID, &uid, &flagsStr)
		if err != nil {
			continue
		}

		flags := ""
		if flagsStr.Valid {
			flags = flagsStr.String
		}

		// Process this message
		processFetchForMessage(deps, conn, messageID, uid, seqNum, flags, items, state)
	}

	deps.SendResponse(conn, fmt.Sprintf("%s OK FETCH completed", tag))
}

// processFetchForMessage processes a single message for FETCH/UID FETCH
func processFetchForMessage(deps ServerDeps, conn net.Conn, messageID, uid int64, seqNum int, flags, items string, state *models.ClientState) {
	// Get appropriate database (user or role mailbox)
	targetDB, err := deps.GetSelectedDB(state)
	if err != nil {
		return
	}

	// Lazy-load the full reconstructed message only when needed
	var rawMsg string
	var rawMsgErr error
	loadRawMsg := func() string {
		if rawMsg == "" && rawMsgErr == nil {
			// Use S3 storage if available and shared DB for blob access
			sharedDB := deps.GetSharedDB()
			s3Storage := deps.GetS3Storage()
			rawMsg, rawMsgErr = parser.ReconstructMessageWithSharedDBAndS3(sharedDB, targetDB, messageID, s3Storage)
			if rawMsgErr != nil {
				return ""
			}
			if !strings.Contains(rawMsg, "\r\n") {
				rawMsg = strings.ReplaceAll(rawMsg, "\n", "\r\n")
			}
		}
		return rawMsg
	}

	itemsUpper := strings.ToUpper(items)
	responseParts := []string{}

	if strings.Contains(itemsUpper, "UID") {
		responseParts = append(responseParts, fmt.Sprintf("UID %d", uid))
	}
	if strings.Contains(itemsUpper, "FLAGS") {
		if flags == "" {
			flags = "()"
		} else {
			flags = fmt.Sprintf("(%s)", flags)
		}
		responseParts = append(responseParts, fmt.Sprintf("FLAGS %s", flags))
	}
	if strings.Contains(itemsUpper, "INTERNALDATE") {
		var internalDate time.Time
		// Query message_mailbox for internal_date using new schema
		query := "SELECT internal_date FROM message_mailbox WHERE message_id = ? AND mailbox_id = ?"
		err := targetDB.QueryRow(query, messageID, state.SelectedMailboxID).Scan(&internalDate)

		var dateStr string
		if err != nil || internalDate.IsZero() {
			dateStr = "01-Jan-1970 00:00:00 +0000"
		} else {
			// Format as RFC 3501: "02-Jan-2006 15:04:05 -0700"
			dateStr = internalDate.Format("02-Jan-2006 15:04:05 -0700")
		}
		responseParts = append(responseParts, fmt.Sprintf("INTERNALDATE \"%s\"", dateStr))
	}
	if strings.Contains(itemsUpper, "RFC822.SIZE") {
		msg := loadRawMsg()
		responseParts = append(responseParts, fmt.Sprintf("RFC822.SIZE %d", len(msg)))
	}

	// Handle ENVELOPE
	if strings.Contains(itemsUpper, "ENVELOPE") {
		msg := loadRawMsg()
		envelope := response.BuildEnvelope(msg)
		responseParts = append(responseParts, envelope)
	}

	// Handle BODYSTRUCTURE
	if strings.Contains(itemsUpper, "BODYSTRUCTURE") {
		msg := loadRawMsg()
		bodyStructure := response.BuildBodyStructure(msg)
		fmt.Printf("DEBUG FETCH: BODYSTRUCTURE for message %d: %s\n", messageID, bodyStructure)
		responseParts = append(responseParts, bodyStructure)
	}

	// Handle BODY (non-extensible BODYSTRUCTURE)
	if strings.Contains(itemsUpper, "BODY") && !strings.Contains(itemsUpper, "BODY[") && !strings.Contains(itemsUpper, "BODY.PEEK") && !strings.Contains(itemsUpper, "BODYSTRUCTURE") {
		// BODY is the non-extensible form of BODYSTRUCTURE
		msg := loadRawMsg()
		bodyStructure := response.BuildBodyStructure(msg)
		// Replace BODYSTRUCTURE with BODY in the response
		bodyStructure = strings.Replace(bodyStructure, "BODYSTRUCTURE", "BODY", 1)
		responseParts = append(responseParts, bodyStructure)
	}

	// Handle numeric BODY sections like BODY.PEEK[1], BODY[2], BODY[1.MIME] with optional partial ranges
	if strings.Contains(itemsUpper, "BODY[") || strings.Contains(itemsUpper, "BODY.PEEK[") {
		// Lazy-load parts for this message if needed
		var parts []map[string]interface{}
		loadParts := func() {
			if parts == nil {
				p, err := db.GetMessageParts(targetDB, messageID)
				if err == nil {
					parts = p
				}
			}
		}

		orig := items
		upper := itemsUpper
		pos := 0
		for {
			idxPeek := strings.Index(upper[pos:], "BODY.PEEK[")
			idxBody := strings.Index(upper[pos:], "BODY[")
			if idxPeek == -1 && idxBody == -1 {
				break
			}
			offset := pos
			prefix := "BODY["
			if idxPeek != -1 && (idxBody == -1 || idxPeek < idxBody) {
				offset += idxPeek
				prefix = "BODY.PEEK["
			} else {
				offset += idxBody
			}

			// Find closing bracket
			start := offset + len(prefix)
			end := strings.Index(upper[start:], "]")
			if end == -1 {
				break
			}
			end = start + end

			sectionSpec := orig[start:end] // preserve original case/format for echo
			sectionUpper := strings.ToUpper(sectionSpec)

			// Only handle numeric sections here; others handled elsewhere
			if len(sectionSpec) > 0 && sectionSpec[0] >= '0' && sectionSpec[0] <= '9' {
				// Determine if .MIME requested
				wantMIME := false
				partNumStr := sectionSpec
				if strings.Contains(sectionUpper, ".MIME") {
					wantMIME = true
					partNumStr = sectionSpec[:strings.Index(sectionUpper, ".MIME")]
				}
				// Parse part number - support nested parts like "1.2"
				partPath, err := parsePartNumberPath(partNumStr)
				if err == nil && len(partPath) > 0 {
					loadParts()
					// Debug: Show parts structure
					fmt.Printf("DEBUG FETCH: Looking up part %v for message %d, have %d parts\n", partPath, messageID, len(parts))
					for i, p := range parts {
						fmt.Printf("  Part %d: id=%v, part_number=%v, parent_part_id=%v, content_type=%v\n",
							i, p["id"], p["part_number"], p["parent_part_id"], p["content_type"])
					}

					// Map IMAP part number path to database part
					target := mapIMAPPartPathToDBPart(parts, partPath)

					fmt.Printf("DEBUG FETCH: mapIMAPPartPathToDBPart returned: %v\n", target != nil)

					payload := ""
					if target != nil {
						// Check if this is a multipart container (has no body)
						contentType, _ := target["content_type"].(string)
						isMultipart := strings.HasPrefix(contentType, "multipart/")

						if wantMIME {
							// Build MIME headers for the part
							hdr := buildMIMEHeadersForPart(target)
							payload = hdr
						} else if isMultipart {
							// For multipart containers, extract from the full reconstructed message
							fullMsg := loadRawMsg()
							payload = extractBodySectionByPath(fullMsg, partPath)
						} else {
							// Part body only - for non-multipart parts
							if blobID, ok := target["blob_id"].(int64); ok {
								// Get shared database for blob retrieval (blobs are now in shared DB)
								sharedDB := deps.GetSharedDB()

								// Try local storage first
								if content, err := db.GetBlob(sharedDB, blobID); err == nil && content != "" {
									payload = content
								} else {
									// Try S3 storage
									s3Storage := deps.GetS3Storage()
									if s3Storage != nil && s3Storage.IsEnabled() {
										if s3BlobID, storageType, err := db.GetBlobS3BlobID(sharedDB, blobID); err == nil && storageType == "s3" && s3BlobID != "" {
											if content, err := s3Storage.Retrieve(s3BlobID); err == nil {
												payload = content
											}
										}
									}
								}
							} else if textContent, ok := target["text_content"].(string); ok {
								payload = textContent
							}
						}
					}

					// Check for partial spec immediately following the closing bracket
					partialStartPos := -1
					after := end + 1
					if after < len(upper) && upper[after] == '<' {
						close := strings.Index(upper[after:], ">")
						if close != -1 {
							rangeSpec := upper[after+1 : after+close]
							var startPos, length int
							if _, err := fmt.Sscanf(rangeSpec, "%d.%d", &startPos, &length); err == nil {
								partialStartPos = startPos
								if startPos < len(payload) {
									endPos := startPos + length
									if endPos > len(payload) {
										endPos = len(payload)
									}
									payload = payload[startPos:endPos]
								} else {
									payload = ""
								}
							}
							// Advance parser position past the range
							end = after + close
						}
					}

					// Append response
					if payload == "" {
						responseParts = append(responseParts, fmt.Sprintf("BODY[%s] NIL", sectionSpec))
					} else {
						// Include partial start position in response if this was a partial fetch
						name := fmt.Sprintf("BODY[%s]", sectionSpec)
						if partialStartPos >= 0 {
							name = fmt.Sprintf("BODY[%s]<%d>", sectionSpec, partialStartPos)
						}
						responseParts = append(responseParts, fmt.Sprintf("%s {%d}\r\n%s", name, len(payload), payload))
					}
				}
			}

			// Move past this section for next search
			pos = end + 1
		}
	}

	// Handle multiple body parts - process each separately
	// Handle BODY.PEEK[HEADER.FIELDS (...)] or BODY[HEADER.FIELDS (...)] - specific header fields
	if strings.Contains(itemsUpper, "BODY.PEEK[HEADER.FIELDS") || strings.Contains(itemsUpper, "BODY[HEADER.FIELDS") {
		start := strings.Index(itemsUpper, "BODY.PEEK[HEADER.FIELDS")
		if start == -1 {
			start = strings.Index(itemsUpper, "BODY[HEADER.FIELDS")
		}

		// Extract requested header field names
		requestedHeaders := []string{"FROM", "TO", "CC", "BCC", "SUBJECT", "DATE", "MESSAGE-ID", "PRIORITY", "X-PRIORITY", "REFERENCES", "NEWSGROUPS", "IN-REPLY-TO", "CONTENT-TYPE", "REPLY-TO"}
		if start != -1 {
			isPeek := strings.Contains(itemsUpper, "BODY.PEEK[HEADER.FIELDS")
			prefixLen := len("BODY[HEADER.FIELDS (")
			if isPeek {
				prefixLen = len("BODY.PEEK[HEADER.FIELDS (")
			}

			fieldsStr := items[start+prefixLen:]
			closeParen := strings.Index(fieldsStr, ")")
			if closeParen != -1 {
				fieldsStr = fieldsStr[:closeParen]
				fields := strings.Fields(fieldsStr)
				if len(fields) > 0 {
					requestedHeaders = []string{}
					for _, f := range fields {
						requestedHeaders = append(requestedHeaders, strings.ToUpper(strings.TrimSpace(f)))
					}
				}
			}
		}

		// Extract only the requested headers from the message
		msg := loadRawMsg()
		headersMap := map[string]string{}
		lines := strings.Split(msg, "\r\n")
		currentHeader := ""
		for _, line := range lines {
			if line == "" {
				break // End of headers
			}
			// Check if this is a continuation line (starts with space or tab)
			if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				if currentHeader != "" {
					headersMap[currentHeader] += "\r\n" + line
				}
				continue
			}
			// New header line
			colonIdx := strings.Index(line, ":")
			if colonIdx != -1 {
				headerName := strings.ToUpper(strings.TrimSpace(line[:colonIdx]))
				for _, h := range requestedHeaders {
					if headerName == h {
						currentHeader = h
						headersMap[h] = line
						break
					}
				}
			}
		}

		// Build response with requested headers in order
		var headerLines []string
		for _, h := range requestedHeaders {
			if val, ok := headersMap[h]; ok {
				headerLines = append(headerLines, val)
			}
		}
		headersStr := strings.Join(headerLines, "\r\n")
		if len(headersStr) > 0 {
			headersStr += "\r\n"
		}
		headersStr += "\r\n" // Final blank line
		// Match the exact format the client requested
		fieldList := strings.Join(requestedHeaders, " ")
		responseParts = append(responseParts, fmt.Sprintf("BODY[HEADER.FIELDS (%s)] {%d}\r\n%s", fieldList, len(headersStr), headersStr))
	}

	// Handle BODY.PEEK[TEXT] or BODY[TEXT] - message body only (can be combined with other parts)
	if strings.Contains(itemsUpper, "BODY.PEEK[TEXT]") || strings.Contains(itemsUpper, "BODY[TEXT]") {
		msg := loadRawMsg()
		headerEnd := strings.Index(msg, "\r\n\r\n")
		body := ""
		if headerEnd != -1 {
			body = msg[headerEnd+4:] // skip the double CRLF
		}

		// Check for partial fetch like BODY.PEEK[TEXT]<0.2048>
		partialStart := 0
		partialLength := len(body)
		if strings.Contains(itemsUpper, "<") && strings.Contains(itemsUpper, ">") {
			startIdx := strings.Index(itemsUpper, "<")
			endIdx := strings.Index(itemsUpper, ">")
			if startIdx != -1 && endIdx > startIdx {
				partialSpec := itemsUpper[startIdx+1 : endIdx]
				_, _ = fmt.Sscanf(partialSpec, "%d.%d", &partialStart, &partialLength)
				if partialStart < len(body) {
					endPos := partialStart + partialLength
					if endPos > len(body) {
						endPos = len(body)
					}
					body = body[partialStart:endPos]
				} else {
					body = ""
				}
			}
		}

		responseParts = append(responseParts, fmt.Sprintf("BODY[TEXT] {%d}\r\n%s", len(body), body))
	}

	// Handle BODY.PEEK[HEADER] or BODY[HEADER] - all headers (check it's not HEADER.FIELDS)
	if (strings.Contains(itemsUpper, "BODY.PEEK[HEADER]") || strings.Contains(itemsUpper, "BODY[HEADER]")) &&
		!strings.Contains(itemsUpper, "HEADER.FIELDS") {
		msg := loadRawMsg()
		headerEnd := strings.Index(msg, "\r\n\r\n")
		headers := msg
		if headerEnd != -1 {
			headers = msg[:headerEnd+2] // include last CRLF
		}
		responseParts = append(responseParts, fmt.Sprintf("BODY[HEADER] {%d}\r\n%s", len(headers), headers))
	}

	// Handle RFC822.HEADER - return only the header portion
	if strings.Contains(itemsUpper, "RFC822.HEADER") {
		msg := loadRawMsg()
		headerEnd := strings.Index(msg, "\r\n\r\n")
		headers := msg
		if headerEnd != -1 {
			headers = msg[:headerEnd+2] // include last CRLF
		}
		responseParts = append(responseParts, fmt.Sprintf("RFC822.HEADER {%d}\r\n%s", len(headers), headers))
	}

	// Handle RFC822.TEXT - body text only (excluding headers)
	if strings.Contains(itemsUpper, "RFC822.TEXT") {
		msg := loadRawMsg()
		headerEnd := strings.Index(msg, "\r\n\r\n")
		body := ""
		if headerEnd != -1 {
			body = msg[headerEnd+4:] // skip the double CRLF
		}
		responseParts = append(responseParts, fmt.Sprintf("RFC822.TEXT {%d}\r\n%s", len(body), body))
	}

	// Handle BODY[] / BODY.PEEK[] / RFC822 / RFC822.PEEK - full message
	if strings.Contains(itemsUpper, "BODY[]") || strings.Contains(itemsUpper, "BODY.PEEK[]") ||
		strings.Contains(itemsUpper, "RFC822.PEEK") ||
		(strings.Contains(itemsUpper, "RFC822") && !strings.Contains(itemsUpper, "RFC822.SIZE") &&
			!strings.Contains(itemsUpper, "RFC822.HEADER") && !strings.Contains(itemsUpper, "RFC822.TEXT") && !strings.Contains(itemsUpper, "RFC822.PEEK")) {
		msg := loadRawMsg()
		responseParts = append(responseParts, fmt.Sprintf("BODY[] {%d}\r\n%s", len(msg), msg))
	}

	if len(responseParts) > 0 {
		responseStr := fmt.Sprintf("* %d FETCH (%s)", seqNum, strings.Join(responseParts, " "))
		deps.SendResponse(conn, responseStr)
	} else {
		deps.SendResponse(conn, fmt.Sprintf("* %d FETCH (FLAGS ())", seqNum))
	}
}

// extractBodySectionByPath extracts a nested MIME body section using a part path like [1, 2] for part 1.2
func extractBodySectionByPath(fullMessage string, partPath []int) string {
	if len(partPath) == 0 {
		return ""
	}

	// Start with the full message
	currentMessage := fullMessage

	// Navigate through each level of the path
	for level, partNum := range partPath {
		// Extract the part at this level
		extracted := extractSinglePart(currentMessage, partNum)
		if extracted == "" {
			return ""
		}

		// If this is the last level in the path, return the body
		if level == len(partPath)-1 {
			return extracted
		}

		// Otherwise, use this part as the message for the next level
		// We need to reconstruct it with headers for the next extraction
		currentMessage = extracted
	}

	return ""
}

// extractSinglePart extracts a single part from a message at the current level
func extractSinglePart(message string, partNum int) string {
	lines := strings.Split(message, "\r\n")

	// Find Content-Type and boundary
	var boundary string
	headerEnd := -1
	for i, line := range lines {
		if line == "" {
			headerEnd = i
			break
		}
		if strings.HasPrefix(strings.ToUpper(line), "CONTENT-TYPE:") {
			ctLine := line
			// Handle multi-line headers
			for j := i + 1; j < len(lines); j++ {
				if len(lines[j]) > 0 && (lines[j][0] == ' ' || lines[j][0] == '\t') {
					ctLine += lines[j]
				} else {
					break
				}
			}
			// Extract boundary
			if idx := strings.Index(strings.ToLower(ctLine), "boundary="); idx != -1 {
				boundaryPart := ctLine[idx+9:]
				if len(boundaryPart) > 0 && boundaryPart[0] == '"' {
					endQuote := strings.Index(boundaryPart[1:], "\"")
					if endQuote != -1 {
						boundary = boundaryPart[1 : endQuote+1]
					}
				} else {
					endIdx := strings.IndexAny(boundaryPart, "; \r\n")
					if endIdx != -1 {
						boundary = boundaryPart[:endIdx]
					} else {
						boundary = strings.TrimSpace(boundaryPart)
					}
				}
			}
		}
	}

	if boundary == "" || headerEnd == -1 {
		return ""
	}

	// Split by boundary
	body := strings.Join(lines[headerEnd+1:], "\r\n")
	delimiter := "--" + boundary
	parts := strings.Split(body, delimiter)

	// parts[0] = preamble, parts[1] = part 1, parts[2] = part 2, etc.
	if partNum <= 0 || partNum >= len(parts) {
		return ""
	}

	partContent := parts[partNum]

	// Skip closing boundary
	if strings.HasPrefix(strings.TrimSpace(partContent), "--") {
		return ""
	}

	// Trim leading CRLF
	partContent = strings.TrimPrefix(partContent, "\r\n")
	partContent = strings.TrimPrefix(partContent, "\n")

	// Extract body (for leaf parts) or return whole part with headers (for nested multiparts)
	partLines := strings.Split(partContent, "\r\n")
	partHeaderEnd := -1
	var partContentType string
	for i, line := range partLines {
		if line == "" {
			partHeaderEnd = i
			break
		}
		if strings.HasPrefix(strings.ToUpper(line), "CONTENT-TYPE:") {
			partContentType = line
		}
	}

	if partHeaderEnd == -1 {
		return ""
	}

	// Check if this is a nested multipart
	if strings.Contains(strings.ToLower(partContentType), "multipart/") {
		// Return the whole part including headers for further extraction
		return partContent
	}

	// For leaf parts, return just the body
	partBody := strings.Join(partLines[partHeaderEnd+1:], "\r\n")
	partBody = strings.TrimRight(partBody, "\r\n")

	return partBody
}

// parsePartNumberPath parses a part number like "1" or "1.2" into a path of integers
func parsePartNumberPath(partNumStr string) ([]int, error) {
	if partNumStr == "" {
		return nil, fmt.Errorf("empty part number")
	}

	// Split by '.' for nested parts
	parts := strings.Split(partNumStr, ".")
	path := make([]int, 0, len(parts))

	for _, p := range parts {
		num, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid part number: %s", p)
		}
		if num <= 0 {
			return nil, fmt.Errorf("part numbers must be positive: %d", num)
		}
		path = append(path, num)
	}

	return path, nil
}

// mapIMAPPartPathToDBPart maps an IMAP part path (like [1, 2] for part 1.2) to a database part
// Part path [1] = first top-level part
// Part path [1, 2] = second child of first top-level part
func mapIMAPPartPathToDBPart(parts []map[string]interface{}, partPath []int) map[string]interface{} {
	if len(partPath) == 0 {
		return nil
	}

	// Helper to read various numeric types as int
	asInt := func(v interface{}) (int, bool) {
		switch t := v.(type) {
		case int:
			return t, true
		case int8:
			return int(t), true
		case int16:
			return int(t), true
		case int32:
			return int(t), true
		case int64:
			return int(t), true
		case uint:
			if t > math.MaxInt {
				return 0, false
			}
			return int(t), true
		case uint8:
			return int(t), true
		case uint16:
			return int(t), true
		case uint32:
			return int(t), true
		case uint64:
			if t > math.MaxInt {
				return 0, false
			}
			return int(t), true
		case float32:
			return int(t), true
		case float64:
			return int(t), true
		default:
			return 0, false
		}
	}

	// Helper to get parent_part_id as int (if present)
	getParentID := func(p map[string]interface{}) (int, bool) {
		v, ok := p["parent_part_id"]
		if !ok || v == nil {
			return 0, false
		}
		return asInt(v)
	}

	// Helper to get children of a part by database ID (not part_number!)
	getChildren := func(parentDBID int) []map[string]interface{} {
		children := []map[string]interface{}{}
		for _, p := range parts {
			if pid, ok := getParentID(p); ok && pid == parentDBID {
				children = append(children, p)
			}
		}
		// Sort children by part_number to ensure correct order
		sort.Slice(children, func(i, j int) bool {
			pnI, _ := asInt(children[i]["part_number"])
			pnJ, _ := asInt(children[j]["part_number"])
			return pnI < pnJ
		})
		return children
	}

	// Get top-level parts
	// IMPORTANT: In IMAP, the root multipart container is invisible.
	// If there's a single root part that's a multipart container, we skip it
	// and treat its children as the IMAP top-level parts.
	topLevelParts := []map[string]interface{}{}
	for _, p := range parts {
		if _, hasParent := p["parent_part_id"]; !hasParent || p["parent_part_id"] == nil {
			topLevelParts = append(topLevelParts, p)
		}
	}
	// Sort by part_number
	sort.Slice(topLevelParts, func(i, j int) bool {
		pnI, _ := asInt(topLevelParts[i]["part_number"])
		pnJ, _ := asInt(topLevelParts[j]["part_number"])
		return pnI < pnJ
	})

	// Check if we have a single root multipart container
	// If so, skip it and use its children as IMAP top-level parts
	if len(topLevelParts) == 1 {
		rootPart := topLevelParts[0]
		contentType := ""
		if ct, ok := rootPart["content_type"].(string); ok {
			contentType = strings.ToLower(ct)
		}
		// If the root part is a multipart container, treat its children as top-level
		if strings.HasPrefix(contentType, "multipart/") {
			rootID, ok := asInt(rootPart["id"])
			if ok {
				topLevelParts = getChildren(rootID)
			}
		}
	}

	// Start with first part in path (top-level)
	if partPath[0] <= 0 || partPath[0] > len(topLevelParts) {
		return nil
	}
	current := topLevelParts[partPath[0]-1]

	// Traverse down the path
	for i := 1; i < len(partPath); i++ {
		// Get database ID of current part to find its children
		partDBID, ok := asInt(current["id"])
		if !ok {
			return nil
		}

		children := getChildren(partDBID)
		if partPath[i] <= 0 || partPath[i] > len(children) {
			return nil
		}
		current = children[partPath[i]-1]
	}

	return current
}

// buildMIMEHeadersForPart reconstructs MIME headers for a specific part
func buildMIMEHeadersForPart(part map[string]interface{}) string {
	var b strings.Builder
	contentType := part["content_type"].(string)
	if charset, ok := part["charset"].(string); ok && strings.TrimSpace(charset) != "" {
		b.WriteString(fmt.Sprintf("Content-Type: %s; charset=%s\r\n", contentType, charset))
	} else {
		b.WriteString(fmt.Sprintf("Content-Type: %s\r\n", contentType))
	}
	// Note: filename handling is done in Content-Disposition below
	// Many clients accept name= on Content-Type or only in Content-Disposition
	_ = part["filename"] // checked but handled elsewhere
	if encoding, ok := part["content_transfer_encoding"].(string); ok && strings.TrimSpace(encoding) != "" {
		b.WriteString(fmt.Sprintf("Content-Transfer-Encoding: %s\r\n", encoding))
	}
	if contentID, ok := part["content_id"].(string); ok && strings.TrimSpace(contentID) != "" {
		b.WriteString(fmt.Sprintf("Content-ID: %s\r\n", contentID))
	}
	if disp, ok := part["content_disposition"].(string); ok && strings.TrimSpace(disp) != "" {
		b.WriteString(fmt.Sprintf("Content-Disposition: %s", disp))
		if filename, ok := part["filename"].(string); ok && strings.TrimSpace(filename) != "" {
			// Only append filename if not already present in disp
			if !strings.Contains(strings.ToLower(disp), "filename=") {
				b.WriteString(fmt.Sprintf("; filename=\"%s\"", filename))
			}
		}
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return b.String()
}
