// SigV4 authentication.
//
// ponytail: stub — verifies the access key only, not the HMAC signature.
// Full canonical-request + HMAC chain verification lands in Task 4.
// The parse step is real; Task 4 only replaces verifySigV4's body.
package api

import (
	"net/http"
	"strings"
)

// sigv4Auth holds fields parsed from an AWS Signature Version 4
// Authorization header.
type sigv4Auth struct {
	accessKey       string // Credential field, before first "/"
	credentialScope string // date/region/service/aws4_request
	signedHeaders   string
	signature       string
}

// parseSigV4 extracts auth fields from the Authorization header.
// Returns ok=false if the header is absent or malformed.
func parseSigV4(r *http.Request) (auth sigv4Auth, ok bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "AWS4-HMAC-SHA256 ") {
		return sigv4Auth{}, false
	}
	for _, field := range strings.Split(strings.TrimPrefix(h, "AWS4-HMAC-SHA256 "), ",") {
		kv := strings.SplitN(strings.TrimSpace(field), "=", 2)
		if len(kv) != 2 {
			return sigv4Auth{}, false
		}
		switch kv[0] {
		case "Credential":
			parts := strings.SplitN(kv[1], "/", 2)
			auth.accessKey = parts[0]
			if len(parts) == 2 {
				auth.credentialScope = parts[1]
			}
		case "SignedHeaders":
			auth.signedHeaders = kv[1]
		case "Signature":
			auth.signature = kv[1]
		}
	}
	if auth.accessKey == "" || auth.signature == "" {
		return sigv4Auth{}, false
	}
	return auth, true
}

// verifySigV4 authenticates the request against configured credentials.
func (h *Handler) verifySigV4(r *http.Request) bool {
	auth, ok := parseSigV4(r)
	return ok && auth.accessKey == h.accessKey
}
