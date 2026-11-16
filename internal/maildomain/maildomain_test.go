package maildomain

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{name: "two labels", input: "lsf.lk", want: "lsf.lk", wantOK: true},
		{name: "three labels", input: "silver.lsf.lk", want: "silver.lsf.lk", wantOK: true},
		{name: "public suffix", input: "example.co.uk", want: "example.co.uk", wantOK: true},
		{name: "deep", input: "a.b.c.opensource.lk", want: "a.b.c.opensource.lk", wantOK: true},
		{name: "mixed case", input: "Silver.LSF.lk", want: "silver.lsf.lk", wantOK: true},
		{name: "trailing dot", input: "lsf.lk.", want: "lsf.lk", wantOK: true},
		{name: "surrounding space", input: " lsf.lk ", want: "lsf.lk", wantOK: true},
		{name: "hyphen and digits", input: "open-data2.lk", want: "open-data2.lk", wantOK: true},
		{name: "container OU", input: "foundations"},
		{name: "system OU", input: "default"},
		{name: "empty", input: ""},
		{name: "only a dot", input: "."},
		{name: "empty label", input: "silver..lsf.lk"},
		{name: "leading dot", input: ".lsf.lk"},
		{name: "slash", input: "lsf.lk/silver"},
		{name: "backslash", input: `lsf.lk\silver`},
		{name: "traversal", input: "../lsf.lk"},
		{name: "plus", input: "lsf.lk+ldf.lk"},
		{name: "inner space", input: "lsf .lk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Normalize(tt.input)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("Normalize(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestNewMap(t *testing.T) {
	m, err := NewMap(map[string][]string{
		"ou-1": {"Example.COM.", " example.org ", "example.com"},
		"ou-2": {"dept.example.gov"},
	})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}

	if got, want := m.Domains("ou-1"), []string{"example.com", "example.org"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Domains(ou-1) = %v, want %v", got, want)
	}
	if got := m.Domains("unmapped"); got != nil {
		t.Errorf("Domains(unmapped) = %v, want nil", got)
	}
	if ou, ok := m.OrgUnit("EXAMPLE.org."); !ok || ou != "ou-1" {
		t.Errorf("OrgUnit(EXAMPLE.org.) = (%q, %v), want (ou-1, true)", ou, ok)
	}
	if _, ok := m.OrgUnit("other.com"); ok {
		t.Errorf("OrgUnit(other.com) found, want not found")
	}
	if !m.Owns("ou-2", "dept.example.gov") || m.Owns("ou-2", "example.com") {
		t.Errorf("Owns gave the wrong answer")
	}
	if m.Len() != 3 {
		t.Errorf("Len() = %d, want 3", m.Len())
	}
}

func TestNewMapRejects(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string][]string
		wantErr string
	}{
		{name: "blank OU", raw: map[string][]string{" ": {"example.com"}}, wantErr: "blank organization unit ID"},
		{name: "no domains", raw: map[string][]string{"ou-1": nil}, wantErr: "lists no domains"},
		{name: "not a domain", raw: map[string][]string{"ou-1": {"localhost"}}, wantErr: "is not a domain name"},
		{name: "shared domain", raw: map[string][]string{"ou-1": {"example.com"}, "ou-2": {"EXAMPLE.com"}}, wantErr: `listed under both "ou-1" and "ou-2"`},
		{name: "same OU twice", raw: map[string][]string{"ou-1": {"a.com"}, " ou-1": {"b.com"}}, wantErr: "listed more than once"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewMap(tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewMap error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNilMap(t *testing.T) {
	var m *Map
	if m.Domains("ou-1") != nil || m.Len() != 0 || m.Owns("ou-1", "example.com") {
		t.Fatal("nil map should be empty")
	}
	if _, ok := m.OrgUnit("example.com"); ok {
		t.Fatal("nil map should own no domain")
	}
	if _, err := m.DomainFor("ou-1", ""); err == nil {
		t.Fatal("nil map should give no domain")
	}
}

func TestDomainFor(t *testing.T) {
	m, err := NewMap(map[string][]string{
		"single": {"one.example"},
		"multi":  {"a.example", "b.example"},
	})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}

	tests := []struct {
		name      string
		ou        string
		preferred string
		want      string
		wantErr   bool
	}{
		{name: "single, nothing preferred", ou: "single", want: "one.example"},
		{name: "single, prefers another OU's domain", ou: "single", preferred: "a.example", want: "one.example"},
		{name: "multi, prefers an owned domain", ou: "multi", preferred: "B.example.", want: "b.example"},
		{name: "multi, nothing preferred", ou: "multi", wantErr: true},
		{name: "multi, prefers a foreign domain", ou: "multi", preferred: "one.example", wantErr: true},
		{name: "unmapped", ou: "nope", preferred: "a.example", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.DomainFor(tt.ou, tt.preferred)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("DomainFor(%q, %q) = (%q, %v), want (%q, err=%v)", tt.ou, tt.preferred, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestPrimaryDomain(t *testing.T) {
	m, err := NewMap(map[string][]string{"multi": {"B.example", "a.example"}})
	if err != nil {
		t.Fatalf("NewMap: %v", err)
	}
	if got, ok := m.PrimaryDomain("multi"); !ok || got != "b.example" {
		t.Errorf("PrimaryDomain(multi) = (%q, %v), want (b.example, true)", got, ok)
	}
	if _, ok := m.PrimaryDomain("unmapped"); ok {
		t.Error("PrimaryDomain(unmapped) found a domain")
	}
}
