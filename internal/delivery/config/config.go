package config

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"raven/internal/blobstorage"
	"raven/internal/maildomain"

	"gopkg.in/yaml.v2"
)

// Config holds the delivery service configuration
type Config struct {
	LMTP        LMTPConfig         `yaml:"lmtp"`
	Database    DatabaseConfig     `yaml:"database"`
	Delivery    DeliveryConfig     `yaml:"delivery"`
	Socketmap   SocketmapConfig    `yaml:"socketmap"`
	Logging     LoggingConfig      `yaml:"logging"`
	BlobStorage blobstorage.Config `yaml:"blob_storage"`
	IDPBaseURL  string             // IDP base URL (loaded from raven.yaml auth_server_url)
	// MailDomains maps organization units to their mail domains (loaded from raven.yaml
	// organization_unit_domains).
	MailDomains *maildomain.Map `yaml:"-"`
}

// errInvalidMailDomains marks a raven.yaml whose organization_unit_domains is invalid. Unlike a
// missing raven.yaml, that stops the service: it would address group mail wrongly.
var errInvalidMailDomains = errors.New("invalid organization_unit_domains in raven.yaml")

// LMTPConfig holds LMTP server configuration
type LMTPConfig struct {
	UnixSocket    string `yaml:"unix_socket"`
	TCPAddress    string `yaml:"tcp_address"`
	MaxSize       int64  `yaml:"max_size"`       // Maximum message size in bytes
	Timeout       int    `yaml:"timeout"`        // Connection timeout in seconds
	Hostname      string `yaml:"hostname"`       // Server hostname for LHLO
	MaxRecipients int    `yaml:"max_recipients"` // Maximum recipients per transaction
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// DeliveryConfig holds delivery-specific configuration
type DeliveryConfig struct {
	DefaultFolder     string   `yaml:"default_folder"`      // Default delivery folder (usually INBOX)
	QuotaEnabled      bool     `yaml:"quota_enabled"`       // Enable quota checking
	QuotaLimit        int64    `yaml:"quota_limit"`         // Quota limit in bytes
	AllowedDomains    []string `yaml:"allowed_domains"`     // List of allowed recipient domains
	RejectUnknownUser bool     `yaml:"reject_unknown_user"` // Reject messages for unknown users
}

// SocketmapConfig controls optional socketmap identity resolution in LMTP.
type SocketmapConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Network        string `yaml:"network"`         // tcp or unix
	Address        string `yaml:"address"`         // host:port for tcp, socket path for unix
	TimeoutSeconds int    `yaml:"timeout_seconds"` // Connection/read/write timeout
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level"`  // log level: debug, info, warn, error
	Format string `yaml:"format"` // log format: text, json
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		LMTP: LMTPConfig{
			UnixSocket:    "/var/run/raven/lmtp.sock",
			TCPAddress:    "127.0.0.1:24",
			MaxSize:       52428800, // 50MB
			Timeout:       300,      // 5 minutes
			Hostname:      "localhost",
			MaxRecipients: 100,
		},
		Database: DatabaseConfig{
			Path: "data/databases",
		},
		Delivery: DeliveryConfig{
			DefaultFolder:     "INBOX",
			QuotaEnabled:      false,
			QuotaLimit:        1073741824, // 1GB
			AllowedDomains:    []string{},
			RejectUnknownUser: false,
		},
		Socketmap: SocketmapConfig{
			Enabled:        false,
			Network:        "tcp",
			Address:        "",
			TimeoutSeconds: 2,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	// Read file
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Try to load raven.yaml to get IDP URL and domain
	if err := loadMainConfig(cfg); err != nil {
		if errors.Is(err, errInvalidMailDomains) {
			return nil, err
		}
		// Log warning but don't fail - group resolution will just not work
		log.Printf("Warning: failed to load main config for group resolution: %v", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// loadMainConfig loads raven.yaml to extract the IDP URL and the OU mail domains
func loadMainConfig(cfg *Config) error {
	ravenConfigPaths := []string{
		"/etc/raven/raven.yaml",
		"./config/raven.yaml",
		"./raven.yaml",
		"config/raven.yaml",
	}

	type MainConfig struct {
		AuthServerURL           string              `yaml:"auth_server_url"`
		OrganizationUnitDomains map[string][]string `yaml:"organization_unit_domains"`
	}

	var ravenCfg MainConfig
	var data []byte
	var err error

	for _, path := range ravenConfigPaths {
		data, err = os.ReadFile(filepath.Clean(path))
		if err == nil {
			break
		}
	}

	if err != nil {
		return fmt.Errorf("could not find raven.yaml in standard locations")
	}

	if err := yaml.Unmarshal(data, &ravenCfg); err != nil {
		return fmt.Errorf("failed to parse raven.yaml: %w", err)
	}

	mailDomains, err := maildomain.NewMap(ravenCfg.OrganizationUnitDomains)
	if err != nil {
		return fmt.Errorf("%w: %w", errInvalidMailDomains, err)
	}
	cfg.MailDomains = mailDomains

	// Extract base URL from auth server URL
	// Strip path like /auth/credentials/authenticate to get base URL
	baseURL, err := extractBaseURL(ravenCfg.AuthServerURL)
	if err != nil {
		return fmt.Errorf("failed to extract IDP base URL: %w", err)
	}

	cfg.IDPBaseURL = baseURL

	return nil
}

// extractBaseURL extracts the base URL from an auth server URL
func extractBaseURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid auth server URL: %s", rawURL)
	}

	return parsed.Scheme + "://" + parsed.Host, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate LMTP config
	if c.LMTP.UnixSocket == "" && c.LMTP.TCPAddress == "" {
		return fmt.Errorf("at least one of unix_socket or tcp_address must be specified")
	}

	if c.LMTP.MaxSize <= 0 {
		return fmt.Errorf("max_size must be positive")
	}

	if c.LMTP.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if c.LMTP.MaxRecipients <= 0 {
		return fmt.Errorf("max_recipients must be positive")
	}

	// Validate database config
	if c.Database.Path == "" {
		return fmt.Errorf("database path cannot be empty")
	}

	// Validate delivery config
	if c.Delivery.DefaultFolder == "" {
		return fmt.Errorf("default_folder cannot be empty")
	}

	if c.Delivery.QuotaEnabled && c.Delivery.QuotaLimit <= 0 {
		return fmt.Errorf("quota_limit must be positive when quota is enabled")
	}

	if c.Socketmap.Enabled {
		if c.Socketmap.Network != "tcp" && c.Socketmap.Network != "unix" {
			return fmt.Errorf("socketmap network must be tcp or unix")
		}

		if c.Socketmap.Address == "" {
			return fmt.Errorf("socketmap address cannot be empty when enabled")
		}

		if c.Socketmap.TimeoutSeconds <= 0 {
			return fmt.Errorf("socketmap timeout_seconds must be positive when enabled")
		}
	}

	// Validate logging config
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid log level: %s", c.Logging.Level)
	}

	validFormats := map[string]bool{"text": true, "json": true}
	if !validFormats[c.Logging.Format] {
		return fmt.Errorf("invalid log format: %s", c.Logging.Format)
	}

	return nil
}
