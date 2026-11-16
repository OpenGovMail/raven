package thunder

import (
	"errors"
	"testing"

	"raven/internal/maildomain"
)

func TestDomainLookups(t *testing.T) {
	m, err := maildomain.NewMap(map[string][]string{
		"ou-1": {"example.com", "example.org"},
		"ou-2": {"dept.example.gov"},
	})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	SetMailDomains(m)
	t.Cleanup(func() { SetMailDomains(nil) })

	for domain, wantOU := range map[string]string{
		"example.com":      "ou-1",
		"Example.ORG.":     "ou-1",
		"dept.example.gov": "ou-2",
	} {
		if !ValidateDomain(domain) {
			t.Errorf("ValidateDomain(%q) = false, want true", domain)
		}
		if ou, err := GetOrgUnitIDForDomain(domain); err != nil || ou != wantOU {
			t.Errorf("GetOrgUnitIDForDomain(%q) = (%q, %v), want (%q, nil)", domain, ou, err, wantOU)
		}
	}

	for _, domain := range []string{"other.com", "localhost", ""} {
		if ValidateDomain(domain) {
			t.Errorf("ValidateDomain(%q) = true, want false", domain)
		}
		if _, err := GetOrgUnitIDForDomain(domain); !errors.Is(err, ErrNoSuchDomain) {
			t.Errorf("GetOrgUnitIDForDomain(%q) error = %v, want ErrNoSuchDomain", domain, err)
		}
	}
}

func TestDomainLookupsWithoutMapping(t *testing.T) {
	SetMailDomains(nil)
	if ValidateDomain("example.com") {
		t.Fatal("ValidateDomain() = true with no mapping set")
	}
	if _, err := GetOrgUnitIDForDomain("example.com"); !errors.Is(err, ErrNoSuchDomain) {
		t.Fatalf("GetOrgUnitIDForDomain() error = %v, want ErrNoSuchDomain", err)
	}
}
