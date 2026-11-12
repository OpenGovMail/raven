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
	"sync"
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

	mu       sync.Mutex
	children map[string][]fakeOU
	gate     chan struct{}
	token    string
	// onList runs before each authorised response is written, with the request count.
	onList func(n int32)
}

// rotateToken makes the fake accept only token and hands it to the client.
func (ft *fakeThunder) rotateToken(token string) {
	ft.mu.Lock()
	ft.token = token
	ft.mu.Unlock()
	SetAuth(&Auth{BearerToken: token, ExpiresAt: time.Now().Add(time.Hour)})
}

// setRoots replaces the tree the fake serves.
func (ft *fakeThunder) setRoots(roots []fakeOU) {
	children := map[string][]fakeOU{"": roots}
	var index func([]fakeOU)
	index = func(ous []fakeOU) {
		for _, ou := range ous {
			children[ou.id] = ou.children
			index(ou.children)
		}
	}
	index(roots)

	ft.mu.Lock()
	ft.children = children
	ft.mu.Unlock()
}

// hold makes every request wait until the returned func is called. Calling it again is safe,
// so a test can defer it and a failed assertion doesn't leave the server stuck.
func (ft *fakeThunder) hold() (release func()) {
	gate := make(chan struct{})
	ft.mu.Lock()
	ft.gate = gate
	ft.mu.Unlock()

	return sync.OnceFunc(func() {
		ft.mu.Lock()
		ft.gate = nil
		ft.mu.Unlock()
		close(gate)
	})
}

// newFakeThunder serves the OU listing endpoints for roots, paginated at pageSize.
func newFakeThunder(t *testing.T, roots []fakeOU, pageSize int) *fakeThunder {
	t.Helper()

	ft := &fakeThunder{token: "test-token"}
	ft.setRoots(roots)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ft.mu.Lock()
		gate, children, token, onList := ft.gate, ft.children, ft.token, ft.onList
		ft.mu.Unlock()
		if gate != nil {
			<-gate
		}

		n := ft.listCalls.Add(1)
		if ft.fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if onList != nil {
			onList(n)
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
		indexByDomain, indexErr, indexBuiltAt = nil, nil, time.Time{}
		indexMu.Unlock()
		SetAuth(nil)
	}
	reset()
	SetAuth(&Auth{BearerToken: "test-token", ExpiresAt: time.Now().Add(time.Hour)})
	t.Cleanup(reset)
}

// refresh rebuilds the index as the service's timer does.
func (ft *fakeThunder) refresh() error {
	return RefreshIndex(ft.host, ft.port, 300)
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

func TestIndexWalkUsesRenewedToken(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{
		{id: "ou-1", handle: "one.lk", children: []fakeOU{{id: "ou-2", handle: "two.lk"}}},
	}, 0)
	ft.mu.Lock()
	ft.onList = func(n int32) {
		if n == 1 {
			ft.rotateToken("renewed-token")
		}
	}
	ft.mu.Unlock()

	if id, found := ft.lookup(t, "two.lk"); !found || id != "ou-2" {
		t.Errorf("lookup(two.lk) after the token changed mid-walk = (%q, %v), want (ou-2, true)", id, found)
	}
}

func TestLookupsDoNotCallThunderOnceBuilt(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	ft.lookup(t, "one.lk")
	calls := ft.listCalls.Load()
	ft.lookup(t, "one.lk")
	ft.lookup(t, "other.lk")

	if got := ft.listCalls.Load(); got != calls {
		t.Errorf("Thunder called %d more times after the index was built, want 0", got-calls)
	}
}

func TestIndexServesPreviousIndexWhenRefreshFails(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	ft.lookup(t, "one.lk")
	ft.fail.Store(true)

	if err := ft.refresh(); err == nil {
		t.Fatal("RefreshIndex() error = nil, want an error when Thunder fails")
	}
	if id, found := ft.lookup(t, "one.lk"); !found || id != "ou-1" {
		t.Errorf("lookup(one.lk) after failed refresh = (%q, %v), want (ou-1, true)", id, found)
	}
}

func TestIndexRefreshDoesNotBlockLookups(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	ft.lookup(t, "one.lk")
	release := ft.hold()
	defer release()
	refreshed := make(chan error, 1)
	go func() { refreshed <- ft.refresh() }()

	start := time.Now()
	for _, domain := range []string{"one.lk", "other.lk", "one.lk"} {
		if _, found := ft.lookup(t, domain); found != (domain == "one.lk") {
			t.Fatalf("lookup(%s) during refresh found = %v", domain, found)
		}
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("lookups took %v while a refresh was stalled, want them answered from the old index", elapsed)
	}

	release()
	if err := <-refreshed; err != nil {
		t.Fatalf("RefreshIndex() error: %v", err)
	}
}

func TestIndexRefreshPicksUpChanges(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	ft.lookup(t, "one.lk")
	ft.setRoots([]fakeOU{{id: "ou-2", handle: "two.lk"}})
	if err := ft.refresh(); err != nil {
		t.Fatalf("RefreshIndex() error: %v", err)
	}

	if id, found := ft.lookup(t, "two.lk"); !found || id != "ou-2" {
		t.Errorf("lookup(two.lk) after refresh = (%q, %v), want (ou-2, true)", id, found)
	}
	if _, found := ft.lookup(t, "one.lk"); found {
		t.Error("lookup(one.lk) still found after its OU was removed")
	}
}

func TestFailedFirstBuildIsNotRepeatedAtOnce(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)
	ft.fail.Store(true)

	if _, _, err := lookupOrgUnit("one.lk", ft.host, ft.port, 300); err == nil {
		t.Fatal("lookupOrgUnit() error = nil, want an error when Thunder fails")
	}
	calls := ft.listCalls.Load()

	if _, _, err := lookupOrgUnit("one.lk", ft.host, ft.port, 300); err == nil {
		t.Fatal("second lookupOrgUnit() error = nil, want the first build's error")
	}
	if got := ft.listCalls.Load(); got != calls {
		t.Errorf("second lookup called Thunder %d times right after a failed build, want 0", got-calls)
	}

	// Once the retry delay has passed, a lookup builds the index.
	ft.fail.Store(false)
	indexMu.Lock()
	indexBuiltAt = time.Now().Add(-failedBuildRetry)
	indexMu.Unlock()

	if id, found := ft.lookup(t, "one.lk"); !found || id != "ou-1" {
		t.Errorf("lookup(one.lk) after the retry delay = (%q, %v), want (ou-1, true)", id, found)
	}
}

func TestLookupDuringWarmIndexSharesTheBuild(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)
	release := ft.hold()

	warmed := make(chan error, 1)
	go func() { warmed <- WarmIndex(ft.host, ft.port, 300) }()

	// Wait until the warm build holds buildMu, so the lookup below queues behind it.
	for buildMu.TryLock() {
		buildMu.Unlock()
		time.Sleep(time.Millisecond)
	}

	type result struct {
		id    string
		found bool
		err   error
	}
	looked := make(chan result, 1)
	go func() {
		id, found, err := lookupOrgUnit("one.lk", ft.host, ft.port, 300)
		looked <- result{id, found, err}
	}()

	release()
	if err := <-warmed; err != nil {
		t.Fatalf("WarmIndex() error: %v", err)
	}
	if r := <-looked; r.err != nil || !r.found || r.id != "ou-1" {
		t.Fatalf("lookupOrgUnit(one.lk) = (%q, %v, %v), want (ou-1, true, nil)", r.id, r.found, r.err)
	}

	// One walk of this tree is two requests: the roots and ou-1's children.
	if got := ft.listCalls.Load(); got != 2 {
		t.Errorf("Thunder called %d times, want 2 (one walk)", got)
	}
}

func TestWarmIndex(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	if err := WarmIndex(ft.host, ft.port, 300); err != nil {
		t.Fatalf("WarmIndex() error: %v", err)
	}
	calls := ft.listCalls.Load()

	if id, found := ft.lookup(t, "one.lk"); !found || id != "ou-1" {
		t.Errorf("lookup(one.lk) = (%q, %v), want (ou-1, true)", id, found)
	}
	if got := ft.listCalls.Load(); got != calls {
		t.Errorf("lookup after WarmIndex called Thunder %d times, want 0", got-calls)
	}
}

func TestWarmIndexReportsFailure(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)
	ft.fail.Store(true)

	if err := WarmIndex(ft.host, ft.port, 300); err == nil {
		t.Error("WarmIndex() error = nil, want an error when Thunder fails")
	}
}

func TestWarmIndexAfterLookupDoesNotWalkAgain(t *testing.T) {
	ft := newFakeThunder(t, []fakeOU{{id: "ou-1", handle: "one.lk"}}, 0)

	ft.lookup(t, "one.lk")
	calls := ft.listCalls.Load()

	if err := WarmIndex(ft.host, ft.port, 300); err != nil {
		t.Fatalf("WarmIndex() error: %v", err)
	}
	if got := ft.listCalls.Load(); got != calls {
		t.Errorf("WarmIndex after a lookup built the index called Thunder %d times, want 0", got-calls)
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
