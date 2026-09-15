// Black-box tests for the S3 HTTP handler (auth, bucket ops, object ops).
// signRequest is a minimal client-side signer — the same algorithm a real
// SDK implements, trimmed to the headers these tests use (host + x-amz-date).
package tests

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/c0ldheat/jario/internal/api"
	"github.com/c0ldheat/jario/internal/store"
)

func newAPIHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st := store.New(t.TempDir())
	return api.NewHandler(st, "testkey", "testsecret"), st
}

// authReq builds a request signed with valid test credentials.
func authReq(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	signRequest(req, "testkey", "testsecret")
	return req
}

func testSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func testHMACSHA(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// signRequest signs r in place with a valid AWS4-HMAC-SHA256 header.
// Signs the exact request line, path, query, and host header as sent.
func signRequest(r *http.Request, accessKey, secretKey string) {
	now := time.Now().UTC()
	date := now.Format("20060102")
	datetime := now.Format("20060102T150405Z")
	region, service := "us-east-1", "s3"

	payloadHash := testSHA256Hex(nil) // empty body
	canonicalURI := r.URL.EscapedPath()

	canonicalHeaders := "host:" + r.Host + "\n" + "x-amz-date:" + datetime + "\n"
	signedHeaders := "host;x-amz-date"
	canonicalReq := r.Method + "\n" + canonicalURI + "\n" + r.URL.Query().Encode() + "\n" +
		canonicalHeaders + "\n" + signedHeaders + "\n" + payloadHash

	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + datetime + "\n" + scope + "\n" + testSHA256Hex([]byte(canonicalReq))

	kSigning := testHMACSHA(testHMACSHA(testHMACSHA(testHMACSHA(
		[]byte("AWS4"+secretKey), date), region), service), "aws4_request")
	signature := hex.EncodeToString(testHMACSHA(kSigning, stringToSign))

	r.Header.Set("X-Amz-Date", datetime)
	r.Header.Set("X-Amz-Content-Sha256", payloadHash)
	r.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		accessKey, scope, signedHeaders, signature))
}

// --- auth ---

func TestAPINoAuthHeader(t *testing.T) {
	h, _ := newAPIHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PUT", "/bucket", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestAPIWrongAccessKey(t *testing.T) {
	h, _ := newAPIHandler(t)
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

func TestAPICreateBucket(t *testing.T) {
	h, _ := newAPIHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/test-bucket", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPICreateBucketTwice(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/test-bucket", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestAPIDeleteBucket(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("DELETE", "/test-bucket", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestAPIDeleteBucketNonexistent(t *testing.T) {
	h, _ := newAPIHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("DELETE", "/no-bucket", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPIListBuckets(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("alpha")
	st.CreateBucket("beta")
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

func TestAPIPutObject(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
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

func TestAPIPutObjectNoBucket(t *testing.T) {
	h, _ := newAPIHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("PUT", "/no-bucket/hello.txt", strings.NewReader("hello")))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPIGetObject(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	st.PutObject("test-bucket", "hello.txt", strings.NewReader("hello world"))
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

func TestAPIGetObjectNotFound(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/test-bucket/no-key", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPIHeadObject(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	st.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("HEAD", "/test-bucket/hello.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("expected ETag header")
	}
}

func TestAPIDeleteObject(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	st.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("DELETE", "/test-bucket/hello.txt", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestAPIListObjectsV2(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	st.PutObject("test-bucket", "a/1.txt", strings.NewReader("1"))
	st.PutObject("test-bucket", "a/2.txt", strings.NewReader("2"))
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

func TestAPIListObjectsV2Paginated(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("test-bucket")
	for _, k := range []string{"a/1.txt", "a/2.txt", "a/3.txt"} {
		st.PutObject("test-bucket", k, strings.NewReader("x"))
	}
	// First page: 2 keys, truncated, with a continuation token.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq("GET", "/test-bucket?list-type=2&prefix=a/&max-keys=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<IsTruncated>true</IsTruncated>") {
		t.Fatalf("expected truncated page, got %s", body)
	}
	token := body[strings.Index(body, "<NextContinuationToken>")+len("<NextContinuationToken>"):]
	token = token[:strings.Index(token, "</NextContinuationToken>")]

	// Second page: last key, not truncated.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, authReq("GET", "/test-bucket?list-type=2&prefix=a/&max-keys=2&continuation-token="+token, nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	body2 := rec2.Body.String()
	if !strings.Contains(body2, "<Key>a/3.txt</Key>") || !strings.Contains(body2, "<IsTruncated>false</IsTruncated>") {
		t.Fatalf("expected final page with a/3.txt, got %s", body2)
	}
}

// --- SigV4 ---

func TestSigV4ValidSignature(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("bucket")
	req := httptest.NewRequest("PUT", "/bucket/key", nil)
	signRequest(req, "testkey", "testsecret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSigV4WrongSecret(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("bucket")
	req := httptest.NewRequest("PUT", "/bucket/key", nil)
	signRequest(req, "testkey", "wrongsecret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestSigV4TamperedPath(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("bucket")
	req := httptest.NewRequest("PUT", "/bucket/key", nil)
	signRequest(req, "testkey", "testsecret")
	// attacker modifies the path after signing
	req.URL.Path = "/bucket/other-key"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestSigV4MissingHeader(t *testing.T) {
	h, _ := newAPIHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PUT", "/bucket/key", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// --- error codes ---

func TestHeadBucketExists(t *testing.T) {
	h, st := newAPIHandler(t)
	st.CreateBucket("exists")
	req := authReq("HEAD", "/exists", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHeadBucketNoSuchBucket(t *testing.T) {
	h, _ := newAPIHandler(t)
	req := authReq("HEAD", "/nope", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "NoSuchBucket") {
		t.Fatalf("expected NoSuchBucket, got %s", rec.Body.String())
	}
}

func TestListObjectsNoSuchBucket(t *testing.T) {
	h, _ := newAPIHandler(t)
	req := authReq("GET", "/nope?list-type=2", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "NoSuchBucket") {
		t.Fatalf("expected NoSuchBucket, got %s", rec.Body.String())
	}
}

func TestListBucketsEmptyReturnsCorrectXML(t *testing.T) {
	h, _ := newAPIHandler(t)
	req := authReq("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ListAllMyBucketsResult") {
		t.Fatalf("expected ListAllMyBucketsResult XML, got %s", body)
	}
}
