// Black-box tests for cluster membership (POST /internal/join).
package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/c0ldheat/jario/internal/api"
	"github.com/c0ldheat/jario/internal/store"
)

// fakeJoiner records AddVoter calls and returns a canned error.
type fakeJoiner struct {
	err error
	got []string
}

func (f *fakeJoiner) AddVoter(id, addr string) error {
	f.got = append(f.got, id+"@"+addr)
	return f.err
}

func newJoinHandler(t *testing.T, j api.Joiner) http.Handler {
	t.Helper()
	h := api.NewHandler(mustNewStore(t), "testkey", "testsecret")
	h.(interface{ SetJoiner(api.Joiner) }).SetJoiner(j)
	return h
}

func TestJoinOK(t *testing.T) {
	f := &fakeJoiner{}
	h := newJoinHandler(t, f)
	// No SigV4 header — operator endpoint bypasses auth.
	req := httptest.NewRequest("POST", "/internal/join", strings.NewReader(`{"id":"node2","addr":"host2:9090"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(f.got) != 1 || f.got[0] != "node2@host2:9090" {
		t.Fatalf("joiner not called with id/addr, got %v", f.got)
	}
}

func TestJoinNotLeader(t *testing.T) {
	f := &fakeJoiner{err: store.ErrNotLeader}
	h := newJoinHandler(t, f)
	req := httptest.NewRequest("POST", "/internal/join", strings.NewReader(`{"id":"node2","addr":"host2:9090"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestJoinDisabled(t *testing.T) {
	h := newJoinHandler(t, nil)
	req := httptest.NewRequest("POST", "/internal/join", strings.NewReader(`{"id":"node2","addr":"host2:9090"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestJoinBadBody(t *testing.T) {
	h := newJoinHandler(t, &fakeJoiner{})
	for _, body := range []string{`not-json`, `{"id":"only-id"}`} {
		req := httptest.NewRequest("POST", "/internal/join", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %q, got %d", body, rec.Code)
		}
	}
}

func TestJoinWrongMethod(t *testing.T) {
	h := newJoinHandler(t, &fakeJoiner{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/internal/join", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRequestJoinRoundTrip(t *testing.T) {
	f := &fakeJoiner{}
	srv := httptest.NewServer(newJoinHandler(t, f))
	defer srv.Close()

	// Full URL and bare host:port forms both work.
	if err := api.RequestJoin(srv.URL, "node2", "host2:9090"); err != nil {
		t.Fatalf("RequestJoin: %v", err)
	}
	if err := api.RequestJoin(strings.TrimPrefix(srv.URL, "http://"), "node3", "host3:9090"); err != nil {
		t.Fatalf("RequestJoin bare addr: %v", err)
	}
	if len(f.got) != 2 {
		t.Fatalf("expected 2 joiner calls, got %v", f.got)
	}
}

func TestRequestJoinRejected(t *testing.T) {
	f := &fakeJoiner{err: store.ErrNotLeader}
	srv := httptest.NewServer(newJoinHandler(t, f))
	defer srv.Close()

	if err := api.RequestJoin(srv.URL, "node2", "host2:9090"); err == nil {
		t.Fatal("expected error for 503 response")
	}
}
