package groupresolver

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"raven/internal/idp"
	"raven/internal/maildomain"
)

const (
	// assertionCacheBufferTime is the time buffer before JWT expiry to refresh the assertion
	assertionCacheBufferTime = 30 * time.Second
)

// AssertionCache stores cached assertions with their expiry time
type AssertionCache struct {
	assertion string
	expiresAt time.Time
}

// GroupResolver handles group resolution from IDP
type GroupResolver struct {
	baseURL        string
	mailDomains    *maildomain.Map
	assertionCache *AssertionCache
	mu             sync.RWMutex
	httpClient     *http.Client
}

// NewGroupResolver creates a new GroupResolver.
//
// It takes no credentials: raven authenticates to Thunder as a service account, configured
// through the environment. See the idp package. mailDomains gives the domains of the members'
// organization units.
func NewGroupResolver(baseURL string, mailDomains *maildomain.Map) *GroupResolver {
	return &GroupResolver{
		baseURL:     baseURL,
		mailDomains: mailDomains,
		httpClient:  buildHTTPClient(),
	}
}

// ResolveGroupMembers resolves all members of a group (recursively) and returns their email addresses.
// groupName should be the portion before "-group@", e.g. "engineering" from "engineering-group@domain.com",
// and groupDomain the part after the "@". The group must belong to the organization unit that
// owns groupDomain. Each member is addressed at their own organization unit's primary domain,
// whatever groupDomain is.
// Returns slice of resolved email addresses, deduplicated
func (gr *GroupResolver) ResolveGroupMembers(groupName, groupDomain string) ([]string, error) {
	groupOU, ok := gr.mailDomains.OrgUnit(groupDomain)
	if !ok {
		return nil, fmt.Errorf("group domain %q is not in organization_unit_domains", groupDomain)
	}

	// Get system assertion
	assertion, err := gr.getOrFreshAssertion()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain assertion for group resolution: %w", err)
	}

	// Look up the group by name within the organization unit that owns the group's domain
	groupID, err := gr.lookupGroupID(assertion, groupName, groupOU)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup group '%s' in organization unit %s: %w", groupName, groupOU, err)
	}

	// Resolve members recursively
	visited := make(map[string]struct{})
	entries := make(map[string]struct{}) // deduplicate resolved addresses
	var resolveMembers func(string) error

	resolveMembers = func(gid string) error {
		if _, seen := visited[gid]; seen {
			log.Printf("GroupResolver: cycle detected for group %s, skipping", gid)
			return nil
		}
		visited[gid] = struct{}{}

		members, err := gr.fetchGroupMembers(assertion, gid)
		if err != nil {
			return fmt.Errorf("failed to fetch members of group %s: %w", gid, err)
		}

		for _, member := range members {
			if member.Type == "user" {
				// Resolve user to email address
				email, err := gr.resolveUserEmail(assertion, member.ID)
				if err != nil {
					log.Printf("GroupResolver: failed to resolve user %s: %v, skipping", member.ID, err)
					continue
				}
				entries[email] = struct{}{}
			} else if member.Type == "group" {
				// Recursively resolve nested group
				if err := resolveMembers(member.ID); err != nil {
					log.Printf("GroupResolver: failed to resolve nested group %s: %v, continuing", member.ID, err)
					// Don't fail entirely, just skip this nested group
				}
			}
		}

		return nil
	}

	if err := resolveMembers(groupID); err != nil {
		return nil, err
	}

	// Convert map keys to slice
	result := make([]string, 0, len(entries))
	for email := range entries {
		result = append(result, email)
	}

	log.Printf("GroupResolver: resolved %d unique members for group %s", len(result), groupName)
	return result, nil
}

// getOrFreshAssertion returns a cached assertion or fetches a new one if expired
func (gr *GroupResolver) getOrFreshAssertion() (string, error) {
	gr.mu.RLock()
	if gr.assertionCache != nil && gr.assertionCache.assertion != "" {
		if time.Now().Add(assertionCacheBufferTime).Before(gr.assertionCache.expiresAt) {
			log.Printf("GroupResolver: using cached assertion (expires in %v)", time.Until(gr.assertionCache.expiresAt))
			assertion := gr.assertionCache.assertion
			gr.mu.RUnlock()
			return assertion, nil
		}
	}
	gr.mu.RUnlock()

	gr.mu.Lock()
	defer gr.mu.Unlock()

	// Double-check cache in case another goroutine refreshed while waiting for write lock
	if gr.assertionCache != nil && gr.assertionCache.assertion != "" {
		if time.Now().Add(assertionCacheBufferTime).Before(gr.assertionCache.expiresAt) {
			log.Printf("GroupResolver: using cached assertion (expires in %v)", time.Until(gr.assertionCache.expiresAt))
			return gr.assertionCache.assertion, nil
		}
	}

	// Fetch new assertion
	assertion, expiresAt, err := gr.fetchAssertion()
	if err != nil {
		return "", err
	}

	gr.assertionCache = &AssertionCache{
		assertion: assertion,
		expiresAt: expiresAt,
	}

	log.Printf("GroupResolver: obtained fresh assertion (expires in %v)", time.Until(expiresAt))
	return assertion, nil
}

// fetchAssertion obtains a system access token for raven's service account.
func (gr *GroupResolver) fetchAssertion() (string, time.Time, error) {
	return idp.SystemToken(gr.baseURL)
}

// lookupGroupID returns the ID of the group named groupName in organization unit ouID. A group
// with the same name in another organization unit doesn't match.
func (gr *GroupResolver) lookupGroupID(assertion, groupName, ouID string) (string, error) {
	type GroupResponse struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		OrganizationUnitID string `json:"ouId"`
	}

	type GroupsListResponse struct {
		Groups []GroupResponse `json:"groups"`
	}

	var result GroupsListResponse
	if err := gr.getJSON(gr.baseURL+"/groups", assertion, &result); err != nil {
		return "", fmt.Errorf("failed to fetch groups list: %w", err)
	}

	for _, group := range result.Groups {
		if group.OrganizationUnitID == ouID && strings.EqualFold(group.Name, groupName) {
			return group.ID, nil
		}
	}

	return "", fmt.Errorf("group '%s' not found in organization unit %s", groupName, ouID)
}

// fetchGroupMembers fetches the members of a group
func (gr *GroupResolver) fetchGroupMembers(assertion, groupID string) ([]Member, error) {
	type MembersResponse struct {
		Members []Member `json:"members"`
	}

	var result MembersResponse
	endpoint := fmt.Sprintf("%s/groups/%s/members", gr.baseURL, groupID)
	if err := gr.getJSON(endpoint, assertion, &result); err != nil {
		return nil, err
	}

	return result.Members, nil
}

// resolveUserEmail resolves a user member id to an email address.
// It fetches the user profile, extracts username, and takes the domain from the user's own
// organization unit: the first domain listed for it in organization_unit_domains.
func (gr *GroupResolver) resolveUserEmail(assertion, userID string) (string, error) {
	user, err := gr.fetchUserByID(assertion, userID)
	if err != nil {
		return "", err
	}

	username := strings.TrimSpace(user.Username)
	if username == "" {
		return "", fmt.Errorf("user %s has no username", userID)
	}

	if strings.Contains(username, "@") {
		parts := strings.SplitN(username, "@", 2)
		local := strings.TrimSpace(parts[0])
		domain := strings.Trim(strings.TrimSpace(parts[1]), ".")
		if local != "" && domain != "" {
			return local + "@" + domain, nil
		}
		return "", fmt.Errorf("user %s has invalid email username", userID)
	}

	if strings.TrimSpace(user.OrganizationUnit) == "" {
		return "", fmt.Errorf("unable to resolve domain for user %s: missing organization unit", userID)
	}

	domain, ok := gr.mailDomains.PrimaryDomain(user.OrganizationUnit)
	if !ok {
		return "", fmt.Errorf("unable to resolve domain for user %s: organization unit %s is not in organization_unit_domains", userID, user.OrganizationUnit)
	}

	return username + "@" + domain, nil
}

type userRecord struct {
	ID               string
	Username         string
	OrganizationUnit string
}

func (gr *GroupResolver) fetchUserByID(assertion, userID string) (*userRecord, error) {
	type userResponse struct {
		ID                  string `json:"id"`
		OrganizationUnit    string `json:"ouId"`
		OrganizationUnitAlt string `json:"organization_unit"`
		Username            string `json:"username"`
		UserName            string `json:"userName"`
		Name                string `json:"name"`
		Email               string `json:"email"`
		Attributes          struct {
			Username string `json:"username"`
			UserName string `json:"userName"`
			Email    string `json:"email"`
			Name     string `json:"name"`
		} `json:"attributes"`
	}

	var resp userResponse
	if err := gr.getJSON(gr.baseURL+"/users/"+userID, assertion, &resp); err != nil {
		return nil, fmt.Errorf("failed to fetch user %s: %w", userID, err)
	}

	username := firstNonEmpty(
		resp.Attributes.Email,
		resp.Email,
		resp.Attributes.Username,
		resp.Attributes.UserName,
		resp.Username,
		resp.UserName,
		resp.Attributes.Name,
		resp.Name,
	)
	if username == "" {
		return nil, fmt.Errorf("user %s profile missing username", userID)
	}

	orgUnit := strings.TrimSpace(resp.OrganizationUnit)
	if orgUnit == "" {
		orgUnit = strings.TrimSpace(resp.OrganizationUnitAlt)
	}

	return &userRecord{
		ID:               strings.TrimSpace(resp.ID),
		Username:         username,
		OrganizationUnit: orgUnit,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// Member represents a group member
type Member struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "user" or "group"
}

// getJSON performs a GET request with JSON response
func (gr *GroupResolver) getJSON(endpoint, assertion string, out any) error {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return err
	}

	if assertion != "" {
		req.Header.Set("Authorization", "Bearer "+assertion)
	}

	resp, err := gr.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// buildHTTPClient creates an HTTP client with TLS configuration
func buildHTTPClient() *http.Client {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // #nosec G402 -- Required for internal auth server communication
	}

	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
		Timeout:   10 * time.Second,
	}
}

// extractJWTExpiry extracts the expiry time from a JWT token
func extractJWTExpiry(token string) (time.Time, error) {
	// JWT format: header.payload.signature
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid JWT format")
	}

	// Decode payload (base64url encoded)
	payload := parts[1]

	// Decode the base64url payload
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}

	if err := json.Unmarshal(decoded, &claims); err != nil {
		return time.Time{}, fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("no exp claim in JWT")
	}

	return time.Unix(claims.Exp, 0), nil
}
