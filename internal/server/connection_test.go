package server

import (
	"bufio"
	"strings"
	"testing"

	"raven/internal/models"
	"raven/internal/server/auth"
)

func TestSanitizeCommandForLoggingHidesCredentials(t *testing.T) {
	tests := []struct{ line, want string }{
		{`a1 LOGIN alice@example.com "s3cret"`, "a1 LOGIN [CREDENTIALS OMITTED]"},
		{"a2 login alice s3cret", "a2 login [CREDENTIALS OMITTED]"},
		{"a3 AUTHENTICATE PLAIN AGFsaWNlAHMzY3JldA==", "a3 AUTHENTICATE [CREDENTIALS OMITTED]"},
		{"a4 AUTHENTICATE AGFsaWNlAHMzY3JldA==", "a4 AUTHENTICATE [CREDENTIALS OMITTED]"},
		{"a5 SELECT INBOX", "a5 SELECT INBOX"},
	}

	for _, tt := range tests {
		if got := sanitizeCommandForLogging(strings.Fields(tt.line), tt.line); got != tt.want {
			t.Errorf("sanitizeCommandForLogging(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// chunkedConn hands out at most max bytes per Read, like a reply that arrives in pieces.
type chunkedConn struct {
	*MockTLSConn
	max int
}

func (c *chunkedConn) Read(p []byte) (int, error) {
	return c.MockTLSConn.Read(p[:min(len(p), c.max)])
}

func TestAuthenticateReadsTheWholeReply(t *testing.T) {
	s, cleanup := SetupTestServer(t)
	defer cleanup()

	tests := []struct {
		name string
		max  int    // bytes per Read
		sent string // sent by the client before the reply, in the same write
	}{
		{"split across reads", 100, ""},
		{"already buffered with the command", 1 << 20, "A1 AUTHENTICATE PLAIN\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &chunkedConn{MockTLSConn: NewMockTLSConn(), max: tt.max}
			conn.AddReadData(tt.sent + strings.Repeat("A", 3000) + "\r\nA2 NOOP\r\n")
			reader := bufio.NewReader(conn)
			if tt.sent != "" {
				_, _ = reader.ReadString('\n') // the command loop's read, which buffers the reply too
			}

			auth.HandleAuthenticate(s.server, reader, conn, "A1", []string{"A1", "AUTHENTICATE", "PLAIN"}, &models.ClientState{})

			if next, _ := reader.ReadString('\n'); strings.TrimSpace(next) != "A2 NOOP" {
				t.Fatalf("next command = %.40q, want %q: part of the reply was left to be read as a command", next, "A2 NOOP")
			}
		})
	}
}

func TestAuthenticateClosesTheConnectionOnAHalfSentReply(t *testing.T) {
	s, cleanup := SetupTestServer(t)
	defer cleanup()

	conn := NewMockTLSConn()
	conn.AddReadData("AAAA") // no line end: the read fails partway through the reply

	auth.HandleAuthenticate(s.server, bufio.NewReader(conn), conn, "A1", []string{"A1", "AUTHENTICATE", "PLAIN"}, &models.ClientState{})

	if !conn.closed {
		t.Fatal("connection still open after a half-sent reply: the rest would be read as a command")
	}
}
