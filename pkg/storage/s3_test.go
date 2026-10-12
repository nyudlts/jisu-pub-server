package storage

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fixedModTime is the Last-Modified value the mock reports for every object, so
// assertions remain deterministic.
var fixedModTime = time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)

// TestS3BackendConformance drives the shared Backend conformance suite against
// S3Backend. It stands up an in-process mock of the minimal S3 REST surface the
// backend uses (GetObject, HeadObject, ListObjectsV2) so the suite runs with no
// real AWS account and no credentials: the backend is configured Anonymous with
// path-style addressing against the mock's endpoint.
func TestS3BackendConformance(t *testing.T) {
	data := ConformanceFixtureBytes(t)
	srv := newS3Mock("test-bucket", map[string][]byte{ConformanceFixtureKey: data})
	t.Cleanup(srv.Close)

	RunConformance(t, func() Backend {
		b, err := NewS3Backend(context.Background(), S3Config{
			Bucket:       "test-bucket",
			Region:       "us-east-1",
			Endpoint:     srv.URL,
			UsePathStyle: true,
			Anonymous:    true,
		})
		require.NoError(t, err)
		return b
	})
}

// TestNewS3BackendRequiresBucket asserts the fail-fast config validation: a
// missing bucket is reported at construction time, not on first read.
func TestNewS3BackendRequiresBucket(t *testing.T) {
	_, err := NewS3Backend(context.Background(), S3Config{Anonymous: true})
	require.Error(t, err)
}

// --- In-process S3 mock ------------------------------------------------------
//
// newS3Mock returns an httptest.Server implementing just enough of the path-style
// S3 REST API for S3Backend: HeadObject (HEAD /bucket/key), GetObject
// (GET /bucket/key) and ListObjectsV2 (GET /bucket?list-type=2&prefix=...). It is
// test-only and holds its objects in memory keyed by full S3 key.

func newS3Mock(bucket string, objects map[string][]byte) *httptest.Server {
	handler := func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/")
		_, key, _ := strings.Cut(trimmed, "/")

		if r.URL.Query().Get("list-type") == "2" {
			writeS3List(w, bucket, r.URL.Query().Get("prefix"), objects)
			return
		}

		data, ok := objects[key]
		switch r.Method {
		case http.MethodHead:
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			setObjectHeaders(w, len(data))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if !ok {
				writeS3Error(w, http.StatusNotFound, "NoSuchKey", key)
				return
			}
			setObjectHeaders(w, len(data))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
	return httptest.NewServer(http.HandlerFunc(handler))
}

func setObjectHeaders(w http.ResponseWriter, size int) {
	w.Header().Set("Content-Length", strconv.Itoa(size))
	w.Header().Set("Last-Modified", fixedModTime.UTC().Format(http.TimeFormat))
	w.Header().Set("Accept-Ranges", "bytes")
}

func writeS3Error(w http.ResponseWriter, status int, code, key string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header +
		"<Error><Code>" + code + "</Code><Message>" + code +
		"</Message><Key>" + key + "</Key></Error>"))
}

type s3ListResult struct {
	XMLName     xml.Name        `xml:"ListBucketResult"`
	Xmlns       string          `xml:"xmlns,attr"`
	Name        string          `xml:"Name"`
	Prefix      string          `xml:"Prefix"`
	KeyCount    int             `xml:"KeyCount"`
	MaxKeys     int             `xml:"MaxKeys"`
	IsTruncated bool            `xml:"IsTruncated"`
	Contents    []s3ListContent `xml:"Contents"`
}

type s3ListContent struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

func writeS3List(w http.ResponseWriter, bucket, prefix string, objects map[string][]byte) {
	result := s3ListResult{
		Xmlns:   "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:    bucket,
		Prefix:  prefix,
		MaxKeys: 1000,
	}
	for key, data := range objects {
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		result.Contents = append(result.Contents, s3ListContent{
			Key:          key,
			LastModified: fixedModTime.UTC().Format("2006-01-02T15:04:05.000Z"),
			Size:         int64(len(data)),
			StorageClass: "STANDARD",
		})
	}
	result.KeyCount = len(result.Contents)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(result)
}
