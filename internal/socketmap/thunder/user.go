package thunder

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// escapeFilterValue escapes special characters in filter values to prevent injection attacks
func escapeFilterValue(value string) string {
	// Escape backslashes first, then double quotes
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}

// ValidateUser checks if a user exists in Thunder IDP
func ValidateUser(email, host, port string, tokenRefreshSeconds int) (bool, error) {
	log.Printf("      ┌─ Thunder User Validation ─────")
	log.Printf("      │ Email: %s", email)

	// Parse email to get username and domain
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		log.Printf("      │ ✗ Invalid email format")
		log.Printf("      └──────────────────────────────")
		return false, nil
	}

	username := parts[0]
	domain := parts[1]

	log.Printf("      │ Username: %s", username)
	log.Printf("      │ Domain: %s", domain)

	// Get authentication token
	auth, err := GetAuth(host, port, tokenRefreshSeconds)
	if err != nil {
		log.Printf("      │ ⚠ Auth failed: %v", err)
		log.Printf("      └──────────────────────────────")
		return false, err
	}

	// Get the OU ID for the domain
	ouID, err := GetOrgUnitIDForDomain(domain)
	if err != nil {
		log.Printf("      │ ⚠ Failed to get OU ID: %v", err)
		log.Printf("      └──────────────────────────────")
		return false, err
	}

	log.Printf("      │ OU ID: %s", ouID)

	// Query Thunder Users API with filter
	client := GetHTTPClient()
	// Escape username to prevent filter injection attacks
	escapedUsername := escapeFilterValue(username)
	filter := fmt.Sprintf("username eq \"%s\"", escapedUsername)

	baseURL := fmt.Sprintf("https://%s:%s/users", host, port)

	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		log.Printf("      │ ✗ Failed to create request: %v", err)
		log.Printf("      └──────────────────────────────")
		return false, err
	}

	// Add the filter as a query parameter (will be automatically URL encoded)
	q := req.URL.Query()
	q.Add("filter", filter)
	req.URL.RawQuery = q.Encode()

	log.Printf("      │ Query: %s", req.URL.String())

	req.Header.Set("Authorization", "Bearer "+auth.BearerToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("      │ ✗ Request failed: %v", err)
		log.Printf("      └──────────────────────────────")
		return false, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("      │ ⚠ Failed to close user response body: %v", err)
		}
	}()

	if resp.StatusCode != 200 {
		log.Printf("      │ ⚠ Unexpected status: %d", resp.StatusCode)
		log.Printf("      └──────────────────────────────")
		return false, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var usersResp UsersResponse
	if err := json.NewDecoder(resp.Body).Decode(&usersResp); err != nil {
		log.Printf("      │ ✗ Failed to parse response: %v", err)
		log.Printf("      └──────────────────────────────")
		return false, err
	}

	log.Printf("      │ Total results: %d", usersResp.TotalResults)

	if usersResp.TotalResults == 0 {
		log.Printf("      │ ✗ User not found in Thunder")
		log.Printf("      └──────────────────────────────")
		return false, nil
	}

	// Validate that the user belongs to the correct OU
	for _, user := range usersResp.Users {
		log.Printf("      │ Found user ID: %s", user.ID)
		log.Printf("      │ User OU: %s", user.OrganizationUnit)

		if user.OrganizationUnit == ouID {
			log.Printf("      │ ✓ User found and OU matches!")
			log.Printf("      └──────────────────────────────")
			return true, nil
		} else {
			log.Printf("      │ ⚠ OU mismatch (expected: %s, got: %s)", ouID, user.OrganizationUnit)
		}
	}

	log.Printf("      │ ✗ User found but OU doesn't match")
	log.Printf("      └──────────────────────────────")
	return false, nil
}
