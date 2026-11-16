// Package maildomain maps organization units to the mail domains they own.
//
// The mapping comes from configuration (organization_unit_domains in raven.yaml), keyed by
// OU ID. An OU can own several domains, a domain belongs to at most one OU, and an OU that
// isn't in the mapping owns no mail domain.
package maildomain

import (
	"fmt"
	"sort"
	"strings"
)

// Map is a validated OU → domains mapping. A nil *Map is an empty mapping.
type Map struct {
	byOU     map[string][]string
	byDomain map[string]string
}

// NewMap validates raw, keyed by OU ID, and normalizes its domains. It fails when an OU ID is
// blank, an OU lists no domains, a domain is not a valid domain name, or two OUs list the
// same domain.
func NewMap(raw map[string][]string) (*Map, error) {
	m := &Map{
		byOU:     make(map[string][]string, len(raw)),
		byDomain: make(map[string]string),
	}

	// Sorted, so a config with several problems always reports the same one.
	ouIDs := make([]string, 0, len(raw))
	for ouID := range raw {
		ouIDs = append(ouIDs, ouID)
	}
	sort.Strings(ouIDs)

	for _, rawID := range ouIDs {
		ouID := strings.TrimSpace(rawID)
		if ouID == "" {
			return nil, fmt.Errorf("organization_unit_domains: blank organization unit ID")
		}
		if _, dup := m.byOU[ouID]; dup {
			return nil, fmt.Errorf("organization_unit_domains: organization unit %q is listed more than once", ouID)
		}

		rawDomains := raw[rawID]
		if len(rawDomains) == 0 {
			return nil, fmt.Errorf("organization_unit_domains: organization unit %q lists no domains", ouID)
		}

		domains := make([]string, 0, len(rawDomains))
		for _, rawDomain := range rawDomains {
			domain, ok := Normalize(rawDomain)
			if !ok {
				return nil, fmt.Errorf("organization_unit_domains: organization unit %q: %q is not a domain name", ouID, rawDomain)
			}
			if owner, taken := m.byDomain[domain]; taken {
				if owner == ouID {
					continue // listed twice under the same OU: harmless
				}
				return nil, fmt.Errorf("organization_unit_domains: domain %q is listed under both %q and %q", domain, owner, ouID)
			}
			m.byDomain[domain] = ouID
			domains = append(domains, domain)
		}
		m.byOU[ouID] = domains
	}

	return m, nil
}

// Domains returns the domains the OU owns, in configured order, or nil if it owns none.
func (m *Map) Domains(ouID string) []string {
	if m == nil {
		return nil
	}
	domains := m.byOU[strings.TrimSpace(ouID)]
	if len(domains) == 0 {
		return nil
	}
	return append([]string(nil), domains...)
}

// Owns reports whether the OU owns domain.
func (m *Map) Owns(ouID, domain string) bool {
	owner, ok := m.OrgUnit(domain)
	return ok && owner == strings.TrimSpace(ouID)
}

// OrgUnit returns the ID of the OU that owns domain.
func (m *Map) OrgUnit(domain string) (string, bool) {
	if m == nil {
		return "", false
	}
	domain, ok := Normalize(domain)
	if !ok {
		return "", false
	}
	ouID, found := m.byDomain[domain]
	return ouID, found
}

// Len returns the number of domains in the mapping.
func (m *Map) Len() int {
	if m == nil {
		return 0
	}
	return len(m.byDomain)
}

// DomainFor picks the domain to address an OU's user by. It returns preferred when the OU
// owns it, otherwise the OU's only domain. It fails when the OU owns no domain, or owns
// several and preferred is none of them.
func (m *Map) DomainFor(ouID, preferred string) (string, error) {
	if preferred != "" && m.Owns(ouID, preferred) {
		domain, _ := Normalize(preferred)
		return domain, nil
	}

	domains := m.Domains(ouID)
	switch len(domains) {
	case 0:
		return "", fmt.Errorf("organization unit %q owns no mail domain", ouID)
	case 1:
		return domains[0], nil
	default:
		if preferred != "" {
			return "", fmt.Errorf("organization unit %q owns several mail domains %v, none of them %q", ouID, domains, preferred)
		}
		return "", fmt.Errorf("organization unit %q owns several mail domains %v and none was asked for", ouID, domains)
	}
}

// Normalize returns domain lowercased and without a trailing dot. It reports false when
// domain is not a domain name.
func Normalize(domain string) (string, bool) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")

	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, label := range labels {
		if !validLabel(label) {
			return "", false
		}
	}

	return domain, true
}

// validLabel also rejects "/" and "\", which keeps the domain safe to put in a URL path.
func validLabel(label string) bool {
	if label == "" {
		return false
	}
	for _, c := range label {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
