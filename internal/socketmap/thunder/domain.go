package thunder

import (
	"fmt"
	"log"
)

// ValidateDomain checks if a domain is the handle of an OU in Thunder IDP
func ValidateDomain(domain, host, port string, tokenRefreshSeconds int) (bool, error) {
	log.Printf("      ┌─ Thunder Domain Validation ──")
	log.Printf("      │ Domain: %s", domain)

	ouID, found, err := lookupOrgUnit(domain, host, port, tokenRefreshSeconds)
	if err != nil {
		log.Printf("      │ ⚠ Domain index unavailable: %v", err)
		log.Printf("      └──────────────────────────────")
		return false, err
	}

	if !found {
		log.Printf("      │ ✗ Domain not found in Thunder")
		log.Printf("      └──────────────────────────────")
		return false, nil
	}

	log.Printf("      │ ✓ Domain found in Thunder")
	log.Printf("      │ OU ID: %s", ouID)
	log.Printf("      └──────────────────────────────")

	return true, nil
}

// GetOrgUnitIDForDomain returns the ID of the OU whose handle is domain
func GetOrgUnitIDForDomain(domain, host, port string, tokenRefreshSeconds int) (string, error) {
	ouID, found, err := lookupOrgUnit(domain, host, port, tokenRefreshSeconds)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no OU has the handle %s", domain)
	}

	return ouID, nil
}
