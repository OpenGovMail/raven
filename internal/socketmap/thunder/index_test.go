package thunder

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeOU struct {
	id       string
	handle   string
	children []fakeOU
}

type fakeThunder struct {
	host, port string
	fail       atomic.Bool
	listCalls  atomic.Int32
}

// newFakeThunder serves the OU listing endpoints for roots, paginated at pageSize.
func newFakeThunder(t *testing.T, roots []fakeOU, pageSize int) *fakeThunder {
	t.Helper()

	children := map[string][]fakeOU{"": roots}
	var index func([]fakeOU)
	index = func(ous []fakeOU) {
		for _, ou := range ous {
			children[ou.id] = ou.children
			index(ou.children)
		}
	}
	index(roots)

	ft := &fakeThunder{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ft.listCalls.Add(1)
		if ft.fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var parent string
		switch {
		case r.URL.Path == "/organization-units":
		case strings.HasPrefix(r.URL.Path, "/organization-units/") && strings.HasSuffix(r.URL.Path, "/ous"):
			parent = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/organization-units/"), "/ous")
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}

		all, ok := children[parent]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		end := len(all)
		if pageSize > 0 && offset+pageSize < end {
			end = offset + pageSize
		}

		page := OrgUnitsResponse{TotalResults: len(all), StartIndex: offset + 1, Count: end - offset, Links: []Link{}}
		for _, ou := range all[offset:end] {
			page.OrganizationUnits = append(page.OrganizationUnits, OrgUnitResponse{ID: ou.id, Handle: ou.handle})
		}
		if end < len(all) {
			page.Links = append(page.Links, Link{Href: fmt.Sprintf("%s?offset=%d&limit=%d", r.URL.Path, end, pageSize), Rel: "next"})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	ft.host, ft.port, err = net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split test server host: %v", err)
	}

	resetIndex(t)
	return ft
}

func resetIndex(t *testing.T) {
	t.Helper()

	reset := func() {
		indexMu.Lock()
		indexByDomain, indexBuiltAt, indexTTL = nil, time.Time{}, 5*time.Minute
		indexMu.Unlock()
		SetAuth(nil)
	}
	reset()
	SetAuth(&Auth{BearerToken: "test-token", ExpiresAt: time.Now().Add(time.Hour)})
	t.Cleanup(reset)
}

func (ft *fakeThunder) lookup(t *testing.T, domain string) (string, bool) {
	t.Helper()

	id, found, err := lookupOrgUnit(domain, ft.host, ft.port, 300)
	if err != nil {
		t.Fatalf("lookupOrgUnit(%q) error: %v", domain, err)
	}
	return id, found
}

func TestIndexResolvesHandlesAnywhereInTheTree(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{
		{id: "ou-foundations", handle: "foundations", children: []fakeOU{
			{id: "ou-silver", handle: "silver.lsf.lk"},
			{id: "ou-af", handle: "af"},
		}},
		{id: "ou-ldf", handle: "ldf.lk"},
		{id: "ou-system", handle: "default"},
	}, 0)

	tests := []struct {
		domain string
		wantID string
	}{
		{domain: "silver.lsf.lk", wantID: "ou-silver"},
		{domain: "ldf.lk", wantID: "ou-ldf"},
		{domain: "Silver.LSF.lk", wantID: "ou-silver"},
		{domain: "lsf.lk"},
		{domain: "foundations"},
		{domain: "af"},
		{domain: "default"},
		{domain: "af.foundations"},
	}

	for _, tt := range tests {
		id, found := ft.lookup(t, tt.domain)
		if found != (tt.wantID != "") || id != tt.wantID {
			t.Errorf("lookup(%q) = (%q, %v), want %q", tt.domain, id, found, tt.wantID)
		}
	}
}

func TestIndexDropsDomainClaimedTwice(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{
		{id: "ou-a", handle: "a", children: []fakeOU{{id: "ou-a-lsf", handle: "lsf.lk"}}},
		{id: "ou-b", handle: "b", children: []fakeOU{{id: "ou-b-lsf", handle: "lsf.lk"}}},
		{id: "ou-ldf", handle: "ldf.lk"},
	}, 0)

	if id, found := ft.lookup(t, "lsf.lk"); found {
		t.Errorf("lookup(lsf.lk) = %q, want not found", id)
	}
	if _, found := ft.lookup(t, "ldf.lk"); !found {
		t.Errorf("lookup(ldf.lk) not found, want found")
	}
}

func TestIndexFollowsPagination(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{
		{id: "ou-1", handle: "one.lk"},
		{id: "ou-2", handle: "two.lk"},
		{id: "ou-3", handle: "grouping", children: []fakeOU{
			{id: "ou-4", handle: "four.lk"},
			{id: "ou-5", handle: "five.lk"},
		}},
	}, 1)

	for domain, want := range map[string]string{"one.lk": "ou-1", "two.lk": "ou-2", "four.lk": "ou-4", "five.lk": "ou-5"} {
		if id, _ := ft.lookup(t, domain); id != want {
			t.Errorf("lookup(%q) = %q, want %q", domain, id, want)
		}
	}
}

func TestIndexIsReusedWithinTTL(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	ft.lookup(t, "one.lk")
	calls := ft.listCalls.Load()
	ft.lookup(t, "one.lk")
	ft.lookup(t, "other.lk")

	if got := ft.listCalls.Load(); got != calls {
		t.Errorf("Thunder called %d more times within the TTL, want 0", got-calls)
	}
}

func TestIndexServesPreviousIndexWhenRefreshFails(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	ft.lookup(t, "one.lk")
	SetIndexTTL(0)
	ft.fail.Store(true)

	if id, found := ft.lookup(t, "one.lk"); !found || id != "ou-1" {
		t.Errorf("lookup(one.lk) after failed refresh = (%q, %v), want (ou-1, true)", id, found)
	}
}

func TestIndexReportsErrorWhenNeverBuilt(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)
	ft.fail.Store(true)

	if _, _, err := lookupOrgUnit("one.lk", ft.host, ft.port, 300); err == nil {
		t.Fatal("lookupOrgUnit() error = nil, want an error when Thunder is unreachable")
	}
	if ok, err := ValidateDomain("one.lk", ft.host, ft.port, 300); ok || err == nil {
		t.Errorf("ValidateDomain() = (%v, %v), want (false, error)", ok, err)
	}
}

func TestGetOrgUnitIDForDomain(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	if id, err := GetOrgUnitIDForDomain("one.lk", ft.host, ft.port, 300); err != nil || id != "ou-1" {
		t.Errorf("GetOrgUnitIDForDomain(one.lk) = (%q, %v), want (ou-1, nil)", id, err)
	}
	if _, err := GetOrgUnitIDForDomain("two.lk", ft.host, ft.port, 300); err == nil {
		t.Error("GetOrgUnitIDForDomain(two.lk) error = nil, want not found")
	}
}
