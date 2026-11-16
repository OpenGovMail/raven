package lmtp

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"raven/internal/delivery/config"
	"raven/internal/delivery/groupresolver"
	"raven/internal/delivery/parser"
	"raven/internal/delivery/storage"
)

// Session represents an LMTP session
type Session struct {
	conn          net.Conn
	reader        *bufio.Reader
	writer        *bufio.Writer
	storage       *storage.Storage
	config        *config.Config
	groupResolver *groupresolver.GroupResolver
	identityRes   identityResolver
	mailFrom      string
	recipients    []string
	recipientMap  map[string]string
	helo          string
}

// NewSession creates a new LMTP session
func NewSession(conn net.Conn, stor *storage.Storage, cfg *config.Config, gr *groupresolver.GroupResolver) *Session {
	return &Session{
		conn:          conn,
		reader:        bufio.NewReader(conn),
		writer:        bufio.NewWriter(conn),
		storage:       stor,
		config:        cfg,
		groupResolver: gr,
		identityRes:   newIdentityResolver(cfg),
		recipients:    make([]string, 0),
		recipientMap:  make(map[string]string),
	}
}

// Handle handles the LMTP session
func (s *Session) Handle() error {
	if s.identityRes != nil {
		defer func() { _ = s.identityRes.Close() }()
	}

	// Set connection timeout
	if s.config.LMTP.Timeout > 0 {
		timeout := time.Duration(s.config.LMTP.Timeout) * time.Second
		_ = s.conn.SetDeadline(time.Now().Add(timeout))
	}

	// Send greeting
	log.Printf("Sending greeting to %s", s.conn.RemoteAddr())
	if err := s.sendResponse(220, "%s LMTP Service ready", s.config.LMTP.Hostname); err != nil {
		log.Printf("Failed to send greeting to %s: %v", s.conn.RemoteAddr(), err)
		return err
	}
	log.Printf("Greeting sent successfully to %s, waiting for client command...", s.conn.RemoteAddr())

	// Process commands
	for {
		log.Printf("Waiting to read from %s...", s.conn.RemoteAddr())
		line, err := s.reader.ReadString('\n')
		if err != nil {
			log.Printf("Read failed from %s: %v (connection likely closed by client)", s.conn.RemoteAddr(), err)
			return fmt.Errorf("read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Sanitize line for logging to prevent log injection
		sanitizedLine := strings.ReplaceAll(strings.ReplaceAll(line, "\n", "\\n"), "\r", "\\r")
		// #nosec G706 -- Input is sanitized above to prevent log injection
		log.Printf("C: %s", sanitizedLine)

		// Parse command
		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(parts[0])
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		// Handle command
		if err := s.handleCommand(cmd, args); err != nil {
			log.Printf("Command error: %v", err)
			if strings.Contains(err.Error(), "QUIT") {
				return nil
			}
		}

		// Reset timeout after each command
		if s.config.LMTP.Timeout > 0 {
			timeout := time.Duration(s.config.LMTP.Timeout) * time.Second
			_ = s.conn.SetDeadline(time.Now().Add(timeout))
		}
	}
}

// handleCommand handles a single LMTP command
func (s *Session) handleCommand(cmd, args string) error {
	switch cmd {
	case "LHLO":
		return s.handleLHLO(args)
	case "MAIL":
		return s.handleMAIL(args)
	case "RCPT":
		return s.handleRCPT(args)
	case "DATA":
		return s.handleDATA()
	case "RSET":
		return s.handleRSET()
	case "NOOP":
		return s.handleNOOP()
	case "QUIT":
		return s.handleQUIT()
	case "VRFY":
		return s.handleVRFY(args)
	case "HELP":
		return s.handleHELP()
	default:
		return s.sendResponse(500, "Command not recognized")
	}
}

// handleLHLO handles the LHLO command
func (s *Session) handleLHLO(args string) error {
	if args == "" {
		return s.sendResponse(501, "LHLO requires domain address")
	}

	s.helo = args

	// Send multiline response with capabilities
	responses := []string{
		fmt.Sprintf("250-%s", s.config.LMTP.Hostname),
		"250-PIPELINING",
		"250-ENHANCEDSTATUSCODES",
		fmt.Sprintf("250-SIZE %d", s.config.LMTP.MaxSize),
		"250 8BITMIME",
	}

	for _, resp := range responses {
		if err := s.sendRawResponse(resp); err != nil {
			return err
		}
	}

	return nil
}

// handleMAIL handles the MAIL FROM command
func (s *Session) handleMAIL(args string) error {
	if s.helo == "" {
		return s.sendResponse(503, "Please send LHLO first")
	}

	if s.mailFrom != "" {
		return s.sendResponse(503, "Sender already specified")
	}

	// Parse MAIL FROM:<address>
	from, err := s.parseMailFrom(args)
	if err != nil {
		return s.sendResponse(501, "Invalid MAIL FROM syntax: %v", err)
	}

	s.mailFrom = from
	return s.sendResponse(250, "2.1.0 Sender OK")
}

// handleRCPT handles the RCPT TO command
func (s *Session) handleRCPT(args string) error {
	if s.mailFrom == "" {
		return s.sendResponse(503, "Please send MAIL FROM first")
	}

	if len(s.recipients) >= s.config.LMTP.MaxRecipients {
		return s.sendResponse(452, "Too many recipients")
	}

	// Parse RCPT TO:<address>
	to, err := s.parseRcptTo(args)
	if err != nil {
		return s.sendResponse(501, "Invalid RCPT TO syntax: %v", err)
	}

	// Validate recipient domain if configured
	if len(s.config.Delivery.AllowedDomains) > 0 {
		domain, err := parser.ExtractDomain(to)
		if err != nil {
			return s.sendResponse(550, "5.1.1 Invalid recipient address")
		}

		allowed := false
		for _, allowedDomain := range s.config.Delivery.AllowedDomains {
			if domain == allowedDomain {
				allowed = true
				break
			}
		}

		if !allowed {
			return s.sendResponse(550, "5.7.1 Relay not permitted")
		}
	}

	// Check if this is a group email and resolve members
	resolvedRecipients, err := s.resolveGroupIfNeeded(to)
	if err != nil {
		// Log the error but still accept the recipient for backwards compatibility
		log.Printf("Warning: failed to resolve group email %s: %v", to, err)
		resolvedRecipients = []string{to}
	}

	// Add resolved recipients (could be the original address or group members)
	for _, recipient := range resolvedRecipients {
		if len(s.recipients) >= s.config.LMTP.MaxRecipients {
			return s.sendResponse(452, "Too many recipients")
		}

		mailboxIdentity, resolveErr := s.resolveMailboxIdentity(recipient)
		if resolveErr != nil {
			log.Printf("Warning: failed to resolve mailbox identity for %s: %v", recipient, resolveErr)
			mailboxIdentity = recipient
		}

		s.recipients = append(s.recipients, recipient)
		s.recipientMap[recipient] = mailboxIdentity
	}

	return s.sendResponse(250, "2.1.5 Recipient OK")
}

// resolveGroupIfNeeded checks if the recipient is a group email and resolves members
// Returns the list of recipients to deliver to (either original or resolved group members)
func (s *Session) resolveGroupIfNeeded(recipient string) ([]string, error) {
	// Check if this looks like a group email (ends with -group@domain)
	if !isGroupEmail(recipient) {
		// Not a group, return the original recipient
		return []string{recipient}, nil
	}

	// It's a group email, resolve members
	if s.groupResolver == nil {
		// Group resolver not configured, return error
		return nil, fmt.Errorf("group resolver not configured for group email resolution")
	}

	groupName, err := parseGroupEmail(recipient)
	if err != nil {
		return nil, fmt.Errorf("failed to parse group email: %w", err)
	}
	_, groupDomain, err := parseEmail(recipient)
	if err != nil {
		return nil, fmt.Errorf("failed to parse group email: %w", err)
	}

	log.Printf("Resolving group email: %s (group=%s)", recipient, groupName)

	members, err := s.groupResolver.ResolveGroupMembers(groupName, groupDomain)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve group members: %w", err)
	}

	if len(members) == 0 {
		return nil, fmt.Errorf("group '%s' has no members", groupName)
	}

	log.Printf("Group email %s resolved to %d members: %v", recipient, len(members), members)
	return members, nil
}

// isGroupEmail checks if an email address is a group email (ends with -group@domain)
func isGroupEmail(email string) bool {
	// Group email format: <group_name>-group@<domain>
	localPart, _, err := parseEmail(email)
	if err != nil {
		return false
	}
	return strings.HasSuffix(localPart, "-group")
}

// parseGroupEmail parses a group email address and returns the group name.
func parseGroupEmail(email string) (groupName string, err error) {
	localPart, _, err := parseEmail(email)
	if err != nil {
		return "", err
	}

	if !strings.HasSuffix(localPart, "-group") {
		return "", fmt.Errorf("not a group email address: %s", email)
	}

	// Remove the "-group" suffix to get the actual group name
	groupName = strings.TrimSuffix(localPart, "-group")
	if groupName == "" {
		return "", fmt.Errorf("empty group name in address: %s", email)
	}

	return groupName, nil
}

// parseEmail parses an email address into local and domain parts
func parseEmail(email string) (localPart, domain string, err error) {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid email address: %s", email)
	}
	return parts[0], parts[1], nil
}

// handleDATA handles the DATA command
func (s *Session) handleDATA() error {
	if s.mailFrom == "" {
		return s.sendResponse(503, "Please send MAIL FROM first")
	}

	if len(s.recipients) == 0 {
		return s.sendResponse(503, "Please send RCPT TO first")
	}

	// Send intermediate response
	if err := s.sendResponse(354, "Start mail input; end with <CRLF>.<CRLF>"); err != nil {
		return err
	}

	// Read message data
	data, err := parser.ReadDataCommand(s.reader, s.config.LMTP.MaxSize)
	if err != nil {
		log.Printf("Error reading message data: %v", err)
		return s.sendResponse(554, "Error reading message: %v", err)
	}

	// Parse message
	msg, err := parser.ParseMessage(bytes.NewReader(data))
	if err != nil {
		log.Printf("Error parsing message: %v", err)
		return s.sendResponse(554, "Error parsing message: %v", err)
	}

	// Validate message
	if err := parser.ValidateMessage(msg, s.config.LMTP.MaxSize); err != nil {
		log.Printf("Message validation failed: %v", err)
		return s.sendResponse(554, "Message validation failed: %v", err)
	}

	// Check quota for each recipient (if enabled)
	if s.config.Delivery.QuotaEnabled {
		for _, recipient := range s.recipients {
			username, err := parser.ExtractLocalPart(recipient)
			if err != nil {
				continue
			}

			if err := s.storage.CheckQuota(username, msg.Size, s.config.Delivery.QuotaLimit); err != nil {
				log.Printf("Quota check failed for %s: %v", recipient, err)
				// Continue with other recipients
			}
		}
	}

	// Deliver to each envelope recipient (LMTP requires per-recipient response)
	folder := s.config.Delivery.DefaultFolder
	results := make(map[string]error, len(s.recipients))
	deliveredTargets := make(map[string]error, len(s.recipients))
	for _, recipient := range s.recipients {
		targetRecipient := recipient
		if mappedRecipient, ok := s.recipientMap[recipient]; ok && mappedRecipient != "" {
			targetRecipient = mappedRecipient
		}

		if err, alreadyDelivered := deliveredTargets[targetRecipient]; alreadyDelivered {
			results[recipient] = err
			continue
		}

		err := s.storage.DeliverMessage(targetRecipient, msg, folder)
		deliveredTargets[targetRecipient] = err
		results[recipient] = err
	}

	// Send per-recipient responses
	for _, recipient := range s.recipients {
		if err := results[recipient]; err != nil {
			log.Printf("Delivery failed for %s: %v", recipient, err)
			_ = s.sendResponse(550, "5.3.0 Delivery failed for <%s>: %v", recipient, err)
		} else {
			log.Printf("Message delivered successfully to %s", recipient)
			_ = s.sendResponse(250, "2.0.0 Message accepted for delivery to <%s>", recipient)
		}
	}

	// Reset session state
	s.mailFrom = ""
	s.recipients = make([]string, 0)
	s.recipientMap = make(map[string]string)

	return nil
}

// handleRSET handles the RSET command
func (s *Session) handleRSET() error {
	s.mailFrom = ""
	s.recipients = make([]string, 0)
	s.recipientMap = make(map[string]string)
	return s.sendResponse(250, "Reset state")
}

func (s *Session) resolveMailboxIdentity(recipient string) (string, error) {
	if s.identityRes == nil {
		return recipient, nil
	}

	identity, err := s.identityRes.Resolve(recipient)
	if err != nil {
		return "", err
	}
	if identity == "" {
		return recipient, nil
	}

	return identity, nil
}

// handleNOOP handles the NOOP command
func (s *Session) handleNOOP() error {
	return s.sendResponse(250, "OK")
}

// handleQUIT handles the QUIT command
func (s *Session) handleQUIT() error {
	_ = s.sendResponse(221, "Bye")
	return fmt.Errorf("QUIT")
}

// handleVRFY handles the VRFY command
func (s *Session) handleVRFY(args string) error {
	// VRFY is typically disabled for security reasons
	return s.sendResponse(252, "Cannot VRFY user, but will accept message")
}

// handleHELP handles the HELP command
func (s *Session) handleHELP() error {
	return s.sendResponse(214, "Commands: LHLO MAIL RCPT DATA RSET NOOP QUIT")
}

// parseMailFrom parses the MAIL FROM command arguments
func (s *Session) parseMailFrom(args string) (string, error) {
	// Expected format: FROM:<address> or FROM: <address>
	args = strings.TrimSpace(args)

	if !strings.HasPrefix(strings.ToUpper(args), "FROM:") {
		return "", fmt.Errorf("expected FROM")
	}

	args = strings.TrimPrefix(args, "FROM:")
	args = strings.TrimPrefix(args, "from:")
	args = strings.TrimSpace(args)

	// Remove angle brackets if present
	args = strings.TrimPrefix(args, "<")
	args = strings.TrimSuffix(args, ">")

	// Handle SIZE parameter and other ESMTP parameters
	parts := strings.Fields(args)
	if len(parts) > 0 {
		return parts[0], nil
	}

	return args, nil
}

// parseRcptTo parses the RCPT TO command arguments
func (s *Session) parseRcptTo(args string) (string, error) {
	// Expected format: TO:<address> or TO: <address>
	args = strings.TrimSpace(args)

	if !strings.HasPrefix(strings.ToUpper(args), "TO:") {
		return "", fmt.Errorf("expected TO")
	}

	args = strings.TrimPrefix(args, "TO:")
	args = strings.TrimPrefix(args, "to:")
	args = strings.TrimSpace(args)

	// Remove angle brackets if present
	args = strings.TrimPrefix(args, "<")
	args = strings.TrimSuffix(args, ">")

	return args, nil
}

// sendResponse sends a formatted response
func (s *Session) sendResponse(code int, format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	response := fmt.Sprintf("%d %s\r\n", code, message)
	return s.sendRawResponse(response)
}

// sendRawResponse sends a raw response
func (s *Session) sendRawResponse(response string) error {
	if !strings.HasSuffix(response, "\r\n") {
		response += "\r\n"
	}

	log.Printf("S: %s", strings.TrimSpace(response))

	_, err := s.writer.WriteString(response)
	if err != nil {
		return err
	}

	return s.writer.Flush()
}
