package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"raven/internal/delivery/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
		return
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Default config validation failed: %v", err)
	}

	// Check default values
	if cfg.LMTP.MaxSize <= 0 {
		t.Error("MaxSize should be positive")
	}

	if cfg.LMTP.Timeout <= 0 {
		t.Error("Timeout should be positive")
	}

	if cfg.Database.Path == "" {
		t.Error("Database path should not be empty")
	}
}

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	content := `
lmtp:
  unix_socket: "/tmp/test.sock"
  tcp_address: "127.0.0.1:24"
  max_size: 10485760
  timeout: 300
  hostname: "test.local"
  max_recipients: 50

database:
  path: "/tmp/test.db"

delivery:
  default_folder: "INBOX"
  quota_enabled: true
  quota_limit: 1073741824
  allowed_domains:
    - "example.com"
    - "test.com"
  reject_unknown_user: true

logging:
  level: "debug"
  format: "json"
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpfile.Name()) }()

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Load config
	cfg, err := config.LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify values
	if cfg.LMTP.UnixSocket != "/tmp/test.sock" {
		t.Errorf("Expected unix_socket /tmp/test.sock, got %s", cfg.LMTP.UnixSocket)
	}

	if cfg.LMTP.MaxSize != 10485760 {
		t.Errorf("Expected max_size 10485760, got %d", cfg.LMTP.MaxSize)
	}

	if cfg.Delivery.QuotaEnabled != true {
		t.Error("Expected quota_enabled to be true")
	}

	if len(cfg.Delivery.AllowedDomains) != 2 {
		t.Errorf("Expected 2 allowed domains, got %d", len(cfg.Delivery.AllowedDomains))
	}

	if cfg.Logging.Level != "debug" {
		t.Errorf("Expected log level debug, got %s", cfg.Logging.Level)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(*config.Config)
		expectErr bool
	}{
		{
			name:      "Valid config",
			modify:    func(c *config.Config) {},
			expectErr: false,
		},
		{
			name: "No listeners",
			modify: func(c *config.Config) {
				c.LMTP.UnixSocket = ""
				c.LMTP.TCPAddress = ""
			},
			expectErr: true,
		},
		{
			name: "Invalid max size",
			modify: func(c *config.Config) {
				c.LMTP.MaxSize = -1
			},
			expectErr: true,
		},
		{
			name: "Invalid timeout",
			modify: func(c *config.Config) {
				c.LMTP.Timeout = 0
			},
			expectErr: true,
		},
		{
			name: "Empty database path",
			modify: func(c *config.Config) {
				c.Database.Path = ""
			},
			expectErr: true,
		},
		{
			name: "Invalid quota",
			modify: func(c *config.Config) {
				c.Delivery.QuotaEnabled = true
				c.Delivery.QuotaLimit = -1
			},
			expectErr: true,
		},
		{
			name: "Invalid log level",
			modify: func(c *config.Config) {
				c.Logging.Level = "invalid"
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			tt.modify(cfg)

			err := cfg.Validate()
			if tt.expectErr && err == nil {
				t.Error("Expected validation error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// loadWithRavenYAML loads a minimal delivery.yaml from a directory whose raven.yaml has
// ravenYAML.
func loadWithRavenYAML(t *testing.T, ravenYAML string) (*config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "raven.yaml"), []byte(ravenYAML), 0600); err != nil {
		t.Fatalf("write raven.yaml: %v", err)
	}
	deliveryPath := filepath.Join(dir, "delivery.yaml")
	if err := os.WriteFile(deliveryPath, []byte("lmtp:\n  tcp_address: 127.0.0.1:24\n"), 0600); err != nil {
		t.Fatalf("write delivery.yaml: %v", err)
	}
	return config.LoadConfig(deliveryPath)
}

func TestLoadConfig_OrganizationUnitDomains(t *testing.T) {
	cfg, err := loadWithRavenYAML(t, `auth_server_url: https://idp.example.com/auth
organization_unit_domains:
  ou-1: [example.com, example.org]
`)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.IDPBaseURL != "https://idp.example.com" {
		t.Errorf("IDPBaseURL = %q", cfg.IDPBaseURL)
	}
	if got := cfg.MailDomains.Domains("ou-1"); len(got) != 2 {
		t.Errorf("Domains(ou-1) = %v, want 2 domains", got)
	}
}

func TestLoadConfig_InvalidOrganizationUnitDomains(t *testing.T) {
	_, err := loadWithRavenYAML(t, `auth_server_url: https://idp.example.com/auth
organization_unit_domains:
  ou-1: [example.com]
  ou-2: [example.com]
`)
	if err == nil {
		t.Fatal("LoadConfig succeeded with a domain listed under two organization units")
	}
}
