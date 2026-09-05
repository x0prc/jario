package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/c0ldheat/jario/internal/store"
)

func testHandler(t *testing.T) *Handler {
	t.Helper()
	st := store.New(t.TempDir())
	return NewHandler(st, "testkey", "testsecret").(*Handler)
}

// authReq builds a request signed with valid test credentials.
func authReq(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	signRequest(req, "testkey", "testsecret")
	return req
}

// --- auth ---

func TestNoAuthHeader(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PUT", "/bucket", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestWrongAccessKey(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest("PUT", "/bucket", nil)
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=badkey/20240101/us-east-1/s3/aws4_request,SignedHeaders=host,Signature=stub")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// --- bucket ops ---

func TestCreateBucket(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/test-bucket", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateBucketTwice(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/test-bucket", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestDeleteBucket(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("DELETE", "/test-bucket", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestDeleteBucketNonexistent(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("DELETE", "/no-bucket", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestListBuckets(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("alpha")
	h.st.CreateBucket("beta")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<Name>alpha</Name>") || !strings.Contains(body, "<Name>beta</Name>") {
		t.Fatalf("expected both buckets in response, got %s", body)
	}
}

// --- object ops ---

func TestPutObject(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/test-bucket/hello.txt", strings.NewReader("hello")))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || !strings.HasPrefix(etag, `"`) {
		t.Fatalf("expected quoted ETag, got %q", etag)
	}
}

func TestPutObjectNoBucket(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/no-bucket/hello.txt", strings.NewReader("hello")))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGetObject(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	h.st.PutObject("test-bucket", "hello.txt", strings.NewReader("hello world"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/test-bucket/hello.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "hello world" {
		t.Fatalf("expected 'hello world', got %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Length") != "11" {
		t.Fatalf("expected Content-Length 11, got %q", rec.Header().Get("Content-Length"))
	}
}

func TestGetObjectNotFound(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/test-bucket/no-key", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHeadObject(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	h.st.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("HEAD", "/test-bucket/hello.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("expected ETag header")
	}
}

func TestDeleteObject(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	h.st.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("DELETE", "/test-bucket/hello.txt", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestListObjectsV2(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("test-bucket")
	h.st.PutObject("test-bucket", "a/1.txt", strings.NewReader("1"))
	h.st.PutObject("test-bucket", "a/2.txt", strings.NewReader("2"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/test-bucket?list-type=2&prefix=a/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<KeyCount>2</KeyCount>") {
		t.Fatalf("expected KeyCount 2, got %s", body)
	}
	if !strings.Contains(body, "<Key>a/1.txt</Key>") {
		t.Fatalf("expected key a/1.txt, got %s", body)
	}
}
