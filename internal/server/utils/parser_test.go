package utils

import (
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestParseQuotedString_QuotedString(t *testing.T) {
	input := `"hello world"`
	expected := "hello world"
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestParseQuotedString_UnquotedString(t *testing.T) {
	input := "hello"
	expected := "hello"
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestParseQuotedString_EmptyQuoted(t *testing.T) {
	input := `""`
	expected := ""
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected empty string, got '%s'", result)
	}
}

func TestParseQuotedString_EmptyString(t *testing.T) {
	input := ""
	expected := ""
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected empty string, got '%s'", result)
	}
}

func TestParseQuotedString_SingleQuote(t *testing.T) {
	input := `"`
	expected := `"`
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestParseQuotedString_QuotedWithSpaces(t *testing.T) {
	input := `"hello   world"`
	expected := "hello   world"
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestParseQuotedString_QuotedWithSpecialChars(t *testing.T) {
	input := `"hello@world.com"`
	expected := "hello@world.com"
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestParseQuotedString_MismatchedQuotes(t *testing.T) {
	input := `"hello`
	// Should return as-is since not properly quoted
	expected := `"hello`
	result := ParseQuotedString(input)

	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestContains_Found(t *testing.T) {
	slice := []string{"apple", "banana", "cherry"}
	item := "banana"

	if !Contains(slice, item) {
		t.Error("Expected to find 'banana' in slice")
	}
}

func TestContains_NotFound(t *testing.T) {
	slice := []string{"apple", "banana", "cherry"}
	item := "orange"

	if Contains(slice, item) {
		t.Error("Expected not to find 'orange' in slice")
	}
}

func TestContains_EmptySlice(t *testing.T) {
	slice := []string{}
	item := "apple"

	if Contains(slice, item) {
		t.Error("Expected not to find item in empty slice")
	}
}

func TestContains_EmptyString(t *testing.T) {
	slice := []string{"apple", "", "cherry"}
	item := ""

	if !Contains(slice, item) {
		t.Error("Expected to find empty string in slice")
	}
}

func TestContains_CaseSensitive(t *testing.T) {
	slice := []string{"Apple", "Banana"}
	item := "apple"

	if Contains(slice, item) {
		t.Error("Contains should be case-sensitive")
	}
}

// Helper function to create an in-memory test database
func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Create necessary tables
	_, err = db.Exec(`
		CREATE TABLE mailboxes (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL
		);
		CREATE TABLE messages (
			id INTEGER PRIMARY KEY,
			mailbox_id INTEGER
		);
		CREATE TABLE message_mailbox (
			message_id INTEGER,
			mailbox_id INTEGER,
			uid INTEGER,
			sequence_number INTEGER
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create test tables: %v", err)
	}

	return db
}

func TestParseSequenceSetWithDB_Single(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	// Insert test data
	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, sequence_number) VALUES (?, 1, ?)", i, i)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	sequences := ParseSequenceSetWithDB("3", mailboxID, db)

	if len(sequences) != 1 {
		t.Errorf("Expected 1 sequence, got %d", len(sequences))
	}
	if len(sequences) > 0 && sequences[0] != 3 {
		t.Errorf("Expected sequence 3, got %d", sequences[0])
	}
}

func TestParseSequenceSetWithDB_Range(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, sequence_number) VALUES (?, 1, ?)", i, i)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	sequences := ParseSequenceSetWithDB("2:4", mailboxID, db)

	if len(sequences) != 3 {
		t.Errorf("Expected 3 sequences, got %d", len(sequences))
	}
	expectedSeqs := []int{2, 3, 4}
	for i, expected := range expectedSeqs {
		if i >= len(sequences) || sequences[i] != expected {
			t.Errorf("Expected sequence %d at position %d, got %d", expected, i, sequences[i])
		}
	}
}

func TestParseSequenceSetWithDB_Star(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, sequence_number) VALUES (?, 1, ?)", i, i)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	sequences := ParseSequenceSetWithDB("*", mailboxID, db)

	if len(sequences) != 1 {
		t.Errorf("Expected 1 sequence, got %d", len(sequences))
	}
	if len(sequences) > 0 && sequences[0] != 5 {
		t.Errorf("Expected last sequence (5), got %d", sequences[0])
	}
}

func TestParseSequenceSetWithDB_Multiple(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, sequence_number) VALUES (?, 1, ?)", i, i)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	sequences := ParseSequenceSetWithDB("1,3,5", mailboxID, db)

	if len(sequences) != 3 {
		t.Errorf("Expected 3 sequences, got %d", len(sequences))
	}
	expectedSeqs := []int{1, 3, 5}
	for i, expected := range expectedSeqs {
		if i >= len(sequences) || sequences[i] != expected {
			t.Errorf("Expected sequence %d at position %d, got %v", expected, i, sequences[i])
		}
	}
}

func TestParseSequenceSetWithDB_EmptyMailbox(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)
	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	sequences := ParseSequenceSetWithDB("1:5", mailboxID, db)

	if len(sequences) != 0 {
		t.Errorf("Expected 0 sequences for empty mailbox, got %d", len(sequences))
	}
}

func TestParseSequenceSetWithDB_ReverseRange(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, sequence_number) VALUES (?, 1, ?)", i, i)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	sequences := ParseSequenceSetWithDB("4:2", mailboxID, db)

	// Should normalize to 2:4
	if len(sequences) != 3 {
		t.Errorf("Expected 3 sequences for reversed range, got %d", len(sequences))
	}
}

func TestParseUIDSequenceSetWithDB_Single(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	uids := []int{100, 200, 300}
	for i, uid := range uids {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i+1)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, uid) VALUES (?, 1, ?)", i+1, uid)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	result := ParseUIDSequenceSetWithDB("200", mailboxID, db)

	if len(result) != 1 {
		t.Errorf("Expected 1 UID, got %d", len(result))
	}
	if len(result) > 0 && result[0] != 200 {
		t.Errorf("Expected UID 200, got %d", result[0])
	}
}

func TestParseUIDSequenceSetWithDB_Range(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	uids := []int{100, 200, 300, 400}
	for i, uid := range uids {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i+1)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, uid) VALUES (?, 1, ?)", i+1, uid)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	result := ParseUIDSequenceSetWithDB("200:400", mailboxID, db)

	if len(result) != 3 {
		t.Errorf("Expected 3 UIDs, got %d: %v", len(result), result)
	}
}

func TestParseUIDSequenceSetWithDB_Star(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	uids := []int{100, 200, 300}
	for i, uid := range uids {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i+1)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, uid) VALUES (?, 1, ?)", i+1, uid)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	result := ParseUIDSequenceSetWithDB("*", mailboxID, db)

	if len(result) != 1 {
		t.Errorf("Expected 1 UID, got %d", len(result))
	}
	if len(result) > 0 && result[0] != 300 {
		t.Errorf("Expected max UID (300), got %d", result[0])
	}
}

func TestParseUIDSequenceSetWithDB_RangeWithStar(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	uids := []int{100, 200, 300, 400, 500}
	for i, uid := range uids {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i+1)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, uid) VALUES (?, 1, ?)", i+1, uid)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	result := ParseUIDSequenceSetWithDB("300:*", mailboxID, db)

	if len(result) != 3 {
		t.Errorf("Expected 3 UIDs (300, 400, 500), got %d: %v", len(result), result)
	}
}

func TestParseUIDSequenceSetWithDB_EmptyMailbox(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)
	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	result := ParseUIDSequenceSetWithDB("100:200", mailboxID, db)

	if len(result) != 0 {
		t.Errorf("Expected 0 UIDs for empty mailbox, got %d", len(result))
	}
}

func TestParseUIDSequenceSetWithDB_NonExistentUID(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (1, 1)")
	if err != nil {
		t.Fatalf("Failed to insert message: %v", err)
	}
	_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, uid) VALUES (1, 1, 100)")
	if err != nil {
		t.Fatalf("Failed to insert message_mailbox: %v", err)
	}

	result := ParseUIDSequenceSetWithDB("999", mailboxID, db)

	if len(result) != 0 {
		t.Errorf("Expected 0 UIDs for non-existent UID, got %d", len(result))
	}
}

func TestParseUIDSequenceSetWithDB_MultipleSequences(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	mailboxID := int64(1)

	_, err := db.Exec("INSERT INTO mailboxes (id, name) VALUES (1, 'INBOX')")
	if err != nil {
		t.Fatalf("Failed to insert mailbox: %v", err)
	}

	uids := []int{100, 200, 300, 400}
	for i, uid := range uids {
		_, err = db.Exec("INSERT INTO messages (id, mailbox_id) VALUES (?, 1)", i+1)
		if err != nil {
			t.Fatalf("Failed to insert message: %v", err)
		}
		_, err = db.Exec("INSERT INTO message_mailbox (message_id, mailbox_id, uid) VALUES (?, 1, ?)", i+1, uid)
		if err != nil {
			t.Fatalf("Failed to insert message_mailbox: %v", err)
		}
	}

	result := ParseUIDSequenceSetWithDB("100,300", mailboxID, db)

	if len(result) != 2 {
		t.Errorf("Expected 2 UIDs, got %d: %v", len(result), result)
	}
}

func TestParseSequenceSet(t *testing.T) {
	cases := []struct {
		set   string
		total int
		want  []int
	}{
		{"*", 3, []int{3}},
		{"1,3:*", 4, []int{1, 3, 4}},
		{"2:5", 3, []int{2, 3}},
		{"1:*", 0, nil},
		// Input order matters: STORE looks positions up again after each Junk move.
		{"3,1", 3, []int{3, 1}},
	}
	for _, c := range cases {
		got := ParseSequenceSet(c.set, c.total)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("ParseSequenceSet(%q, %d) = %v, want %v", c.set, c.total, got, c.want)
		}
	}
}
