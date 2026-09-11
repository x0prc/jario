// S3 XML wire types, response encoding, and error mapping.
package api

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/c0ldheat/jario/internal/store"
)

// s3xmlns is the XML namespace on every S3 response document.
const s3xmlns = "http://s3.amazonaws.com/doc/2006-03-01/"

// writeXML encodes v as an S3 XML response with the standard header.
func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(v)
}

// --- error mapping ---

// s3ErrorBody is the standard S3 XML error document.
type s3ErrorBody struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// s3Error writes an S3 XML error with the HTTP status for code.
func (h *Handler) s3Error(w http.ResponseWriter, code, msg string) {
	status := http.StatusBadRequest
	switch code {
	case "BucketAlreadyExists":
		status = http.StatusConflict
	case "NoSuchBucket", "NoSuchKey":
		status = http.StatusNotFound
	case "AccessDenied":
		status = http.StatusForbidden
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	xml.NewEncoder(w).Encode(s3ErrorBody{Code: code, Message: msg})
}

// storeError maps store sentinel errors onto S3 error responses.
// Unknown errors become InternalError without leaking internals.
func (h *Handler) storeError(w http.ResponseWriter, err error) {
	code := "InternalError"
	switch {
	case errors.Is(err, store.ErrBucketExists):
		code = "BucketAlreadyExists"
	case errors.Is(err, store.ErrNoBucket):
		code = "NoSuchBucket"
	case errors.Is(err, store.ErrNoKey):
		code = "NoSuchKey"
	}
	h.s3Error(w, code, err.Error())
}

// --- ListBuckets (GET /) ---

type bucketEntry struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type listBucketsResult struct {
	XMLName xml.Name      `xml:"ListAllMyBucketsResult"`
	Xmlns   string        `xml:"xmlns,attr"`
	Buckets []bucketEntry `xml:"Buckets>Bucket"`
}

// --- ListObjectsV2 (GET /{bucket}?list-type=2) ---

type objectEntry struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type listBucketResult struct {
	XMLName               xml.Name      `xml:"ListBucketResult"`
	Xmlns                 string        `xml:"xmlns,attr"`
	Name                  string        `xml:"Name"`
	Prefix                string        `xml:"Prefix"`
	KeyCount              int           `xml:"KeyCount"`
	MaxKeys               int           `xml:"MaxKeys"`
	IsTruncated           bool          `xml:"IsTruncated"`
	NextContinuationToken string        `xml:"NextContinuationToken,omitempty"`
	Contents              []objectEntry `xml:"Contents"`
}
