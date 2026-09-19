// Package api implements the S3-compatible HTTP handler.
// Routes bucket ops, object ops, and multipart uploads to the
// store layer, behind SigV4 auth.
package api

import (
	"encoding/xml"
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
	q := r.URL.Query()
	// GET /{bucket}?uploads — list multipart uploads.
	if r.Method == http.MethodGet && q.Has("uploads") {
		h.listMultipartUploads(w, r, bucket)
		return
	}
	// GET /{bucket}?versions — list object versions.
	if r.Method == http.MethodGet && q.Has("versions") {
		h.listObjectVersions(w, r, bucket)
		return
	}
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
	q := r.URL.Query()
	switch {
	// POST /{bucket}/{key}?uploads — initiate multipart upload.
	case r.Method == http.MethodPost && q.Has("uploads"):
		h.createMultipartUpload(w, r, bucket, key)
	// PUT /{bucket}/{key}?uploadId=...&partNumber=... — upload a part.
	case r.Method == http.MethodPut && q.Has("uploadId") && q.Has("partNumber"):
		h.uploadPart(w, r, bucket, key)
	// POST /{bucket}/{key}?uploadId=... — complete multipart upload.
	case r.Method == http.MethodPost && q.Has("uploadId"):
		h.completeMultipartUpload(w, r, bucket, key)
	// GET /{bucket}/{key}?uploadId=... — list parts.
	case r.Method == http.MethodGet && q.Has("uploadId"):
		h.listParts(w, r, bucket, key)
	// DELETE /{bucket}/{key}?uploadId=... — abort multipart upload.
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		h.abortMultipartUpload(w, r, bucket, key)
	default:
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
}

func (h *Handler) putObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	etag, versionID, err := h.st.PutObject(bucket, key, r.Body)
	if err != nil {
		h.storeError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	if versionID != "" {
		w.Header().Set("X-Amz-Version-Id", versionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	rc, meta, err := h.st.GetObject(bucket, key, versionID)
	if err != nil {
		h.storeError(w, err)
		return
	}
	// If the current version is a delete marker, return 404 with header.
	if meta.IsDeleteMarker {
		rc.Close()
		w.Header().Set("X-Amz-Delete-Marker", "true")
		if meta.VersionID != "" {
			w.Header().Set("X-Amz-Version-Id", meta.VersionID)
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("ETag", `"`+meta.ETag+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	if meta.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", meta.VersionID)
	}
	io.Copy(w, rc)
}

func (h *Handler) headObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	var meta *store.ObjectMeta
	var err error
	if versionID != "" {
		meta, err = h.st.GetMetaVersion(bucket, key, versionID)
	} else {
		meta, err = h.st.GetMeta(bucket, key)
	}
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if meta.IsDeleteMarker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
		if meta.VersionID != "" {
			w.Header().Set("X-Amz-Version-Id", meta.VersionID)
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("ETag", `"`+meta.ETag+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	if meta.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", meta.VersionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")
	_, err := h.st.DeleteObject(bucket, key, versionID)
	if err != nil {
		h.storeError(w, err)
		return
	}
	// S3 semantics: DELETE is idempotent — 204 always.
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := h.st.GetBucket(bucket); err != nil {
		h.storeError(w, err)
		return
	}
	prefix := r.URL.Query().Get("prefix")
	versions, deleteMarkers := h.st.ListObjectVersions(bucket, prefix)
	res := listObjectVersionsResult{
		Xmlns:          s3xmlns,
		Name:           bucket,
		Prefix:         prefix,
		KeyMarker:      r.URL.Query().Get("key-marker"),
		VersionIdMarker: r.URL.Query().Get("version-id-marker"),
	}
	for _, v := range versions {
		res.Versions = append(res.Versions, listVersionEntry{
			Key:          v.Key,
			VersionID:    v.VersionID,
			IsLatest:     v.IsLatest,
			LastModified: v.LastModified,
			ETag:         v.ETag,
			Size:         v.Size,
			StorageClass: v.StorageClass,
		})
	}
	for _, dm := range deleteMarkers {
		res.DeleteMarkers = append(res.DeleteMarkers, listDeleteMarkerEntry{
			Key:          dm.Key,
			VersionID:    dm.VersionID,
			IsLatest:     dm.IsLatest,
			LastModified: dm.LastModified,
		})
	}
	writeXML(w, res)
}

// --- multipart ops ---

func (h *Handler) createMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID, err := h.st.NewMultipartUpload(bucket, key)
	if err != nil {
		h.storeError(w, err)
		return
	}
	writeXML(w, initiateMultipartUploadResult{
		Xmlns:    s3xmlns,
		Bucket:   bucket,
		Key:      key,
		UploadID: uploadID,
	})
}

func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	partNum, err := strconv.Atoi(q.Get("partNumber"))
	if err != nil || partNum < 1 || partNum > 10000 {
		h.s3Error(w, "InvalidArgument", "partNumber must be between 1 and 10000")
		return
	}
	etag, err := h.st.UploadPart(bucket, key, uploadID, partNum, r.Body)
	if err != nil {
		h.storeError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) completeMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	var req struct {
		Parts []store.CompletedPart `xml:"Part"`
	}
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		h.s3Error(w, "MalformedXML", "invalid CompleteMultipartUpload body")
		return
	}
	if len(req.Parts) == 0 {
		h.s3Error(w, "MalformedXML", "At least one part must be specified")
		return
	}
	if err := h.st.CompleteMultipartUpload(bucket, key, uploadID, req.Parts); err != nil {
		h.storeError(w, err)
		return
	}
	writeXML(w, completeMultipartUploadResult{
		Xmlns:   s3xmlns,
		Location: "http://" + r.Host + "/" + bucket + "/" + key,
		Bucket:  bucket,
		Key:     key,
	})
}

func (h *Handler) listParts(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	parts, err := h.st.ListParts(bucket, key, uploadID)
	if err != nil {
		h.storeError(w, err)
		return
	}
	res := listPartsResult{Xmlns: s3xmlns, Bucket: bucket, Key: key, UploadID: uploadID}
	for _, p := range parts {
		res.Parts = append(res.Parts, listPartsPartEntry{
			PartNumber:   p.PartNumber,
			LastModified: time.Now().Format(time.RFC3339),
			ETag:         `"` + p.ETag + `"`,
			Size:         p.Size,
		})
	}
	writeXML(w, res)
}

func (h *Handler) listMultipartUploads(w http.ResponseWriter, r *http.Request, bucket string) {
	uploads, err := h.st.ListMultipartUploads(bucket)
	if err != nil {
		h.storeError(w, err)
		return
	}
	res := listMultipartUploadsResult{Xmlns: s3xmlns, Bucket: bucket}
	for _, u := range uploads {
		res.Uploads = append(res.Uploads, listMultipartUploadEntry{
			Key:       u.Key,
			UploadID:  u.UploadID,
			Initiated: u.Initiated.Format(time.RFC3339),
		})
	}
	writeXML(w, res)
}

func (h *Handler) abortMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	uploadID := r.URL.Query().Get("uploadId")
	if err := h.st.AbortMultipartUpload(bucket, key, uploadID); err != nil {
		h.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
