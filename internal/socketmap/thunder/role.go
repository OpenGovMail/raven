package thunder

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// ValidateRoleAddress checks whether an email local-part matches a role in the same OU as the domain.
// If it matches, it returns a mailbox identity in role_<role-name>@<domain>.db format.
func ValidateRoleAddress(email, host, port string, tokenRefreshSeconds int) (bool, string, error) {
	log.Printf("      ┌─ Thunder Role Validation ─────")
	log.Printf("      │ Email: %s", email)
	defer log.Printf("      └──────────────────────────────")

	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		log.Printf("      │ ✗ Invalid email format")
		return false, "", nil
	}

	roleName := strings.TrimSpace(parts[0])
	domain := strings.TrimSpace(parts[1])
	if roleName == "" || domain == "" {
		log.Printf("      │ ✗ Empty role or domain")
		return false, "", nil
	}

	log.Printf("      │ Role candidate: %s", roleName)
	log.Printf("      │ Domain: %s", domain)

	auth, err := GetAuth(host, port, tokenRefreshSeconds)
	if err != nil {
		log.Printf("      │ ⚠ Auth failed: %v", err)
		return false, "", err
	}

	ouID, err := GetOrgUnitIDForDomain(domain)
	if err != nil {
		log.Printf("      │ ⚠ Failed to get OU ID: %v", err)
		return false, "", err
	}
	log.Printf("      │ OU ID: %s", ouID)

	client := GetHTTPClient()
	pageStartIndex := 1
	pageSize := 100

	for {
		baseURL := fmt.Sprintf("https://%s:%s/roles", host, port)
		req, err := http.NewRequest("GET", baseURL, nil)
		if err != nil {
			log.Printf("      │ ✗ Failed to create request: %v", err)
			return false, "", err
		}

		q := req.URL.Query()
		q.Set("startIndex", fmt.Sprintf("%d", pageStartIndex))
		q.Set("count", fmt.Sprintf("%d", pageSize))
		req.URL.RawQuery = q.Encode()

		req.Header.Set("Authorization", "Bearer "+auth.BearerToken)
		req.Header.Set("Content-Type", "application/json")

		log.Printf("      │ Query: %s", req.URL.String())

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("      │ ✗ Request failed: %v", err)
			return false, "", err
		}

		var rolesResp RolesResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&rolesResp)
		closeErr := resp.Body.Close()
		if closeErr != nil {
			log.Printf("      │ ⚠ Failed to close role response body: %v", closeErr)
		}

		if resp.StatusCode != 200 {
			log.Printf("      │ ⚠ Unexpected status: %d", resp.StatusCode)
			return false, "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}
		if decodeErr != nil {
			log.Printf("      │ ✗ Failed to parse response: %v", decodeErr)
			return false, "", decodeErr
		}

		log.Printf("      │ Page start: %d, count: %d, total: %d", rolesResp.StartIndex, rolesResp.Count, rolesResp.TotalResults)

		for _, role := range rolesResp.Roles {
			if role.OrganizationUnitID == ouID && strings.EqualFold(role.Name, roleName) {
				mailboxIdentity := fmt.Sprintf("role_%s@%s.db", strings.ToLower(roleName), strings.ToLower(domain))
				log.Printf("      │ ✓ Role found and OU matches")
				log.Printf("      │ Mailbox identity: %s", mailboxIdentity)
				return true, mailboxIdentity, nil
			}
		}

		if rolesResp.Count <= 0 || rolesResp.StartIndex+rolesResp.Count > rolesResp.TotalResults {
			break
		}
		pageStartIndex = rolesResp.StartIndex + rolesResp.Count
	}

	log.Printf("      │ ✗ Role not found for OU/domain")
	return false, "", nil
}
