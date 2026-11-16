package main

import (
	"log"
	"time"

	"raven/internal/conf"
	"raven/internal/socketmap/cache"
	"raven/internal/socketmap/config"
	"raven/internal/socketmap/server"
	"raven/internal/socketmap/thunder"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("╔════════════════════════════════════════════════════════════╗")
	log.Println("║       Socketmap Service - Postfix Virtual Mailbox Maps    ║")
	log.Println("╚════════════════════════════════════════════════════════════╝")
	log.Println("")

	// Load configuration
	cfg := config.Load()

	// The mail domains each organization unit owns come from raven.yaml. Without them every
	// domain would answer NOTFOUND and mail would bounce, so a missing or invalid mapping
	// stops the service instead.
	ravenCfg, err := conf.LoadConfig()
	if err != nil {
		log.Fatalf("✗ Failed to load raven.yaml for organization_unit_domains: %v", err)
	}
	if ravenCfg.MailDomains.Len() == 0 {
		log.Printf("⚠ organization_unit_domains is empty: every domain will answer NOTFOUND")
	}
	thunder.SetMailDomains(ravenCfg.MailDomains)

	// Authenticate with Thunder at startup with retry logic
	log.Println("┌─ Thunder Authentication ─────────")

	var auth *thunder.Auth
	maxRetries := 5
	retryDelay := 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("│ Retry attempt %d/%d (waiting %v)...", attempt, maxRetries, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
		}

		auth, err = thunder.Authenticate(cfg.ThunderHost, cfg.ThunderPort, cfg.TokenRefreshSeconds)
		if err == nil {
			thunder.SetAuth(auth)
			log.Println("└───────────────────────────────────")
			break
		}

		if attempt < maxRetries {
			log.Printf("│ ⚠ Authentication attempt %d failed: %v", attempt, err)
		}
	}

	if err != nil {
		log.Printf("│ ⚠ Initial authentication failed after %d attempts: %v", maxRetries, err)
		log.Printf("│ Service will attempt to authenticate on first request")
		log.Println("└───────────────────────────────────")
	}
	log.Println("")

	// Start token refresh goroutine
	go func() {
		ticker := time.NewTicker(time.Duration(cfg.TokenRefreshSeconds) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("⏰ Token refresh timer triggered")
			newAuth, err := thunder.Authenticate(cfg.ThunderHost, cfg.ThunderPort, cfg.TokenRefreshSeconds)
			if err != nil {
				log.Printf("⚠ Token refresh failed: %v", err)
			} else {
				thunder.SetAuth(newAuth)
				log.Println("✓ Token refreshed successfully")
			}
		}
	}()

	// Initialize cache
	cacheManager := cache.New(cfg.CacheTTLSeconds)

	// Display configuration
	log.Printf("Starting socketmap service on %s:%s", cfg.Host, cfg.Port)
	log.Printf("Configuration:")
	log.Printf("  • Thunder Host: %s:%s", cfg.ThunderHost, cfg.ThunderPort)
	log.Printf("  • Cache TTL: %d seconds", cfg.CacheTTLSeconds)
	log.Printf("  • Token Refresh: %d seconds", cfg.TokenRefreshSeconds)
	log.Printf("  • Mail domains: %d", ravenCfg.MailDomains.Len())
	log.Println("")

	// Create and start server
	srv := server.New(cfg, cacheManager)
	if err := srv.Start(); err != nil {
		log.Fatalf("✗ Failed to start server: %v", err)
	}
}
