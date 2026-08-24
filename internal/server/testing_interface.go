package server

import (
	"net"

	"raven/internal/models"
	"raven/internal/server/auth"
	"raven/internal/server/extension"
	"raven/internal/server/mailbox"
	"raven/internal/server/message"
	"raven/internal/server/selection"
	"raven/internal/server/uid"
)

// TestInterface provides access to internal methods for testing
// This interface should only be used in tests
type TestInterface struct {
	server *IMAPServer
}

// NewTestInterface creates a new test interface wrapper
// This function should only be used in tests
func NewTestInterface(server *IMAPServer) *TestInterface {
	return &TestInterface{server: server}
}

// HandleCapability exposes the capability handler for testing
func (t *TestInterface) HandleCapability(conn net.Conn, tag string, state *models.ClientState) {
	auth.HandleCapability(t.server, conn, tag, []string{tag, "CAPABILITY"}, state)
}

// HandleLogin exposes the login handler for testing
func (t *TestInterface) HandleLogin(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	auth.HandleLogin(t.server, conn, tag, parts, state)
}

// HandleList exposes the list handler for testing
func (t *TestInterface) HandleList(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleList(t.server, conn, tag, parts, state)
}

// HandleCreate exposes the create handler for testing
func (t *TestInterface) HandleCreate(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleCreate(t.server, conn, tag, parts, state)
}

// HandleDelete exposes the delete handler for testing
func (t *TestInterface) HandleDelete(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleDelete(t.server, conn, tag, parts, state)
}

// HandleRename exposes the rename handler for testing
func (t *TestInterface) HandleRename(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleRename(t.server, conn, tag, parts, state)
}

// HandleLogout exposes the logout handler for testing
func (t *TestInterface) HandleLogout(conn net.Conn, tag string) {
	auth.HandleLogout(t.server, conn, tag)
}

// HandleIdle exposes the idle handler for testing
func (t *TestInterface) HandleIdle(conn net.Conn, tag string, state *models.ClientState) {
	extension.HandleIdle(t.server, conn, tag, state)
}

// HandleNamespace exposes the namespace handler for testing
func (t *TestInterface) HandleNamespace(conn net.Conn, tag string, state *models.ClientState) {
	extension.HandleNamespace(t.server, conn, tag, state)
}

// HandleUnselect exposes the unselect handler for testing
func (t *TestInterface) HandleUnselect(conn net.Conn, tag string, state *models.ClientState) {
	selection.HandleUnselect(t.server, conn, tag, state)
}

// HandleNoop exposes the noop handler for testing
func (t *TestInterface) HandleNoop(conn net.Conn, tag string, state *models.ClientState) {
	extension.HandleNoop(t.server, conn, tag, state)
}

// HandleCheck exposes the check handler for testing
func (t *TestInterface) HandleCheck(conn net.Conn, tag string, state *models.ClientState) {
	message.HandleCheck(t.server, conn, tag, state)
}

// HandleClose exposes the close handler for testing
func (t *TestInterface) HandleClose(conn net.Conn, tag string, state *models.ClientState) {
	selection.HandleClose(t.server, conn, tag, state)
}

// HandleExpunge exposes the expunge handler for testing
func (t *TestInterface) HandleExpunge(conn net.Conn, tag string, state *models.ClientState) {
	message.HandleExpunge(t.server, conn, tag, state)
}

// HandleSelect exposes the select handler for testing
func (t *TestInterface) HandleSelect(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	selection.HandleSelect(t.server, conn, tag, parts, state)
}

// HandleExamine exposes the examine handler for testing (uses same handler as SELECT)
func (t *TestInterface) HandleExamine(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	selection.HandleSelect(t.server, conn, tag, parts, state)
}

// HandleAppend exposes the append handler for testing
func (t *TestInterface) HandleAppend(conn net.Conn, tag string, parts []string, fullLine string, state *models.ClientState) {
	message.HandleAppend(t.server, conn, tag, parts, fullLine, state)
}

// HandleAuthenticate exposes the authenticate handler for testing
func (t *TestInterface) HandleAuthenticate(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	auth.HandleAuthenticate(t.server, conn, tag, parts, state)
}

// HandleStartTLS exposes the STARTTLS handler for testing
func (t *TestInterface) HandleStartTLS(conn net.Conn, tag string, parts []string) {
	clientHandler := func(conn net.Conn, state *models.ClientState) {
		handleClient(t.server, conn, state)
	}
	auth.HandleStartTLS(t.server, clientHandler, conn, tag, parts)
}

// HandleSubscribe exposes the subscribe handler for testing
func (t *TestInterface) HandleSubscribe(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleSubscribe(t.server, conn, tag, parts, state)
}

// HandleUnsubscribe exposes the unsubscribe handler for testing
func (t *TestInterface) HandleUnsubscribe(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleUnsubscribe(t.server, conn, tag, parts, state)
}

// HandleLsub exposes the lsub handler for testing
func (t *TestInterface) HandleLsub(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleLsub(t.server, conn, tag, parts, state)
}

// HandleStatus exposes the status handler for testing
func (t *TestInterface) HandleStatus(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	mailbox.HandleStatus(t.server, conn, tag, parts, state)
}

// HandleSearch exposes the search handler for testing
func (t *TestInterface) HandleSearch(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	message.HandleSearch(t.server, conn, tag, parts, state)
}

// HandleFetch exposes the fetch handler for testing
func (t *TestInterface) HandleFetch(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	message.HandleFetch(t.server, conn, tag, parts, state)
}

// HandleStore exposes the store handler for testing
func (t *TestInterface) HandleStore(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	message.HandleStore(t.server, conn, tag, parts, state)
}

// HandleCopy exposes the copy handler for testing
func (t *TestInterface) HandleCopy(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	message.HandleCopy(t.server, conn, tag, parts, state)
}

// HandleUID exposes the UID handler for testing
func (t *TestInterface) HandleUID(conn net.Conn, tag string, parts []string, state *models.ClientState) {
	uid.HandleUID(t.server, conn, tag, parts, state)
}

// SetTLSCertificates sets custom TLS certificate paths for testing
func (t *TestInterface) SetTLSCertificates(certPath, keyPath string) {
	t.server.SetTLSCertificates(certPath, keyPath)
}

// GetDBManager exposes the database manager for testing
func (t *TestInterface) GetDBManager() interface{} {
	return t.server.dbManager
}

// GetDB provides backward compatibility - returns the DBManager
// Note: Tests should migrate to using GetDBManager() and per-user databases
func (t *TestInterface) GetDB() interface{} {
	return t.server.dbManager
}

// SendResponse exposes the sendResponse method for testing
func (t *TestInterface) SendResponse(conn net.Conn, response string) {
	t.server.sendResponse(conn, response)
}

// HandleClientExported exposes handleClient for testing
func HandleClientExported(server *TestInterface, conn net.Conn) {
	handleClient(server.server, conn, &models.ClientState{})
}

// GetServer returns the underlying IMAPServer for compatibility
func (t *TestInterface) GetServer() *IMAPServer {
	return t.server
}
