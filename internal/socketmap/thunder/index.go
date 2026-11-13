package thunder

import (
	"encoding/json"
	"errors"
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
// tree is read and kept.
//
// A walk makes one request per OU, which can outlast a caller's timeout, so lookups only
// read the index. The caller rebuilds it on a timer with RefreshIndex. A lookup waits for a
// walk only while no index has ever been built.
var (
	indexMu sync.RWMutex
	// A domain that several OUs claim maps to "", so its mail is deferred until an admin
	// removes the duplicate, instead of bouncing or reaching the wrong organization.
	indexByDomain    map[string]string
	indexErr         error     // why the last walk failed, nil if it succeeded
	indexBuiltAt     time.Time // when the last walk ended, whether or not it succeeded
	indexRefreshedAt time.Time // when the last successful walk ended

	buildMu sync.Mutex

	maxIndexAge = time.Hour
)

// ErrDomainUnavailable means a domain can't be answered for now, so mail to it should be
// deferred, not bounced.
var ErrDomainUnavailable = errors.New("mail domain temporarily unavailable")

// While no index exists, lookups share a failed walk's error this long before trying again.
const failedBuildRetry = 5 * time.Second

// SetMaxIndexAge sets how old the index may get while refreshes keep failing. After that,
// lookups fail instead of accepting domains whose OU may have been renamed or deleted.
func SetMaxIndexAge(d time.Duration) {
	maxIndexAge = d
}

// WarmIndex builds the domain index so the first lookups don't wait for a tree walk. It does
// nothing if a lookup already built one.
func WarmIndex(host, port string, tokenRefreshSeconds int) error {
	return ensureIndex(host, port, tokenRefreshSeconds)
}

// RefreshIndex walks the tree again and swaps in the result. If the walk fails, the previous
// index keeps being served.
func RefreshIndex(host, port string, tokenRefreshSeconds int) error {
	buildMu.Lock()
	defer buildMu.Unlock()

	return storeIndex(host, port, tokenRefreshSeconds)
}

// lookupOrgUnit returns the ID of the OU that owns domain. It fails with ErrDomainUnavailable
// when no index could be built, the index is older than maxIndexAge, or several OUs claim
// the domain.
func lookupOrgUnit(domain, host, port string, tokenRefreshSeconds int) (string, bool, error) {
	domain, ok := maildomain.FromHandle(domain)
	if !ok {
		return "", false, nil
	}

	index, refreshedAt := currentIndex()
	if index == nil {
		if err := ensureIndex(host, port, tokenRefreshSeconds); err != nil {
			return "", false, fmt.Errorf("%w: %w", ErrDomainUnavailable, err)
		}
		index, refreshedAt = currentIndex()
	}

	if age := time.Since(refreshedAt); age > maxIndexAge {
		return "", false, fmt.Errorf("%w: domain index not refreshed for %s", ErrDomainUnavailable, age.Round(time.Second))
	}

	ouID, found := index[domain]
	if found && ouID == "" {
		return "", false, fmt.Errorf("%w: %s is the handle of more than one OU", ErrDomainUnavailable, domain)
	}
	return ouID, found, nil
}

func currentIndex() (map[string]string, time.Time) {
	indexMu.RLock()
	defer indexMu.RUnlock()
	return indexByDomain, indexRefreshedAt
}

// ensureIndex builds the index unless one exists, so callers queued behind a first build share
// its result, failure included, instead of each walking the tree again.
func ensureIndex(host, port string, tokenRefreshSeconds int) error {
	buildMu.Lock()
	defer buildMu.Unlock()

	indexMu.RLock()
	built, err, at := indexByDomain != nil, indexErr, indexBuiltAt
	indexMu.RUnlock()

	if built {
		return nil
	}
	if err != nil && time.Since(at) < failedBuildRetry {
		return err
	}
	return storeIndex(host, port, tokenRefreshSeconds)
}

// storeIndex walks the tree and stores the result. The caller holds buildMu.
func storeIndex(host, port string, tokenRefreshSeconds int) error {
	index, err := buildIndex(host, port, tokenRefreshSeconds)

	indexMu.Lock()
	defer indexMu.Unlock()
	now := time.Now()
	if err == nil {
		indexByDomain, indexRefreshedAt = index, now
	}
	indexErr, indexBuiltAt = err, now

	return err
}

func buildIndex(host, port string, tokenRefreshSeconds int) (map[string]string, error) {
	// A long walk can outlive the token it started with, so each page asks for the current one.
	token := func() (string, error) {
		auth, err := GetAuth(host, port, tokenRefreshSeconds)
		if err != nil {
			return "", err
		}
		return auth.BearerToken, nil
	}

	baseURL := fmt.Sprintf("https://%s:%s", host, port)
	client := GetHTTPClient()

	owners := make(map[string][]string)
	visited := make(map[string]bool)
	pending := []string{fmt.Sprintf("/organization-units?limit=%d", orgUnitPageLimit)}

	for len(pending) > 0 {
		listPath := pending[0]
		pending = pending[1:]

		ous, err := listOrgUnits(client, baseURL, listPath, token)
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
	conflicts := 0
	for domain, ids := range owners {
		if len(ids) > 1 {
			log.Printf("      │ ⚠ Domain %s is the handle of several OUs %v, deferring its mail", domain, ids)
			index[domain] = ""
			conflicts++
			continue
		}
		index[domain] = ids[0]
	}

	log.Printf("      │ ✓ Domain index built: %d OUs, %d mail domains, %d claimed by several OUs", len(visited), len(index)-conflicts, conflicts)

	return index, nil
}

// listOrgUnits returns every page of the listing at listPath.
func listOrgUnits(client *http.Client, baseURL, listPath string, token func() (string, error)) ([]OrgUnitResponse, error) {
	var ous []OrgUnitResponse
	seen := make(map[string]bool)

	for listPath != "" {
		if seen[listPath] {
			return nil, fmt.Errorf("pagination loop at %s", listPath)
		}
		seen[listPath] = true

		bearer, err := token()
		if err != nil {
			return nil, err
		}
		page, err := getOrgUnitsPage(client, baseURL+listPath, bearer)
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
