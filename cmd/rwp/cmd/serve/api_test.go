package serve

import (
	"encoding/base64"
	"encoding/json"

	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/readium/go-toolkit/pkg/archive"
	"github.com/readium/go-toolkit/pkg/fetcher"
	"github.com/readium/go-toolkit/pkg/manifest"
)

// tests for demoList
func TestDemoList(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"book-a.epub", "book-b.epub"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{}
	s.config.BaseDirectory = dir

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/lists", nil)
	s.demoList(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	var got []demoListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	want := []demoListItem{
		{
			Filename: "book-a.epub",
			Path:     base64.RawURLEncoding.EncodeToString([]byte("book-a.epub")),
		},
		{
			Filename: "book-b.epub",
			Path:     base64.RawURLEncoding.EncodeToString([]byte("book-b.epub")),
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("response = %#v; want %#v", got, want)
	}
}

func TestDemoListDirectoryError(t *testing.T) {
	s := &Server{}
	s.config.BaseDirectory = filepath.Join(t.TempDir(), "missing")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/lists", nil)
	s.demoList(rec, req)

	if rec.Code != 500 {
		t.Errorf("status = %d; want 500", rec.Code)
	}
}

// tests for getPublication
// the rest are integration tests happening in `integration/server_test.go`

func TestGetPublicationInvalidBase64(t *testing.T) {
	s := &Server{}

	_, err := s.getPublication("%%%")
	if err == nil {
		t.Fatal("getPublication() error = nil; want an error for invalid base64")
	}
}

// DONE: getManifest

func TestGetManifestValidPublication(t *testing.T) {
	s, id := newManifestTestServer(t, "")
	rec := requestManifest(t, s, id, "")

	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body)
	}

	var manifest map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := manifest["metadata"]; !ok {
		t.Fatal("manifest has no metadata")
	}
}

func TestGetManifestResponseHeaders(t *testing.T) {
	s, id := newManifestTestServer(t, "")
	rec := requestManifest(t, s, id, "")

	if got := rec.Header().Get("Content-Type"); !strings.HasSuffix(got, "; charset=utf-8") {
		t.Errorf("Content-Type = %q; want UTF-8 charset", got)
	}
	if got, want := rec.Header().Get("Cache-Control"), "private, must-revalidate"; got != want {
		t.Errorf("Cache-Control = %q; want %q", got, want)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q; want %q", got, "*")
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Error("ETag is empty")
	}
}

func TestGetManifestSelfLinkScheme(t *testing.T) {
	tests := []struct {
		name       string
		proto      string
		wantPrefix string
	}{
		{name: "HTTP by default", wantPrefix: "http://"},
		{name: "HTTPS forwarded by proxy", proto: "https", wantPrefix: "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, id := newManifestTestServer(t, "")
			rec := requestManifest(t, s, id, tt.proto)

			if rec.Code != 200 {
				t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body)
			}

			var manifest map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}

			href := findSelfLinkHref(manifest)
			want := tt.wantPrefix + "pub.test/" + id + "/manifest.json"
			if href != want {
				t.Errorf("self link href = %q; want %q", href, want)
			}
		})
	}
}

func TestGetManifestETag(t *testing.T) {
	s, id := newManifestTestServer(t, "")
	initial := requestManifest(t, s, id, "")
	if initial.Code != 200 {
		t.Fatalf("initial status = %d; want 200", initial.Code)
	}
	etag := initial.Header().Get("ETag")
	if etag == "" {
		t.Fatal("initial response has no ETag")
	}

	t.Run("matching ETag returns not modified", func(t *testing.T) {
		rec := requestManifestWithETag(t, s, id, etag)
		if rec.Code != 304 {
			t.Errorf("status = %d; want 304", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("304 response body is not empty: %q", rec.Body.String())
		}
	})

	t.Run("non-matching ETag returns manifest", func(t *testing.T) {
		rec := requestManifestWithETag(t, s, id, `"different"`)
		if rec.Code != 200 {
			t.Fatalf("status = %d; want 200", rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Error("response body is empty")
		}
	})
}

func TestGetManifestMissingPublication(t *testing.T) {
	s := NewServer(ServerConfig{BaseDirectory: t.TempDir()})
	s.router = s.Routes()

	id := base64.RawURLEncoding.EncodeToString([]byte("missing.epub"))
	rec := requestManifest(t, s, id, "")

	if rec.Code != 500 {
		t.Errorf("status = %d; want 500", rec.Code)
	}
}

func TestGetManifestIndentedJSON(t *testing.T) {
	s, id := newManifestTestServer(t, "  ")
	rec := requestManifest(t, s, id, "")

	if rec.Code != 200 {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body)
	}

	var manifest map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "\n  ") {
		t.Error("response JSON does not appear to be indented")
	}
}

func newManifestTestServer(t *testing.T, indent string) (*Server, string) {
	t.Helper()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test file")
	}
	fixture := filepath.Join(filepath.Dir(testFile), "..", "..", "..", "..", "test", "9781479819454.epub")

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read EPUB fixture %q: %v", fixture, err)
	}

	publicationsDir := t.TempDir()
	const filename = "9781479819454.epub"
	if err := os.WriteFile(filepath.Join(publicationsDir, filename), data, 0600); err != nil {
		t.Fatalf("copy EPUB fixture: %v", err)
	}

	s := NewServer(ServerConfig{
		BaseDirectory: publicationsDir,
		JSONIndent:    indent,
	})
	s.router = s.Routes()

	id := base64.RawURLEncoding.EncodeToString([]byte(filename))
	return s, id
}

func requestManifest(t *testing.T, s *Server, id, forwardedProto string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("GET", "/"+id+"/manifest.json", nil)
	req.Host = "pub.test"
	if forwardedProto != "" {
		req.Header.Set("X-Forwarded-Proto", forwardedProto)
	}

	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func requestManifestWithETag(t *testing.T, s *Server, id, etag string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("GET", "/"+id+"/manifest.json", nil)
	req.Host = "pub.test"
	req.Header.Set("If-None-Match", etag)

	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func findSelfLinkHref(value map[string]any) string {
	links, ok := value["links"].([]any)
	if !ok {
		return ""
	}

	for _, item := range links {
		link, ok := item.(map[string]any)
		if !ok || !hasSelfRel(link["rel"]) {
			continue
		}
		if href, ok := link["href"].(string); ok {
			return href
		}
	}
	return ""
}

func hasSelfRel(value any) bool {
	switch rel := value.(type) {
	case string:
		return rel == "self"
	case []any:
		for _, item := range rel {
			if item == "self" {
				return true
			}
		}
	}
	return false
}

// TODO: getAsset

func TestGetAssetExistingResource(t *testing.T) {
	s, id, href := assetFixture(t, func(_ *manifest.Link) bool { return true })
	rec := requestAsset(s, id, href, "", "", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("asset response body is empty")
	}
	for header, want := range map[string]string{
		"Cache-Control":               "private, max-age=86400, immutable",
		"Access-Control-Allow-Origin": "*",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q; want %q", header, got, want)
		}
	}
	if got := rec.Header().Get("Content-Type"); got == "" {
		t.Error("Content-Type is empty")
	}
}

func TestGetAssetMissingResource(t *testing.T) {
	s, id, _ := assetFixture(t, func(_ *manifest.Link) bool { return true })
	rec := requestAsset(s, id, "not-in-the-publication", "", "", nil)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

func TestGetAssetInvalidPublicationID(t *testing.T) {
	s, _, href := assetFixture(t, func(_ *manifest.Link) bool { return true })
	rec := requestAsset(s, "a", href, "", "", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", rec.Code)
	}
}

func TestGetAssetSingleRange(t *testing.T) {
	s, id, href := assetFixture(t, func(_ *manifest.Link) bool { return true })

	full := requestAsset(s, id, href, "", "", nil)
	if full.Code != http.StatusOK {
		t.Fatalf("full response status = %d; want 200", full.Code)
	}
	if full.Body.Len() < 4 {
		t.Skip("fixture asset is too small to test a byte range")
	}

	rec := requestAsset(s, id, href, "bytes=0-2", "", nil)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d; want 206", rec.Code)
	}
	if got, want := rec.Body.String(), full.Body.String()[:3]; got != want {
		t.Errorf("range body = %q; want %q", got, want)
	}
	if got := rec.Header().Get("Content-Range"); got == "" {
		t.Error("Content-Range header is empty")
	}
}

func TestGetAssetInvalidRange(t *testing.T) {
	s, id, href := assetFixture(t, func(_ *manifest.Link) bool { return true })
	rec := requestAsset(s, id, href, "not-a-range", "", nil)

	if rec.Code != http.StatusLengthRequired {
		t.Errorf("status = %d; want 411", rec.Code)
	}
}

func TestGetAssetMultipleRanges(t *testing.T) {
	s, id, href := assetFixture(t, func(_ *manifest.Link) bool { return true })
	rec := requestAsset(s, id, href, "bytes=0-1,3-4", "", nil)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d; want 501", rec.Code)
	}
}

func TestGetAssetTextCharset(t *testing.T) {
	s, id, href := assetFixture(t, func(link *manifest.Link) bool {
		return slices.Contains(utfCharsetNeeded, link.MediaType().String())
	})
	rec := requestAsset(s, id, href, "", "", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "charset=utf-8") {
		t.Errorf("Content-Type = %q; want UTF-8 charset", got)
	}
}

func TestGetAssetTemplatedLink(t *testing.T) {
	s, id, href := assetFixture(t, func(link *manifest.Link) bool {
		return link.Templated
	})

	query := url.Values{"width": {"100"}, "height": {"100"}}
	rec := requestAsset(s, id, href, "", "", query)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body)
	}
}

func TestGetAssetCompressedResource(t *testing.T) {
	s, id, _ := assetFixture(t, func(_ *manifest.Link) bool { return true })

	publication, err := s.getPublication(id)
	if err != nil {
		t.Fatalf("load fixture publication: %v", err)
	}

	var href string
	links := append(publication.Manifest.ReadingOrder, publication.Manifest.Resources...)
	for _, link := range links {
		res := publication.Get(link)
		compressed, ok := res.(fetcher.CompressedResource)
		isDeflate := ok && compressed.CompressedAs(archive.CompressionMethodDeflate)
		res.Close()

		if isDeflate {
			href = link.Href
			break
		}
	}
	if href == "" {
		t.Skip("fixture has no deflate-compressed asset")
	}

	rec := requestAsset(s, id, href, "", "deflate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "deflate" {
		t.Errorf("Content-Encoding = %q; want deflate", got)
	}
}

func assetFixture(t *testing.T, match func(*manifest.Link) bool) (*Server, string, string) {
	t.Helper()

	s, id := newManifestTestServer(t, "")
	publication, err := s.getPublication(id)
	if err != nil {
		t.Fatalf("load fixture publication: %v", err)
	}

	links := append(publication.Manifest.ReadingOrder, publication.Manifest.Resources...)
	for _, link := range links {
		if match(&link) {
			return s, id, link.Href
		}
	}
	t.Skip("fixture has no asset matching this test")
	return nil, "", ""
}

func requestAsset(s *Server, id, href, rangeHeader, acceptEncoding string, query url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/"+id+"/"+href, nil)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	req = mux.SetURLVars(req, map[string]string{
		"path":  id,
		"asset": href,
	})

	rec := httptest.NewRecorder()
	s.getAsset(rec, req)
	return rec
}
