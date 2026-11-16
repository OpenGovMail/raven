package thunder

import (
	"errors"
	"fmt"
	"log"
	"sync/atomic"

	"raven/internal/maildomain"
)

// ErrNoSuchDomain means no OU owns the domain: an answer, not a failed lookup.
var ErrNoSuchDomain = errors.New("no organization unit owns this domain")

// mailDomains is the configured OU → domains mapping. Until SetMailDomains is called no
// domain is known.
var mailDomains atomic.Pointer[maildomain.Map]

// SetMailDomains sets the mapping that says which OU owns each mail domain.
func SetMailDomains(m *maildomain.Map) {
	mailDomains.Store(m)
}

// ValidateDomain reports whether an OU owns domain.
func ValidateDomain(domain string) bool {
	log.Printf("      ┌─ Domain Validation ──────────")
	log.Printf("      │ Domain: %s", domain)

	ouID, found := mailDomains.Load().OrgUnit(domain)
	if !found {
		log.Printf("      │ ✗ Domain not in organization_unit_domains")
		log.Printf("      └──────────────────────────────")
		return false
	}

	log.Printf("      │ ✓ Domain found")
	log.Printf("      │ OU ID: %s", ouID)
	log.Printf("      └──────────────────────────────")

	return true
}

// GetOrgUnitIDForDomain returns the ID of the OU that owns domain.
func GetOrgUnitIDForDomain(domain string) (string, error) {
	ouID, found := mailDomains.Load().OrgUnit(domain)
	if !found {
		return "", fmt.Errorf("%w: %s", ErrNoSuchDomain, domain)
	}
	return ouID, nil
}
