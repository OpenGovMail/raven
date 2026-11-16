package thunder

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// ValidateGroupAddress checks if an address matches the group pattern and exists in Thunder IDP.
// Expected format: <group-name>-group@<domain>
func ValidateGroupAddress(email, host, port string, tokenRefreshSeconds int) (bool, error) {
	log.Printf("      ┌─ Thunder Group Validation ────")
	log.Printf("      │ Email: %s", email)
	defer log.Printf("      └──────────────────────────────")

	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		log.Printf("      │ ✗ Invalid email format")
		return false, nil
	}

	localPart := parts[0]
	domain := parts[1]

	if !strings.HasSuffix(localPart, "-group") {
		log.Printf("      │ ✗ Not a group address")
		return false, nil
	}

	groupName := strings.TrimSuffix(localPart, "-group")
	if groupName == "" {
		log.Printf("      │ ✗ Empty group name")
		return false, nil
	}

	log.Printf("      │ Group: %s", groupName)
	log.Printf("      │ Domain: %s", domain)

	auth, err := GetAuth(host, port, tokenRefreshSeconds)
	if err != nil {
		log.Printf("      │ ⚠ Auth failed: %v", err)
		return false, err
	}

	ouID, err := GetOrgUnitIDForDomain(domain)
	if err != nil {
		log.Printf("      │ ⚠ Failed to get OU ID: %v", err)
		return false, err
	}

	log.Printf("      │ OU ID: %s", ouID)

	client := GetHTTPClient()
	baseURL := fmt.Sprintf("https://%s:%s/groups", host, port)
	req, err := http.NewRequest("GET", baseURL, nil)
	if err != nil {
		log.Printf("      │ ✗ Failed to create request: %v", err)
		return false, err
	}

	req.Header.Set("Authorization", "Bearer "+auth.BearerToken)
	req.Header.Set("Content-Type", "application/json")

	log.Printf("      │ Query: %s", req.URL.String())

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("      │ ✗ Request failed: %v", err)
		return false, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("      │ ⚠ Failed to close group response body: %v", err)
		}
	}()

	if resp.StatusCode != 200 {
		log.Printf("      │ ⚠ Unexpected status: %d", resp.StatusCode)
		return false, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var groupsResp GroupsResponse
	if err := json.NewDecoder(resp.Body).Decode(&groupsResp); err != nil {
		log.Printf("      │ ✗ Failed to parse response: %v", err)
		return false, err
	}

	log.Printf("      │ Total results: %d", groupsResp.TotalResults)

	if groupsResp.TotalResults == 0 {
		log.Printf("      │ ✗ Group not found in Thunder")
		return false, nil
	}

	for _, group := range groupsResp.Groups {
		if group.OrganizationUnitID == ouID && strings.EqualFold(group.Name, groupName) {
			log.Printf("      │ ✓ Group found and OU matches")
			return true, nil
		}
	}

	log.Printf("      │ ✗ Group found but OU/name mismatch")
	return false, nil
}
