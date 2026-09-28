package main

import (
	"log"
	"time"

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

	// Authenticate with Thunder at startup with retry logic
	log.Println("┌─ Thunder Authentication ─────────")

	var auth *thunder.Auth
	var err error
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
	thunder.SetIndexTTL(time.Duration(cfg.CacheTTLSeconds) * time.Second)

	// Build the domain index before serving so the first lookups don't wait on a tree walk
	log.Println("┌─ Domain Index ───────────────────")
	if err := thunder.WarmIndex(cfg.ThunderHost, cfg.ThunderPort, cfg.TokenRefreshSeconds); err != nil {
		log.Printf("│ ⚠ Initial domain index build failed: %v", err)
		log.Printf("│ Check that raven's service account can list organization units")
		log.Printf("│ Service will retry on first request")
	}
	log.Println("└───────────────────────────────────")
	log.Println("")

	// Display configuration
	log.Printf("Starting socketmap service on %s:%s", cfg.Host, cfg.Port)
	log.Printf("Configuration:")
	log.Printf("  • Thunder Host: %s:%s", cfg.ThunderHost, cfg.ThunderPort)
	log.Printf("  • Cache TTL: %d seconds", cfg.CacheTTLSeconds)
	log.Printf("  • Token Refresh: %d seconds", cfg.TokenRefreshSeconds)
	log.Println("")

	// Create and start server
	srv := server.New(cfg, cacheManager)
	if err := srv.Start(); err != nil {
		log.Fatalf("✗ Failed to start server: %v", err)
	}
}
