// Package maildomain reads the mail domain an organization unit owns from its handle.
//
// The handle is the whole domain: its depth and its relation to the parent OU's handle
// carry no meaning. An OU whose handle is not a domain owns no mail domain.
package maildomain

import "strings"

// FromHandle returns the mail domain declared by an OU handle, lowercased and without a
// trailing dot. It reports false when the handle is not a domain.
func FromHandle(handle string) (string, bool) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(handle)), ".")

	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, label := range labels {
		if !validLabel(label) {
			return "", false
		}
	}

	return domain, true
}

// validLabel also rejects "/" and "\", which keeps the domain safe to put in a URL path.
func validLabel(label string) bool {
	if label == "" {
		return false
	}
	for _, c := range label {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
