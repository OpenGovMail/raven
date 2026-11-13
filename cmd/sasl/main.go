package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"raven/internal/conf"
	"raven/internal/idp"
	"raven/internal/sasl"
)

func main() {
	// Command-line flags
	socketPath := flag.String("socket", "/var/spool/postfix/private/auth", "Path to UNIX socket")
	tcpAddr := flag.String("tcp", ":12345", "TCP address to bind (e.g., 127.0.0.1:12345 or :12345)")
	configPath := flag.String("config", "/etc/raven/raven.yaml", "Path to configuration file")
	flag.Parse()

	log.Println("Starting Raven SASL Authentication Service...")

	// Load configuration
	cfg, err := conf.LoadConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config: %v", err)
		log.Println("Please ensure raven.yaml is configured with auth_server_url")
		os.Exit(1)
	}

	// Set defaults and validate configuration
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	log.Printf("Configuration loaded:")
	log.Printf("  Socket path: %s", *socketPath)
	log.Printf("  TCP address: %s", *tcpAddr)
	log.Printf("  Config path: %s", *configPath)
	log.Printf("  Auth URL: %s", cfg.AuthServerURL)
	log.Printf("  SASL Scope: %s", cfg.SASLScope)
	idp.WarnIfDirectAuthSecretMissing()

	// Create SASL server with scope configuration
	server := sasl.NewServer(*socketPath, *tcpAddr, cfg.AuthServerURL, cfg.Domain, cfg.SASLScope)

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()

	// Wait for shutdown signal or error
	select {
	case err := <-errChan:
		if err != nil {
			log.Fatalf("Server error: %v", err)
		}
	case sig := <-sigChan:
		log.Printf("Received signal %v, shutting down gracefully...", sig)
		if err := server.Shutdown(); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}
	}

	log.Println("Raven SASL Authentication Service stopped")
}
