package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"raven/internal/blobstorage"
	"raven/internal/maildomain"

	"gopkg.in/yaml.v2"
)

// SASLScope defines where SASL authentication should be applied
type SASLScope string

const (
	// SASLScopeTCPOnly applies SASL authentication only on TCP connections
	SASLScopeTCPOnly SASLScope = "tcp_only"
	// SASLScopeUnixSocketOnly applies SASL authentication only on Unix domain sockets
	SASLScopeUnixSocketOnly SASLScope = "unix_socket_only"
	// SASLScopeAll applies SASL authentication on all connection types (default)
	SASLScopeAll SASLScope = "all"
)

type Config struct {
	Domain                            string             `yaml:"domain"`
	AuthServerURL                     string             `yaml:"auth_server_url"`
	SASLScope                         SASLScope          `yaml:"sasl_scope"`
	OAuthIssuer                       string             `yaml:"oauth_issuer_url"`
	OAuthJWKSURL                      string             `yaml:"oauth_jwks_url"`
	OAuthAudience                     []string           `yaml:"oauth_audience"`
	OAuthSkewSec                      int                `yaml:"oauth_clock_skew_seconds"`
	OAuthClientEmailAuthorizationFile string             `yaml:"oauth_client_email_authorization_file"`
	BlobStorage                       blobstorage.Config `yaml:"blob_storage"`

	// OrganizationUnitDomains maps an organization unit ID to the mail domains it owns.
	OrganizationUnitDomains map[string][]string `yaml:"organization_unit_domains"`
	// MailDomains is OrganizationUnitDomains, validated. LoadConfig fills it in.
	MailDomains *maildomain.Map `yaml:"-"`
}

func LoadConfig() (*Config, error) {
	var cfg Config

	// Try multiple possible paths
	configPaths := []string{
		"/etc/raven/raven.yaml",
		"./config/raven.yaml",
		"./raven.yaml",
		"config/raven.yaml",
	}

	var data []byte
	var err error
	for _, path := range configPaths {
		data, err = os.ReadFile(filepath.Clean(path))
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Set defaults
	cfg.SetDefaults()

	if cfg.MailDomains, err = maildomain.NewMap(cfg.OrganizationUnitDomains); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// SetDefaults sets default values for configuration
func (c *Config) SetDefaults() {
	if c.SASLScope == "" {
		c.SASLScope = SASLScopeAll
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.AuthServerURL == "" {
		return fmt.Errorf("auth_server_url is required")
	}

	// Validate SASL scope
	switch c.SASLScope {
	case SASLScopeTCPOnly, SASLScopeUnixSocketOnly, SASLScopeAll:
		// Valid scope
	default:
		return fmt.Errorf("invalid sasl_scope: %s (must be 'tcp_only', 'unix_socket_only', or 'all')", c.SASLScope)
	}

	return nil
}
