// Black-box tests for multipart upload endpoints.
package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/c0ldheat/jario/internal/api"
	"github.com/c0ldheat/jario/internal/store"
)

func newMultipartHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st := store.New(t.TempDir())
	return api.NewHandler(st, "testkey", "testsecret"), st
}

// initiate sends POST /{bucket}/{key}?uploads and returns the upload ID.
func initiate(t *testing.T, h http.Handler, bucket, key string) string {
	t.Helper()
	req := authReq("POST", "/"+bucket+"/"+key+"?uploads", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initiate: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<UploadId>") {
		t.Fatalf("initiate: missing UploadId in response: %s", body)
	}
	start := strings.Index(body, "<UploadId>") + len("<UploadId>")
	end := strings.Index(body, "</UploadId>")
	return body[start:end]
}

func TestMultipartUploadFullCycle(t *testing.T) {
	h, st := newMultipartHandler(t)

	// 1. Create bucket via PUT.
	st.CreateBucket("mbucket")

	// 2. Initiate.
	uploadID := initiate(t, h, "mbucket", "multipart.dat")

	// 3. Upload parts — capture ETags from each upload.
	var etags [3]string
	for i, data := range []string{"hello", " ", "world"} {
		partNum := i + 1
		req := authReq("PUT", "/mbucket/multipart.dat?uploadId="+uploadID+"&partNumber="+itoa(partNum), strings.NewReader(data))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("upload part %d: %d: %s", partNum, rec.Code, rec.Body.String())
		}
		etags[i] = rec.Header().Get("ETag")
		if etags[i] == "" {
			t.Fatalf("upload part %d: missing ETag", partNum)
		}
	}

	// 4. List parts.
	req := authReq("GET", "/mbucket/multipart.dat?uploadId="+uploadID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list parts: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<PartNumber>1</PartNumber>") {
		t.Fatalf("list parts: missing part 1: %s", body)
	}
	if !strings.Contains(body, "<PartNumber>3</PartNumber>") {
		t.Fatalf("list parts: missing part 3: %s", body)
	}

	// 5. Complete with the ETags from step 3.
	completeBody := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>` + etags[0] + `</ETag></Part><Part><PartNumber>2</PartNumber><ETag>` + etags[1] + `</ETag></Part><Part><PartNumber>3</PartNumber><ETag>` + etags[2] + `</ETag></Part></CompleteMultipartUpload>`
	req = authReq("POST", "/mbucket/multipart.dat?uploadId="+uploadID, strings.NewReader(completeBody))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: %d: %s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if !strings.Contains(body, "multipart.dat") {
		t.Fatalf("complete: missing key in response: %s", body)
	}

	// 6. Verify object exists.
	req = authReq("HEAD", "/mbucket/multipart.dat", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("head object after complete: %d", rec.Code)
	}

	// 7. Verify upload is gone from list.
	req = authReq("GET", "/mbucket?uploads", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list uploads: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), uploadID) {
		t.Fatalf("upload should be gone after complete: %s", rec.Body.String())
	}
}

func TestMultipartAbort(t *testing.T) {
	h, st := newMultipartHandler(t)
	st.CreateBucket("b")
	uploadID := initiate(t, h, "b", "abort.dat")

	// Upload a part.
	req := authReq("PUT", "/b/abort.dat?uploadId="+uploadID+"&partNumber=1", strings.NewReader("data"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload part: %d", rec.Code)
	}

	// Abort.
	req = authReq("DELETE", "/b/abort.dat?uploadId="+uploadID, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("abort: expected 204, got %d", rec.Code)
	}

	// Upload is gone.
	req = authReq("GET", "/b?uploads", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), uploadID) {
		t.Fatalf("upload should be gone after abort: %s", rec.Body.String())
	}
}

func TestMultipartInvalidUploadID(t *testing.T) {
	h, st := newMultipartHandler(t)
	st.CreateBucket("b")

	// Upload part with nonexistent ID.
	req := authReq("PUT", "/b/k?uploadId=fake&partNumber=1", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for bad uploadID, got %d", rec.Code)
	}

	// Complete with nonexistent ID.
	body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>x</ETag></Part></CompleteMultipartUpload>`
	req = authReq("POST", "/b/k?uploadId=fake", strings.NewReader(body))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for complete bad ID, got %d", rec.Code)
	}
}

func TestMultipartListUploads(t *testing.T) {
	h, st := newMultipartHandler(t)
	st.CreateBucket("b")
	initiate(t, h, "b", "file1.dat")
	initiate(t, h, "b", "file2.dat")

	req := authReq("GET", "/b?uploads", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list uploads: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "file1.dat") || !strings.Contains(body, "file2.dat") {
		t.Fatalf("expected both uploads in list: %s", body)
	}
	if !strings.Contains(body, "ListMultipartUploadsResult") {
		t.Fatalf("expected ListMultipartUploadsResult XML: %s", body)
	}
}

func TestMultipartPartNumberBounds(t *testing.T) {
	h, st := newMultipartHandler(t)
	st.CreateBucket("b")
	uploadID := initiate(t, h, "b", "k")

	// Part number 0 should fail.
	req := authReq("PUT", "/b/k?uploadId="+uploadID+"&partNumber=0", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("part 0: expected 400, got %d", rec.Code)
	}

	// Part number 10001 should fail.
	req = authReq("PUT", "/b/k?uploadId="+uploadID+"&partNumber=10001", strings.NewReader("x"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("part 10001: expected 400, got %d", rec.Code)
	}
}

func TestMultipartCompleteEmptyParts(t *testing.T) {
	h, st := newMultipartHandler(t)
	st.CreateBucket("b")
	uploadID := initiate(t, h, "b", "k")

	req := authReq("POST", "/b/k?uploadId="+uploadID, strings.NewReader("<CompleteMultipartUpload></CompleteMultipartUpload>"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty parts, got %d", rec.Code)
	}
}

// itoa is a tiny int-to-string helper to avoid importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestAbortStaleUploads(t *testing.T) {
	h, st := newMultipartHandler(t)
	st.CreateBucket("b")

	// Create two uploads.
	id1 := initiate(t, h, "b", "stale1.dat")
	id2 := initiate(t, h, "b", "stale2.dat")

	// Abort uploads older than 0s — both should be cleaned up.
	n := st.AbortStaleUploads(0)
	if n != 2 {
		t.Fatalf("expected 2 aborted, got %d", n)
	}

	// Both uploads should be gone.
	req := authReq("GET", "/b?uploads", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, id1) || strings.Contains(body, id2) {
		t.Fatalf("uploads should be gone after stale abort: %s", body)
	}
}

func TestAbortStaleUploadsNoneStale(t *testing.T) {
	h, st := newMultipartHandler(t)
	st.CreateBucket("b")
	initiate(t, h, "b", "fresh.dat")

	// Nothing is stale if maxAge is 24h.
	n := st.AbortStaleUploads(24 * time.Hour)
	if n != 0 {
		t.Fatalf("expected 0 aborted, got %d", n)
	}

	// Upload should still be there.
	req := authReq("GET", "/b?uploads", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "fresh.dat") {
		t.Fatalf("fresh upload should still exist: %s", rec.Body.String())
	}
}
