// Package api implements the S3-compatible HTTP handler.
// Routes bucket ops, object ops, and (later) multipart uploads
// to the store layer, behind SigV4 auth.
package api

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/c0ldheat/jario/internal/store"
)

// Handler routes HTTP requests to the storage engine.
type Handler struct {
	st        *store.Store
	accessKey string
	secretKey string
	joiner    Joiner // nil disables /internal/join
}

// NewHandler creates an S3 handler with the given credentials.
func NewHandler(st *store.Store, accessKey, secretKey string) http.Handler {
	return &Handler{st: st, accessKey: accessKey, secretKey: secretKey}
}

// ServeHTTP routes operator endpoints directly, everything else
// behind SigV4 auth.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Operator endpoints bypass SigV4 — firewall them in production.
	if strings.HasPrefix(r.URL.Path, "/internal/") {
		h.routeInternal(w, r)
		return
	}
	if !h.verifySigV4(r) {
		h.s3Error(w, "AccessDenied", "access denied")
		return
	}
	h.route(w, r)
}

// route dispatches by path shape: / → buckets, /{bucket} → bucket ops,
// /{bucket}/{key} → object ops.
func (h *Handler) route(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	if path == "" && r.Method == http.MethodGet {
		h.listBuckets(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	bucket := parts[0]
	if len(parts) == 1 {
		h.handleBucket(w, r, bucket)
	} else {
		h.handleObject(w, r, bucket, parts[1])
	}
}

// --- bucket ops ---

func (h *Handler) handleBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		if err := h.st.CreateBucket(bucket); err != nil {
			h.storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := h.st.DeleteBucket(bucket); err != nil {
			h.storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		h.listObjectsV2(w, r, bucket)
	case http.MethodHead:
		h.headBucket(w, bucket)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// headBucket handles HEAD /{bucket} — returns 200 if the bucket exists,
// 404 with NoSuchBucket otherwise.
func (h *Handler) headBucket(w http.ResponseWriter, bucket string) {
	if _, err := h.st.GetBucket(bucket); err != nil {
		h.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	res := listBucketsResult{Xmlns: s3xmlns}
	for _, b := range h.st.ListBuckets() {
		res.Buckets = append(res.Buckets, bucketEntry{
			Name:         b.Name,
			CreationDate: b.CreatedAt.Format(time.RFC3339),
		})
	}
	writeXML(w, res)
}

// listObjectsV2 handles GET /{bucket}?list-type=2 with pagination.
func (h *Handler) listObjectsV2(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := h.st.GetBucket(bucket); err != nil {
		h.storeError(w, err)
		return
	}

	prefix := r.URL.Query().Get("prefix")
	startAfter := r.URL.Query().Get("start-after")
	if ct := r.URL.Query().Get("continuation-token"); ct != "" {
		startAfter = ct
	}
	maxKeys := 1000
	if mk := r.URL.Query().Get("max-keys"); mk != "" {
		if n, err := strconv.Atoi(mk); err == nil && n > 0 && n <= 1000 {
			maxKeys = n
		}
	}

	objects, nextToken := h.st.ListObjectsPaged(bucket, prefix, startAfter, maxKeys)
	res := listBucketResult{
		Xmlns:       s3xmlns,
		Name:        bucket,
		Prefix:      prefix,
		MaxKeys:     maxKeys,
		KeyCount:    len(objects),
		IsTruncated: nextToken != "",
	}
	if nextToken != "" {
		res.NextContinuationToken = nextToken
	}
	for _, o := range objects {
		res.Contents = append(res.Contents, objectEntry{
			Key:          o.Key,
			LastModified: o.CreatedAt.Format(time.RFC3339),
			ETag:         `"` + o.ETag + `"`,
			Size:         o.Size,
			StorageClass: "STANDARD",
		})
	}
	writeXML(w, res)
}

// --- object ops ---

func (h *Handler) handleObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		h.putObject(w, r, bucket, key)
	case http.MethodGet:
		h.getObject(w, r, bucket, key)
	case http.MethodHead:
		h.headObject(w, r, bucket, key)
	case http.MethodDelete:
		h.deleteObject(w, r, bucket, key)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) putObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	etag, err := h.st.PutObject(bucket, key, r.Body)
	if err != nil {
		h.storeError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	rc, meta, err := h.st.GetObject(bucket, key)
	if err != nil {
		h.storeError(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("ETag", `"`+meta.ETag+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	io.Copy(w, rc)
}

func (h *Handler) headObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	meta, err := h.st.GetMeta(bucket, key)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("ETag", `"`+meta.ETag+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	// S3 semantics: DELETE is idempotent — 204 even if the key never existed.
	h.st.DeleteObject(bucket, key)
	w.WriteHeader(http.StatusNoContent)
}
