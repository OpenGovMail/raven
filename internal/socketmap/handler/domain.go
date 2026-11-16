package handler

import (
	"log"

	"raven/internal/socketmap/cache"
	"raven/internal/socketmap/config"
	"raven/internal/socketmap/thunder"
)

// HandleVirtualDomainsMap handles the virtual-domains map lookup. The domains come from the
// organization_unit_domains mapping in memory, so the answer is never cached or deferred.
func HandleVirtualDomainsMap(domain string, _ *config.Config, _ *cache.Cache) string {
	log.Printf("  │ Checking if domain is valid...")

	if thunder.ValidateDomain(domain) {
		log.Printf("  │ ✓ DOMAIN FOUND: %s", domain)
		log.Printf("  │ Response: OK")
		log.Printf("  └─────────────────────────────────────")
		return "OK 1"
	}

	log.Printf("  │ ✗ DOMAIN NOT FOUND: %s", domain)
	log.Printf("  │ Response: NOTFOUND")
	log.Printf("  └─────────────────────────────────────")
	return "NOTFOUND"
}
