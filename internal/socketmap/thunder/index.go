package thunder

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"raven/internal/maildomain"
)

// Thunder rejects a larger page size.
const orgUnitPageLimit = 100

// The index maps each mail domain to the OU whose handle it is. Thunder can only find an OU
// by its full handle path, and a domain says nothing about where its OU sits, so the whole
// tree is read and kept for indexTTL.
var (
	indexMu       sync.Mutex
	indexTTL      = 5 * time.Minute
	indexByDomain map[string]string
	indexBuiltAt  time.Time
)

// SetIndexTTL sets how long the domain index is used before it is rebuilt.
func SetIndexTTL(ttl time.Duration) {
	indexMu.Lock()
	defer indexMu.Unlock()
	indexTTL = ttl
}

// lookupOrgUnit returns the ID of the OU that owns domain. It fails only when no index could
// ever be built; an index that can't be refreshed keeps being served.
func lookupOrgUnit(domain, host, port string, tokenRefreshSeconds int) (string, bool, error) {
	domain, ok := maildomain.FromHandle(domain)
	if !ok {
		return "", false, nil
	}

	indexMu.Lock()
	defer indexMu.Unlock()

	if indexByDomain == nil || time.Since(indexBuiltAt) >= indexTTL {
		index, err := buildIndex(host, port, tokenRefreshSeconds)
		switch {
		case err == nil:
			indexByDomain = index
		case indexByDomain == nil:
			return "", false, err
		default:
			log.Printf("      │ ⚠ Domain index refresh failed, serving the previous one: %v", err)
		}
		// A failed refresh also waits a full TTL, so an outage doesn't put a tree walk
		// behind every lookup.
		indexBuiltAt = time.Now()
	}

	ouID, found := indexByDomain[domain]
	return ouID, found, nil
}

func buildIndex(host, port string, tokenRefreshSeconds int) (map[string]string, error) {
	auth, err := GetAuth(host, port, tokenRefreshSeconds)
	if err != nil {
		return nil, err
	}

	baseURL := fmt.Sprintf("https://%s:%s", host, port)
	client := GetHTTPClient()

	owners := make(map[string][]string)
	visited := make(map[string]bool)
	pending := []string{fmt.Sprintf("/organization-units?limit=%d", orgUnitPageLimit)}

	for len(pending) > 0 {
		listPath := pending[0]
		pending = pending[1:]

		ous, err := listOrgUnits(client, baseURL, listPath, auth.BearerToken)
		if err != nil {
			return nil, err
		}

		for _, ou := range ous {
			if ou.ID == "" || visited[ou.ID] {
				continue
			}
			visited[ou.ID] = true

			if domain, ok := maildomain.FromHandle(ou.Handle); ok {
				owners[domain] = append(owners[domain], ou.ID)
			}
			// Non-mail OUs are walked too: a grouping OU can hold OUs that have mail.
			pending = append(pending, fmt.Sprintf("/organization-units/%s/ous?limit=%d", url.PathEscape(ou.ID), orgUnitPageLimit))
		}
	}

	index := make(map[string]string, len(owners))
	for domain, ids := range owners {
		if len(ids) > 1 {
			// Bouncing is safer than delivering to the wrong organization.
			log.Printf("      │ ⚠ Domain %s is the handle of several OUs %v, ignoring it", domain, ids)
			continue
		}
		index[domain] = ids[0]
	}

	log.Printf("      │ ✓ Domain index built: %d OUs, %d mail domains", len(visited), len(index))

	return index, nil
}

// listOrgUnits returns every page of the listing at listPath.
func listOrgUnits(client *http.Client, baseURL, listPath, token string) ([]OrgUnitResponse, error) {
	var ous []OrgUnitResponse
	seen := make(map[string]bool)

	for listPath != "" {
		if seen[listPath] {
			return nil, fmt.Errorf("pagination loop at %s", listPath)
		}
		seen[listPath] = true

		page, err := getOrgUnitsPage(client, baseURL+listPath, token)
		if err != nil {
			return nil, err
		}
		ous = append(ous, page.OrganizationUnits...)

		listPath = ""
		for _, link := range page.Links {
			if link.Rel == "next" {
				listPath = link.Href
				break
			}
		}
	}

	return ous, nil
}

func getOrgUnitsPage(client *http.Client, endpoint, token string) (*OrgUnitsResponse, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing %s: unexpected status %d", endpoint, resp.StatusCode)
	}

	var page OrgUnitsResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("listing %s: %w", endpoint, err)
	}

	return &page, nil
}
