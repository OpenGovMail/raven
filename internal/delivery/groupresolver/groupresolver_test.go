package groupresolver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"raven/internal/maildomain"
)

func writeResponseJSON(w http.ResponseWriter, payload any) bool {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return false
	}

	return true
}

func createTestJWT(exp int64) string {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payload := map[string]interface{}{"exp": exp}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	return headerB64 + "." + payloadB64 + ".dummy-signature"
}

func TestExtractJWTExpiry(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		wantErr    bool
		wantExpiry bool
	}{
		{
			name:       "valid JWT with exp claim",
			token:      createTestJWT(time.Now().Add(1 * time.Hour).Unix()),
			wantErr:    false,
			wantExpiry: true,
		},
		{
			name:    "invalid JWT format",
			token:   "invalid.jwt",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, err := extractJWTExpiry(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractJWTExpiry() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && exp.IsZero() != !tt.wantExpiry {
				t.Errorf("extractJWTExpiry() returned zero time, wanted non-zero")
			}
		})
	}
}

func TestGroupResolverAuthentication(t *testing.T) {
	// Raven authenticates to the identity server as a service account.
	t.Setenv("IDP_CLIENT_ID", "RAVEN_SYSTEM")
	t.Setenv("IDP_CLIENT_SECRET", "secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" && r.Method == http.MethodPost {
			exp := time.Now().Add(1 * time.Hour).Unix()
			_ = writeResponseJSON(w, map[string]interface{}{
				"access_token": createTestJWT(exp),
				"expires_in":   3600,
			})
		}
	}))
	defer server.Close()

	gr := NewGroupResolver(server.URL, nil)
	assertion, err := gr.getOrFreshAssertion()

	if err != nil {
		t.Fatalf("getOrFreshAssertion() error = %v", err)
	}

	if assertion == "" {
		t.Error("getOrFreshAssertion() returned empty assertion")
	}

	assertion2, err := gr.getOrFreshAssertion()
	if err != nil {
		t.Fatalf("getOrFreshAssertion() (second call) error = %v", err)
	}

	if assertion != assertion2 {
		t.Error("cached assertion was not reused")
	}
}

func TestGroupMemberResolution(t *testing.T) {
	// Raven authenticates to the identity server as a service account.
	t.Setenv("IDP_CLIENT_ID", "RAVEN_SYSTEM")
	t.Setenv("IDP_CLIENT_SECRET", "secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			exp := time.Now().Add(1 * time.Hour).Unix()
			_ = writeResponseJSON(w, map[string]interface{}{
				"access_token": createTestJWT(exp),
				"expires_in":   3600,
			})
		case "/groups":
			auth := r.Header.Get("Authorization")
			if auth == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			resp := map[string]interface{}{
				// Two OUs each have a group called "engineering".
				"groups": []map[string]string{
					{"id": "group-eng-net", "name": "engineering", "ouId": "ou-2"},
					{"id": "group-eng", "name": "Engineering", "ouId": "ou-1"},
				},
			}
			_ = writeResponseJSON(w, resp)

		case "/groups/group-eng-net/members":
			resp := map[string]interface{}{
				"members": []map[string]string{{"id": "user-1", "type": "user"}},
			}
			_ = writeResponseJSON(w, resp)

		case "/groups/group-eng/members":
			auth := r.Header.Get("Authorization")
			if auth == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			resp := map[string]interface{}{
				"members": []map[string]string{
					{"id": "user-1", "type": "user"},
					{"id": "user-2", "type": "user"},
					{"id": "user-3", "type": "user"},
				},
			}
			_ = writeResponseJSON(w, resp)

		case "/users/user-1":
			resp := map[string]interface{}{
				"id":   "user-1",
				"ouId": "ou-1",
				"attributes": map[string]string{
					"username": "alice",
				},
			}
			_ = writeResponseJSON(w, resp)

		case "/users/user-2":
			resp := map[string]interface{}{
				"id":   "user-2",
				"ouId": "ou-2",
				"attributes": map[string]string{
					"username": "bob",
				},
			}
			_ = writeResponseJSON(w, resp)

		case "/users/user-3":
			resp := map[string]interface{}{
				"id":   "user-3",
				"ouId": "ou-silver",
				"attributes": map[string]string{
					"username": "carol",
				},
			}
			_ = writeResponseJSON(w, resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// The domains come from configuration; the IdP is never asked about organization units.
	mailDomains, err := maildomain.NewMap(map[string][]string{
		"ou-1":      {"example.com"},
		"ou-2":      {"example.net"},
		"ou-silver": {"silver.lsf.lk", "lsf.lk"},
	})
	if err != nil {
		t.Fatalf("NewMap() error = %v", err)
	}
	gr := NewGroupResolver(server.URL, mailDomains)

	tests := []struct {
		name        string
		groupDomain string
		want        []string
		wantErr     string
	}{
		{
			// Members keep their own OU's domain, whatever the group's domain. carol's OU
			// owns several domains, so she gets the first one listed.
			name:        "members addressed at their own OU's domain",
			groupDomain: "example.com",
			want:        []string{"alice@example.com", "bob@example.net", "carol@silver.lsf.lk"},
		},
		{
			// The group is the one in the OU that owns example.net, not ou-1's group.
			name:        "group taken from the OU that owns the group domain",
			groupDomain: "example.net",
			want:        []string{"alice@example.com"},
		},
		{
			name:        "no group of that name in the OU that owns the group domain",
			groupDomain: "lsf.lk",
			wantErr:     "not found in organization unit ou-silver",
		},
		{
			name:        "group domain not in the mapping",
			groupDomain: "unknown.example",
			wantErr:     "not in organization_unit_domains",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			members, err := gr.ResolveGroupMembers("engineering", tt.groupDomain)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResolveGroupMembers() error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveGroupMembers() error = %v", err)
			}
			sort.Strings(members)
			if !reflect.DeepEqual(members, tt.want) {
				t.Errorf("ResolveGroupMembers() = %v, want %v", members, tt.want)
			}
		})
	}
}

func TestGroupNotFound(t *testing.T) {
	// Raven authenticates to the identity server as a service account.
	t.Setenv("IDP_CLIENT_ID", "RAVEN_SYSTEM")
	t.Setenv("IDP_CLIENT_SECRET", "secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			exp := time.Now().Add(1 * time.Hour).Unix()
			_ = writeResponseJSON(w, map[string]interface{}{
				"access_token": createTestJWT(exp),
				"expires_in":   3600,
			})
		case "/groups":
			auth := r.Header.Get("Authorization")
			if auth == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			resp := map[string]interface{}{
				"groups": []map[string]string{},
			}
			_ = writeResponseJSON(w, resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	mailDomains, err := maildomain.NewMap(map[string][]string{"ou-1": {"example.com"}})
	if err != nil {
		t.Fatalf("NewMap() error = %v", err)
	}
	gr := NewGroupResolver(server.URL, mailDomains)
	_, err = gr.ResolveGroupMembers("nonexistent", "example.com")

	if err == nil {
		t.Error("ResolveGroupMembers() expected error for nonexistent group")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("ResolveGroupMembers() error = %v, expected 'not found'", err)
	}
}
