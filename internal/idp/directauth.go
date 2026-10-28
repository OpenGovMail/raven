package idp

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// directAuthRequiredCode is the error code Thunder answers with when a Direct API call arrives
// without a valid Direct-Auth-Secret header. Thunder returns it before looking at the end user's
// credentials, so it says nothing about whether their password is right.
const directAuthRequiredCode = "AUTH-4010"

// DirectAuthSecret returns the shared secret that Thunder's Direct API requires, or an empty
// string when none is configured. Thunder rejects those endpoints with 401 when the secret is
// missing or wrong.
//
// IDP_DIRECT_AUTH_SECRET takes precedence. Otherwise the secret is read from the file named by
// IDP_DIRECT_AUTH_SECRET_FILE, which lets a deployment mount the same secret file Thunder uses.
// The file is read on every call so that a rotated secret is picked up without a restart.
func DirectAuthSecret() string {
	if secret := strings.TrimSpace(os.Getenv("IDP_DIRECT_AUTH_SECRET")); secret != "" {
		return secret
	}

	path := strings.TrimSpace(os.Getenv("IDP_DIRECT_AUTH_SECRET_FILE"))
	if path == "" {
		return ""
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		log.Printf("WARNING: could not read IDP_DIRECT_AUTH_SECRET_FILE: %v", err)
		return ""
	}

	return strings.TrimSpace(string(data))
}

// WarnIfDirectAuthSecretMissing logs a warning when no Direct API secret is configured. Without
// it Thunder rejects every password check, and every user appears to have entered a wrong
// password, so the misconfiguration is otherwise easy to miss.
func WarnIfDirectAuthSecretMissing() {
	if DirectAuthSecret() != "" {
		return
	}

	log.Println("WARNING: neither IDP_DIRECT_AUTH_SECRET nor IDP_DIRECT_AUTH_SECRET_FILE provides a secret;" +
		" Thunder will reject every password login with 401 " + directAuthRequiredCode)
}

// IsDirectAuthRejection reports whether a Direct API response means Thunder refused the call
// itself, because the Direct-Auth-Secret header was missing or wrong, rather than rejecting the
// end user's credentials.
func IsDirectAuthRejection(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}

	var errResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		return false
	}

	return errResp.Code == directAuthRequiredCode
}
