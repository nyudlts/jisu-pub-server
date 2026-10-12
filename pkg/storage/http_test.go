package storage

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// httpFixtureModTime is the Last-Modified value the mock reports for the fixture
// object so assertions stay deterministic.
var httpFixtureModTime = time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)

// TestHTTPBackendConformanceRanged drives the shared Backend conformance suite
// against HTTPBackend talking to a server that supports byte ranges (via
// http.ServeContent, which advertises Accept-Ranges and answers Range requests
// with 206 responses). This exercises the lazy, ranged Open path.
func TestHTTPBackendConformanceRanged(t *testing.T) {
	data := ConformanceFixtureBytes(t)
	srv := newHTTPFileMock(data, true)
	t.Cleanup(srv.Close)

	RunConformance(t, func() Backend {
		b, err := NewHTTPBackend(HTTPConfig{BaseURL: srv.URL})
		require.NoError(t, err)
		return b
	})
}

// TestHTTPBackendConformanceNoRanges drives the same suite against a server that
// never advertises Accept-Ranges and ignores Range headers, so Open must fall
// back to downloading the whole object to a temp file. This exercises the
// non-ranged fallback path.
func TestHTTPBackendConformanceNoRanges(t *testing.T) {
	data := ConformanceFixtureBytes(t)
	srv := newHTTPFileMock(data, false)
	t.Cleanup(srv.Close)

	RunConformance(t, func() Backend {
		b, err := NewHTTPBackend(HTTPConfig{BaseURL: srv.URL})
		require.NoError(t, err)
		return b
	})
}

// TestNewHTTPBackendValidation asserts the fail-fast config validation: a
// missing or non-http(s) base URL is reported at construction time.
func TestNewHTTPBackendValidation(t *testing.T) {
	_, err := NewHTTPBackend(HTTPConfig{})
	require.Error(t, err, "empty base URL must fail fast")

	_, err = NewHTTPBackend(HTTPConfig{BaseURL: "ftp://example.com/books"})
	require.Error(t, err, "non-http(s) scheme must fail fast")

	_, err = NewHTTPBackend(HTTPConfig{BaseURL: "/no/scheme"})
	require.Error(t, err, "a URL without a host must fail fast")
}

// TestHTTPBackendDefaultHeaderApplied verifies the pluggable-client contract:
// default headers configured on the backend (the hook reserved for future auth)
// are sent on requests.
func TestHTTPBackendDefaultHeaderApplied(t *testing.T) {
	data := ConformanceFixtureBytes(t)

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Test-Token"); v != "" {
			gotHeader = v
		}
		http.ServeContent(w, r, "epub.epub", httpFixtureModTime, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)

	b, err := NewHTTPBackend(HTTPConfig{
		BaseURL: srv.URL,
		Header:  http.Header{"X-Test-Token": {"secret-from-roundtripper"}},
	})
	require.NoError(t, err)

	_, err = b.Stat(context.Background(), ConformanceFixtureKey)
	require.NoError(t, err)
	require.Equal(t, "secret-from-roundtripper", gotHeader, "default headers must be sent")
}

// TestHTTPBackendSmoke is a manual smoke test against a real hosted EPUB. It is
// skipped unless STORAGE_HTTP_SMOKE_URL is set to the full URL of a hosted EPUB,
// so CI never depends on external network access. Set it, for example:
//
//	STORAGE_HTTP_SMOKE_URL=https://files.example.com/books/book.epub \
//	  go test ./pkg/storage/... -run TestHTTPBackendSmoke -v
func TestHTTPBackendSmoke(t *testing.T) {
	full := os.Getenv("STORAGE_HTTP_SMOKE_URL")
	if full == "" {
		t.Skip("set STORAGE_HTTP_SMOKE_URL to a hosted EPUB URL to run the smoke test")
	}
	base := full
	key := ""
	if i := strings.LastIndex(full, "/"); i >= 0 {
		base, key = full[:i], full[i+1:]
	}

	b, err := NewHTTPBackend(HTTPConfig{BaseURL: base})
	require.NoError(t, err)

	ctx := context.Background()
	info, err := b.Stat(ctx, key)
	require.NoError(t, err)
	require.Positive(t, info.Size, "hosted object must report a size")
	t.Logf("Stat %s: size=%d modtime=%s", full, info.Size, info.ModTime)

	rsc, err := b.Open(ctx, key)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rsc.Close()) })

	head := make([]byte, 4)
	_, err = rsc.Seek(0, 0)
	require.NoError(t, err)
	_, err = rsc.Read(head)
	require.NoError(t, err)
	require.Equal(t, "PK\x03\x04", string(head), "a hosted EPUB must begin with the ZIP signature")
}

// --- In-process HTTP file-server mock ----------------------------------------
//
// newHTTPFileMock returns an httptest.Server serving a single object at
// /epub.epub plus an HTML directory index at the root, mimicking a public file
// server. When ranged is true it serves via http.ServeContent so byte ranges and
// Accept-Ranges work; when false it ignores Range and never advertises
// Accept-Ranges, forcing the backend's full-download fallback. Unknown paths
// return 404 so the missing-key and empty-prefix conformance cases hold.

func newHTTPFileMock(data []byte, ranged bool) *httptest.Server {
	handler := func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")

		if p == "" {
			writeDirIndex(w, []string{"epub.epub"})
			return
		}
		if p != ConformanceFixtureKey {
			http.NotFound(w, r)
			return
		}

		if ranged {
			http.ServeContent(w, r, "epub.epub", httpFixtureModTime, bytes.NewReader(data))
			return
		}

		// Non-ranged server: advertise no range support and ignore any Range
		// header, always returning the full body with status 200.
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Last-Modified", httpFixtureModTime.UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/epub+zip")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
	return httptest.NewServer(http.HandlerFunc(handler))
}

// writeDirIndex renders a minimal HTML directory listing in the <a href="…">
// form emitted by common file servers, which parseDirLinks consumes.
func writeDirIndex(w http.ResponseWriter, entries []string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	var sb strings.Builder
	sb.WriteString("<!doctype html><html><body><pre>\n")
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("<a href=%q>%s</a>\n", e, e))
	}
	sb.WriteString("</pre></body></html>\n")
	_, _ = w.Write([]byte(sb.String()))
}
