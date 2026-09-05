// Package api implements the S3-compatible HTTP handler.
// SigV4 verification lives here.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strings"
)

// sigv4Auth holds fields parsed from an AWS4-HMAC-SHA256 Authorization header.
type sigv4Auth struct {
	accessKey     string
	date          string // YYYYMMDD, from Credential scope
	datetime      string // ISO8601 basic format, from X-Amz-Date
	region        string
	service       string
	signedHeaders []string
	signature     string
}

// verifySigV4 validates a request's AWS Signature Version 4.
// Returns false if the header is missing, malformed, or the signature
// does not match.
func (h *Handler) verifySigV4(r *http.Request) bool {
	auth, ok := parseSigV4(r)
	if !ok || auth.accessKey != h.accessKey {
		return false
	}

	payloadHash, err := payloadHash(r)
	if err != nil {
		return false
	}

	expected := computeSignature(h.secretKey, auth, r, payloadHash)
	return subtle.ConstantTimeCompare(
		[]byte(expected), []byte(auth.signature)) == 1
}

// parseSigV4 extracts and validates the Authorization header fields.
func parseSigV4(r *http.Request) (sigv4Auth, bool) {
	var a sigv4Auth

	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "AWS4-HMAC-SHA256 ") {
		return a, false
	}

	for _, f := range strings.Split(strings.TrimPrefix(h, "AWS4-HMAC-SHA256 "), ",") {
		kv := strings.SplitN(strings.TrimSpace(f), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "Credential":
			parts := strings.Split(kv[1], "/")
			if len(parts) != 5 || parts[4] != "aws4_request" {
				return a, false
			}
			a.accessKey, a.date, a.region, a.service = parts[0], parts[1], parts[2], parts[3]
		case "SignedHeaders":
			a.signedHeaders = strings.Split(kv[1], ";")
		case "Signature":
			a.signature = kv[1]
		}
	}

	a.datetime = r.Header.Get("X-Amz-Date")
	if a.accessKey == "" || a.signature == "" || a.datetime == "" {
		return a, false
	}
	return a, true
}

// payloadHash returns the sha256 hex of the request body. Reads and
// replaces r.Body so the handler can still consume it.
// Respects the X-Amz-Content-Sha256 header when present (chunked signing).
func payloadHash(r *http.Request) (string, error) {
	if h := r.Header.Get("X-Amz-Content-Sha256"); h != "" {
		return h, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// computeSignature builds the canonical request, string to sign,
// and derives the signing key from the secret.
func computeSignature(secretKey string, a sigv4Auth, r *http.Request, payloadHash string) string {
	canonicalReq := buildCanonicalRequest(r, a.signedHeaders, payloadHash)
	scope := a.date + "/" + a.region + "/" + a.service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + a.datetime + "\n" + scope + "\n" + sha256Hex([]byte(canonicalReq))

	kDate := hmacSHA([]byte("AWS4"+secretKey), a.date)
	kRegion := hmacSHA(kDate, a.region)
	kService := hmacSHA(kRegion, a.service)
	kSigning := hmacSHA(kService, "aws4_request")
	return hex.EncodeToString(hmacSHA(kSigning, stringToSign))
}

// buildCanonicalRequest assembles the canonical request string per spec.
func buildCanonicalRequest(r *http.Request, signedHeaders []string, payloadHash string) string {
	headers := make(map[string]string)
	headers["host"] = r.Host
	for _, h := range signedHeaders {
		if h == "host" {
			continue
		}
		headers[h] = strings.TrimSpace(r.Header.Get(h))
	}

	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}

	return strings.Join([]string{
		r.Method,
		r.URL.EscapedPath(),
		r.URL.Query().Encode(),
		canonicalHeaders.String(),
		strings.Join(names, ";"),
		payloadHash,
	}, "\n")
}

func hmacSHA(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
