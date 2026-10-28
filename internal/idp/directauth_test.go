package idp

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectAuthSecret_FromEnv(t *testing.T) {
	t.Setenv("IDP_DIRECT_AUTH_SECRET", "  env-secret\n")
	t.Setenv("IDP_DIRECT_AUTH_SECRET_FILE", "")

	if got := DirectAuthSecret(); got != "env-secret" {
		t.Errorf("DirectAuthSecret() = %q, want %q", got, "env-secret")
	}
}

func TestDirectAuthSecret_FromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct_auth_secret")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	t.Setenv("IDP_DIRECT_AUTH_SECRET", "")
	t.Setenv("IDP_DIRECT_AUTH_SECRET_FILE", path)

	if got := DirectAuthSecret(); got != "file-secret" {
		t.Errorf("DirectAuthSecret() = %q, want %q", got, "file-secret")
	}
}

func TestDirectAuthSecret_EnvTakesPrecedenceOverFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct_auth_secret")
	if err := os.WriteFile(path, []byte("file-secret"), 0600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	t.Setenv("IDP_DIRECT_AUTH_SECRET", "env-secret")
	t.Setenv("IDP_DIRECT_AUTH_SECRET_FILE", path)

	if got := DirectAuthSecret(); got != "env-secret" {
		t.Errorf("DirectAuthSecret() = %q, want %q", got, "env-secret")
	}
}

func TestDirectAuthSecret_MissingFile(t *testing.T) {
	t.Setenv("IDP_DIRECT_AUTH_SECRET", "")
	t.Setenv("IDP_DIRECT_AUTH_SECRET_FILE", filepath.Join(t.TempDir(), "absent"))

	if got := DirectAuthSecret(); got != "" {
		t.Errorf("DirectAuthSecret() = %q, want empty", got)
	}
}

func TestDirectAuthSecret_Unset(t *testing.T) {
	t.Setenv("IDP_DIRECT_AUTH_SECRET", "")
	t.Setenv("IDP_DIRECT_AUTH_SECRET_FILE", "")

	if got := DirectAuthSecret(); got != "" {
		t.Errorf("DirectAuthSecret() = %q, want empty", got)
	}
}

func TestIsDirectAuthRejection(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"missing secret", http.StatusUnauthorized, `{"code":"AUTH-4010","message":"Authentication is required"}`, true},
		{"wrong password", http.StatusUnauthorized, `{"code":"AUTHN-1001","message":"Invalid credentials"}`, false},
		{"empty body", http.StatusUnauthorized, ``, false},
		{"not json", http.StatusUnauthorized, `Unauthorized`, false},
		{"other status", http.StatusForbidden, `{"code":"AUTH-4010"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDirectAuthRejection(tt.status, []byte(tt.body)); got != tt.want {
				t.Errorf("IsDirectAuthRejection(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
			}
		})
	}
}
