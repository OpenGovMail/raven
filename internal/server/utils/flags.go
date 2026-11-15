package utils

import (
	"strings"
)

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

// GetMailboxAttributes returns the appropriate attributes for a mailbox
func GetMailboxAttributes(mailboxName string) string {
	switch mailboxName {
	case "Drafts":
		return "\\Drafts"
	case "Trash":
		return "\\Trash"
	case "Sent":
		return "\\Sent"
	case "Spam":
		return "\\Junk"
	case "INBOX":
		return "\\Unmarked"
	default:
		return "\\Unmarked"
	}
}
