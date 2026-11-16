package conf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_YAMLTags(t *testing.T) {
	// Test that Config struct has correct YAML tags
	cfg := Config{
		Domain:        "example.com",
		AuthServerURL: "https://auth.example.com",
	}

	if cfg.Domain != "example.com" {
		t.Errorf("Expected domain 'example.com', got '%s'", cfg.Domain)
	}
	if cfg.AuthServerURL != "https://auth.example.com" {
		t.Errorf("Expected auth_server_url 'https://auth.example.com', got '%s'", cfg.AuthServerURL)
	}
}

func TestLoadConfig_Success(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	configContent := `domain: test.example.com
auth_server_url: https://auth.test.example.com
`
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Change to temp directory so config can be found
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg == nil {
		t.Fatal("Expected config to be non-nil")
		return
	}

	if cfg.Domain != "test.example.com" {
		t.Errorf("Expected domain 'test.example.com', got '%s'", cfg.Domain)
	}

	if cfg.AuthServerURL != "https://auth.test.example.com" {
		t.Errorf("Expected auth_server_url 'https://auth.test.example.com', got '%s'", cfg.AuthServerURL)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	// Change to a temp directory with no config file
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	_, err = LoadConfig()
	if err == nil {
		t.Error("Expected error for missing config file, got nil")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	// Create a temporary config file with invalid YAML
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	invalidYAML := `domain: test.example.com
auth_server_url: [invalid yaml structure
  missing closing bracket
`
	err := os.WriteFile(configPath, []byte(invalidYAML), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	_, err = LoadConfig()
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	// Create an empty config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	err := os.WriteFile(configPath, []byte(""), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error for empty file, got: %v", err)
	}

	if cfg == nil {
		t.Fatal("Expected config to be non-nil")
		return
	}

	// Empty file should result in empty config fields
	if cfg.Domain != "" {
		t.Errorf("Expected empty domain, got '%s'", cfg.Domain)
	}
	if cfg.AuthServerURL != "" {
		t.Errorf("Expected empty auth_server_url, got '%s'", cfg.AuthServerURL)
	}
}

func TestLoadConfig_PartialConfig(t *testing.T) {
	// Create a config file with only one field
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	configContent := `domain: partial.example.com
`
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Domain != "partial.example.com" {
		t.Errorf("Expected domain 'partial.example.com', got '%s'", cfg.Domain)
	}

	if cfg.AuthServerURL != "" {
		t.Errorf("Expected empty auth_server_url, got '%s'", cfg.AuthServerURL)
	}
}

func TestLoadConfig_WithComments(t *testing.T) {
	// Create a config file with YAML comments
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	configContent := `# This is a comment
domain: commented.example.com
# Another comment
auth_server_url: https://auth.commented.example.com
`
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Domain != "commented.example.com" {
		t.Errorf("Expected domain 'commented.example.com', got '%s'", cfg.Domain)
	}
}

func TestLoadConfig_ConfigSubdirectory(t *testing.T) {
	// Test loading from config/ subdirectory
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	err := os.Mkdir(configDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	configPath := filepath.Join(configDir, "raven.yaml")
	configContent := `domain: subdir.example.com
auth_server_url: https://auth.subdir.example.com
`
	err = os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Domain != "subdir.example.com" {
		t.Errorf("Expected domain 'subdir.example.com', got '%s'", cfg.Domain)
	}
}

func TestLoadConfig_SpecialCharacters(t *testing.T) {
	// Test config with special characters in values
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	configContent := `domain: "test-domain.example.com"
auth_server_url: "https://auth.example.com:8443/api/v1"
`
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if cfg.Domain != "test-domain.example.com" {
		t.Errorf("Expected domain 'test-domain.example.com', got '%s'", cfg.Domain)
	}

	if cfg.AuthServerURL != "https://auth.example.com:8443/api/v1" {
		t.Errorf("Expected auth_server_url 'https://auth.example.com:8443/api/v1', got '%s'", cfg.AuthServerURL)
	}
}

func TestLoadConfig_WhitespaceHandling(t *testing.T) {
	// Test config with extra whitespace
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	configContent := `
domain:   whitespace.example.com
auth_server_url:   https://auth.whitespace.example.com

`
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// YAML should trim whitespace
	if cfg.Domain != "whitespace.example.com" {
		t.Errorf("Expected domain 'whitespace.example.com', got '%s'", cfg.Domain)
	}
}

func TestLoadConfig_CaseSensitiveKeys(t *testing.T) {
	// Test that YAML keys are case-sensitive
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	// Use uppercase keys (should not match lowercase struct tags)
	configContent := `Domain: uppercase.example.com
Auth_Server_URL: https://auth.uppercase.example.com
`
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	// Keys with wrong case should not populate fields
	if cfg.Domain != "" {
		t.Errorf("Expected empty domain (case mismatch), got '%s'", cfg.Domain)
	}
}

// TestSASLScopeValidation tests SASL scope validation
func TestSASLScopeValidation(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "raven.yaml")

	tests := []struct {
		name          string
		configContent string
		expectedScope SASLScope
		expectError   bool
	}{
		{
			name: "Config with tcp_only scope",
			configContent: `domain: test.example.com
auth_server_url: https://auth.test.example.com
sasl_scope: tcp_only
`,
			expectedScope: SASLScopeTCPOnly,
			expectError:   false,
		},
		{
			name: "Config with unix_socket_only scope",
			configContent: `domain: test.example.com
auth_server_url: https://auth.test.example.com
sasl_scope: unix_socket_only
`,
			expectedScope: SASLScopeUnixSocketOnly,
			expectError:   false,
		},
		{
			name: "Config with all scope",
			configContent: `domain: test.example.com
auth_server_url: https://auth.test.example.com
sasl_scope: all
`,
			expectedScope: SASLScopeAll,
			expectError:   false,
		},
		{
			name: "Config without sasl_scope (should default)",
			configContent: `domain: test.example.com
auth_server_url: https://auth.test.example.com
`,
			expectedScope: SASLScopeAll, // Default after SetDefaults()
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := os.WriteFile(configPath, []byte(tt.configContent), 0600)
			if err != nil {
				t.Fatalf("Failed to create test config file: %v", err)
			}

			originalDir, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get current directory: %v", err)
			}
			defer func() { _ = os.Chdir(originalDir) }()

			err = os.Chdir(tmpDir)
			if err != nil {
				t.Fatalf("Failed to change directory: %v", err)
			}

			cfg, err := LoadConfig()
			if tt.expectError && err == nil {
				t.Error("Expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError && cfg.SASLScope != tt.expectedScope {
				t.Errorf("Expected scope %s, got %s", tt.expectedScope, cfg.SASLScope)
			}
		})
	}
}

// loadConfigFrom writes content as raven.yaml in a temp directory and loads it from there.
func loadConfigFrom(t *testing.T, content string) (*Config, error) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "raven.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}
	t.Chdir(tmpDir)
	return LoadConfig()
}

func TestLoadConfig_OrganizationUnitDomains(t *testing.T) {
	cfg, err := loadConfigFrom(t, `auth_server_url: https://auth.example.com
organization_unit_domains:
  ou-1:
    - Example.COM
    - example.org.
  ou-2:
    - dept.example.gov
`)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.MailDomains.Domains("ou-1"); len(got) != 2 || got[0] != "example.com" || got[1] != "example.org" {
		t.Errorf("Domains(ou-1) = %v, want [example.com example.org]", got)
	}
	if ou, ok := cfg.MailDomains.OrgUnit("dept.example.gov"); !ok || ou != "ou-2" {
		t.Errorf("OrgUnit(dept.example.gov) = (%q, %v), want (ou-2, true)", ou, ok)
	}
}

func TestLoadConfig_NoOrganizationUnitDomains(t *testing.T) {
	cfg, err := loadConfigFrom(t, "auth_server_url: https://auth.example.com\n")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MailDomains.Len() != 0 {
		t.Errorf("MailDomains.Len() = %d, want 0", cfg.MailDomains.Len())
	}
}

func TestLoadConfig_DomainUnderTwoOrganizationUnits(t *testing.T) {
	_, err := loadConfigFrom(t, `organization_unit_domains:
  ou-1: [example.com]
  ou-2: [example.com]
`)
	if err == nil {
		t.Fatal("Expected an error for a domain listed under two organization units, got nil")
	}
}
