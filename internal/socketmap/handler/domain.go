package handler

import (
	"log"
	"time"

	"raven/internal/socketmap/cache"
	"raven/internal/socketmap/config"
	"raven/internal/socketmap/thunder"
)

// DomainExists checks if a domain exists in Thunder IDP. An error means it can't be answered
// for now.
func DomainExists(domain string, cfg *config.Config, cacheManager *cache.Cache) (bool, error) {
	log.Printf("    ┌─ Domain Lookup ─────────────────")
	log.Printf("    │ Domain: %s", domain)

	// Check cache first (read lock)
	cacheKey := "domain:" + domain
	entry, found := cacheManager.Get(cacheKey)

	now := time.Now()

	if found {
		// Cache hit - check if still valid
		if !cacheManager.IsExpired(entry) {
			log.Printf("    │ ✓ CACHE HIT (fresh)")
			log.Printf("    │ Cached result: exists=%v", entry.Exists)
			log.Printf("    │ Expires: %s", entry.Expires.Format("15:04:05"))
			log.Printf("    └─────────────────────────────────")
			return entry.Exists, nil
		}

		// Cache expired but exists - check if we should refresh
		cacheAge := now.Sub(entry.LastUpdate).Seconds()
		log.Printf("    │ ✓ CACHE HIT (stale)")
		log.Printf("    │ Age: %.0f seconds", cacheAge)
		log.Printf("    │ Refreshing from IDP...")
	} else {
		log.Printf("    │ ✗ CACHE MISS")
		log.Printf("    │ Querying IDP...")
	}

	// Query Thunder IDP for domain validation
	exists, err := thunder.ValidateDomain(domain, cfg.ThunderHost, cfg.ThunderPort, cfg.TokenRefreshSeconds)
	if err != nil {
		log.Printf("    │ ⚠ IDP query failed: %v", err)
		log.Printf("    └─────────────────────────────────")
		return false, err
	}

	log.Printf("    │ IDP result: exists=%v", exists)

	// Only cache positive results (exists=true)
	if exists {
		cacheManager.Set(cacheKey, cache.Entry{
			Exists:     true,
			Expires:    now.Add(cacheManager.GetTTL()),
			LastUpdate: now,
		})
		log.Printf("    │ ✓ Cached positive result for %d seconds", cfg.CacheTTLSeconds)
	} else {
		log.Printf("    │ ℹ Negative result NOT cached (will query IDP next time)")
	}

	log.Printf("    └─────────────────────────────────")

	return exists, nil
}

// HandleVirtualDomainsMap handles the virtual-domains map lookup
func HandleVirtualDomainsMap(domain string, cfg *config.Config, cacheManager *cache.Cache) string {
	log.Printf("  │ Checking if domain is valid...")
	exists, err := DomainExists(domain, cfg, cacheManager)

	// TEMP makes Postfix defer the mail, so the sender retries instead of getting a bounce.
	if err != nil {
		log.Printf("  │ Response: TEMP")
		log.Printf("  └─────────────────────────────────────")
		return "TEMP " + err.Error()
	}

	if exists {
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
