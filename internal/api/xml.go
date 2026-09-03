package api

import (
	"encoding/xml"
	"net/http"
)

// s3Error is the standard S3 XML error body.
type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// s3Error writes an S3 XML error. Maps known error strings to correct HTTP status.
func (h *Handler) s3Error(w http.ResponseWriter, code, msg string) {
	status := http.StatusBadRequest
	switch code {
	case "BucketAlreadyExists":
		status = http.StatusConflict
	case "NoSuchBucket", "NoSuchKey":
		status = http.StatusNotFound
	}
	e := s3Error{Code: code, Message: msg}
	b, _ := xml.Marshal(e)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	w.Write(b)
}
