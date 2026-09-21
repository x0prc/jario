// Additional API-level tests for versioning, delete markers, and error paths.
package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- versioning API tests ---

func TestAPIPutObjectReturnsVersionId(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/b/key.txt", strings.NewReader("data")))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	vid := rec.Header().Get("X-Amz-Version-Id")
	if vid == "" {
		t.Fatal("expected X-Amz-Version-Id header")
	}
}

func TestAPIGetObjectDeleteMarkerReturns404(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	// Put then delete (creates delete marker).
	st.PutObject("b", "key.txt", strings.NewReader("data"))
	_, _ = st.DeleteObject("b", "key.txt", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/b/key.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if rec.Header().Get("X-Amz-Delete-Marker") != "true" {
		t.Fatal("expected X-Amz-Delete-Marker header")
	}
}

func TestAPIHeadObjectDeleteMarkerReturns404(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	st.PutObject("b", "key.txt", strings.NewReader("data"))
	_, _ = st.DeleteObject("b", "key.txt", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("HEAD", "/b/key.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if rec.Header().Get("X-Amz-Delete-Marker") != "true" {
		t.Fatal("expected X-Amz-Delete-Marker header")
	}
}

func TestAPIGetObjectSpecificVersion(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	_, v1, _ := st.PutObject("b", "key.txt", strings.NewReader("v1"))
	st.PutObject("b", "key.txt", strings.NewReader("v2"))

	// Get the first version specifically.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/b/key.txt?versionId="+v1, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "v1" {
		t.Fatalf("expected 'v1', got %q", rec.Body.String())
	}
}

func TestAPIDeleteSpecificVersion(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	_, v1, _ := st.PutObject("b", "key.txt", strings.NewReader("v1"))
	st.PutObject("b", "key.txt", strings.NewReader("v2"))

	// Delete specific version.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("DELETE", "/b/key.txt?versionId="+v1, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	// v2 should still be accessible.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, authReq("GET", "/b/key.txt", nil))
	if rec2.Code != http.StatusOK || rec2.Body.String() != "v2" {
		t.Fatalf("expected v2 after deleting v1, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// v1 should be gone.
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, authReq("GET", "/b/key.txt?versionId="+v1, nil))
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for deleted version, got %d", rec3.Code)
	}
}

func TestAPIListObjectVersions(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	st.PutObject("b", "a.txt", strings.NewReader("1"))
	st.PutObject("b", "a.txt", strings.NewReader("2"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/b?versions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<Key>a.txt</Key>") {
		t.Fatalf("expected key a.txt in versions response: %s", body)
	}
	if !strings.Contains(body, "<IsLatest>true</IsLatest>") {
		t.Fatalf("expected IsLatest in response: %s", body)
	}
}

// --- error path tests ---

func TestAPIMultipartETagMismatch(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	uploadID := initiate(t, h, "b", "k")
	// Upload part 1.
	req := authReq("PUT", "/b/k?uploadId="+uploadID+"&partNumber=1", strings.NewReader("data"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Complete with wrong ETag.
	body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"wrong</ETag></Part></CompleteMultipartUpload>`
	req = authReq("POST", "/b/k?uploadId="+uploadID, strings.NewReader(body))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("expected error for ETag mismatch")
	}
}

func TestAPIGetObjectVersionNotFound(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("b")
	st.PutObject("b", "key.txt", strings.NewReader("data"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/b/key.txt?versionId=999", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPIHeadBucketNoSuchBucketReturnsXML(t *testing.T) {
	h, _ := newAPIHandler(t)
	req := authReq("HEAD", "/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "NoSuchBucket") {
		t.Fatalf("expected NoSuchBucket in body, got %s", rec.Body.String())
	}
}
