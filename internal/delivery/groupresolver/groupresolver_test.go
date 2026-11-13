package groupresolver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

	gr := NewGroupResolver(server.URL)
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
				"groups": []map[string]string{
					{"id": "group-eng", "name": "engineering"},
				},
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

		// A child OU whose domain is unrelated to its grouping parent.
		case "/organization-units/ou-silver":
			resp := map[string]interface{}{
				"id":     "ou-silver",
				"handle": "silver.lsf.lk",
				"parent": "ou-foundations",
			}
			_ = writeResponseJSON(w, resp)

		case "/organization-units/ou-foundations":
			t.Errorf("parent OU fetched; an OU's domain must not depend on its parents")
			w.WriteHeader(http.StatusInternalServerError)

		case "/organization-units/ou-1":
			resp := map[string]interface{}{
				"id":     "ou-1",
				"handle": "example.com",
				"parent": nil,
			}
			_ = writeResponseJSON(w, resp)

		case "/organization-units/ou-2":
			resp := map[string]interface{}{
				"id":     "ou-2",
				"handle": "example.net",
				"parent": nil,
			}
			_ = writeResponseJSON(w, resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	gr := NewGroupResolver(server.URL)
	members, err := gr.ResolveGroupMembers("engineering")

	if err != nil {
		t.Fatalf("ResolveGroupMembers() error = %v", err)
	}

	expectedMembers := map[string]bool{
		"alice@example.com":   true,
		"bob@example.net":     true,
		"carol@silver.lsf.lk": true,
	}

	if len(members) != len(expectedMembers) {
		t.Errorf("ResolveGroupMembers() returned %d members, expected %d", len(members), len(expectedMembers))
	}

	for _, member := range members {
		if !expectedMembers[member] {
			t.Errorf("unexpected member: %s", member)
		}
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

	gr := NewGroupResolver(server.URL)
	_, err := gr.ResolveGroupMembers("nonexistent")

	if err == nil {
		t.Error("ResolveGroupMembers() expected error for nonexistent group")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("ResolveGroupMembers() error = %v, expected 'not found'", err)
	}
}
