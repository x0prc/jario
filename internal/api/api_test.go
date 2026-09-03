// S3 routing + bucket operation tests.
// SigV4 auth is stubbed — tests use static credentials.
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/c0ldheat/jario/internal/store"
)

func testHandler(t *testing.T) *Handler {
	st := store.New(t.TempDir())
	return NewHandler(st, "testkey", "testsecret").(*Handler)
}

func TestCreateBucket(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest("PUT", "/test-bucket", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteBucket(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	req := httptest.NewRequest("DELETE", "/test-bucket", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestCreateBucketTwice(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	req := httptest.NewRequest("PUT", "/test-bucket", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// S3 returns 409 Conflict for duplicate bucket
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestDeleteBucketNonexistent(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest("DELETE", "/no-bucket", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// S3 returns 404 for NoSuchBucket
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
