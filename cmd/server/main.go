package main

import (
	"flag"
	"log"
	"net"

	"raven/internal/blobstorage"
	"raven/internal/conf"
	"raven/internal/db"
	"raven/internal/idp"
	"raven/internal/server"
)

const SERVER_IP = "0.0.0.0:143"
const SERVER_IP_SSL = "0.0.0.0:993"

func main() {
	// Command-line flags
	dbPath := flag.String("db", "/app/data/databases", "Path to database directory")
	configPath := flag.String("config", "/etc/raven/raven.yaml", "Path to configuration file")
	flag.Parse()

	log.Println("Starting Raven SQLite IMAP server...")

	// Initialize database manager
	dbManager, err := db.NewDBManager(*dbPath)
	if err != nil {
		log.Fatal("Failed to initialize database manager:", err)
	}
	defer func() {
		if err := dbManager.Close(); err != nil {
			log.Printf("Error closing database manager: %v", err)
		}
	}()

	log.Printf("Database manager initialized: %s", *dbPath)

	// Try to load configuration for S3 blob storage
	var s3Storage *blobstorage.S3BlobStorage
	cfg, err := conf.LoadConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config from %s: %v", *configPath, err)
		log.Println("S3 blob storage will be disabled")
	} else if cfg.BlobStorage.Enabled {
		log.Println("Initializing S3 blob storage...")
		s3Storage, err = blobstorage.NewS3BlobStorage(cfg.BlobStorage)
		if err != nil {
			log.Printf("Warning: Failed to initialize S3 blob storage: %v", err)
			log.Println("Falling back to local SQLite storage")
			s3Storage = nil
		} else {
			log.Printf("S3 blob storage initialized: %s (bucket: %s)", cfg.BlobStorage.Endpoint, cfg.BlobStorage.Bucket)
		}
	} else {
		log.Println("S3 blob storage is disabled in config, using local SQLite storage")
	}

	idp.WarnIfDirectAuthSecretMissing()

	// Create IMAP server with S3 storage support
	imapServer := server.NewIMAPServerWithS3(dbManager, s3Storage)

	// Start plain IMAP (143)
	go func() {
		ln, err := net.Listen("tcp", SERVER_IP) // #nosec G102 -- Intentionally binding to all interfaces for IMAP server
		if err != nil {
			log.Fatal("Failed to start TCP listener:", err)
		}
		defer func() {
			if err := ln.Close(); err != nil {
				log.Printf("Error closing listener: %v", err)
			}
		}()

		log.Printf("Raven SQLite IMAP server running on %s", SERVER_IP)
		log.Println("Configure your email client with:")
		log.Println("  Server: localhost (or container IP)")
		log.Println("  Port: 143")

		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Println("Accept error:", err)
				continue
			}

			log.Printf("New connection from: %s", conn.RemoteAddr())
			go imapServer.HandleConnection(conn)
		}
	}()

	// Start IMAPS (993)
	lnSSL, err := net.Listen("tcp", SERVER_IP_SSL) // #nosec G102 -- Intentionally binding to all interfaces for IMAPS server
	if err != nil {
		log.Fatal("Failed to start SSL TCP listener:", err)
	}
	defer func() {
		if err := lnSSL.Close(); err != nil {
			log.Printf("Error closing SSL listener: %v", err)
		}
	}()

	log.Printf("Raven SQLite IMAPS server running on %s", SERVER_IP_SSL)
	log.Println("Configure your email client with:")
	log.Println("  Server: localhost (or container IP)")
	log.Println("  Port: 993")
	log.Println("  Security: SSL/TLS")

	for {
		conn, err := lnSSL.Accept()
		if err != nil {
			log.Println("Accept error:", err)
			continue
		}

		log.Printf("New SSL connection from: %s", conn.RemoteAddr())
		go imapServer.HandleSSLConnection(conn)
	}
}
