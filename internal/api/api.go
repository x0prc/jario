// Package api implements the S3-compatible HTTP handler.
// Routes bucket ops, object ops, and multipart upload lifecycle
// to the store layer. SigV4 auth at the middleware level.
package api

import (
	"net/http"
	"strings"

	"github.com/c0ldheat/jario/internal/store"
)

// Handler routes HTTP requests to the storage engine.
type Handler struct {
	st        *store.Store
	accessKey string
	secretKey string
}

// NewHandler creates an S3 handler. auth is stubbed —
// full SigV4 arrives with Task 4.
func NewHandler(st *store.Store, accessKey, secretKey string) http.Handler {
	return &Handler{st: st, accessKey: accessKey, secretKey: secretKey}
}

// ServeHTTP dispatches S3 requests by path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	if path == "" && r.Method == http.MethodGet {
		h.listBuckets(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	bucket := parts[0]
	key := ""
	if len(parts) > 1 {
		key = parts[1]
	}

	if key == "" {
		h.handleBucket(w, r, bucket)
	} else {
		h.handleObject(w, r, bucket, key)
	}
}

func (h *Handler) handleBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		if err := h.st.CreateBucket(bucket); err != nil {
			h.s3Error(w, "BucketAlreadyExists", err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := h.st.DeleteBucket(bucket); err != nil {
			h.s3Error(w, "NoSuchBucket", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	// object ops in Task 3
	w.WriteHeader(http.StatusNotImplemented)
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}
