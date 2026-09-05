// SigV4 verification tests. signRequest is a minimal client-side
// signer — the same algorithm a real SDK implements, trimmed to the
// headers these tests use (host + x-amz-date).
package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// signRequest signs r in place with a valid AWS4-HMAC-SHA256 header.
// Signs the exact request line, path, query, and host header as sent.
func signRequest(r *http.Request, accessKey, secretKey string) {
	now := time.Now().UTC()
	date := now.Format("20060102")
	datetime := now.Format("20060102T150405Z")
	region, service := "us-east-1", "s3"

	payloadHash := sha256Hex(nil) // empty body
	canonicalURI := r.URL.EscapedPath()

	canonicalHeaders := "host:" + r.Host + "\n" + "x-amz-date:" + datetime + "\n"
	signedHeaders := "host;x-amz-date"
	canonicalReq := r.Method + "\n" + canonicalURI + "\n" + r.URL.Query().Encode() + "\n" +
		canonicalHeaders + "\n" + signedHeaders + "\n" + payloadHash

	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + datetime + "\n" + scope + "\n" + sha256Hex([]byte(canonicalReq))

	kSigning := hmacSHA(hmacSHA(hmacSHA(hmacSHA(
		[]byte("AWS4"+secretKey), date), region), service), "aws4_request")
	signature := hex.EncodeToString(hmacSHA(kSigning, stringToSign))

	r.Header.Set("X-Amz-Date", datetime)
	r.Header.Set("X-Amz-Content-Sha256", payloadHash)
	r.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		accessKey, scope, signedHeaders, signature))
}

func TestSigV4ValidSignature(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("bucket")
	req := httptest.NewRequest("PUT", "/bucket/key", nil)
	signRequest(req, "testkey", "testsecret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSigV4WrongSecret(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("bucket")
	req := httptest.NewRequest("PUT", "/bucket/key", nil)
	signRequest(req, "testkey", "wrongsecret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestSigV4TamperedPath(t *testing.T) {
	h := testHandler(t)
	h.st.CreateBucket("bucket")
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
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PUT", "/bucket/key", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}
