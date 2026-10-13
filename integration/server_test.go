//go:build integration

package integration

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/readium/go-toolkit/cmd/rwp/cmd/serve"
)

func TestMissingEPUB(t *testing.T) {
	publicationsDir := t.TempDir()
	server := startTestServer(t, publicationsDir)

	id := base64.RawURLEncoding.EncodeToString([]byte("missing.epub"))
	resp, _ := getManifestResponse(t, server, id)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d; want %d", resp.StatusCode, http.StatusInternalServerError)
	}
}

func TestValidEPUBReturnsManifest(t *testing.T) {
	publicationsDir := t.TempDir()
	filename := copyEPUBFixture(t, publicationsDir)
	server := startTestServer(t, publicationsDir)

	id := base64.RawURLEncoding.EncodeToString([]byte(filename))
	resp, body := getManifestResponse(t, server, id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want %d; body: %s", resp.StatusCode, http.StatusOK, body)
	}

	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if _, ok := manifest["metadata"]; !ok {
		t.Fatal("manifest has no metadata")
	}
}

func TestGetPublicationUsesCache(t *testing.T) {
	publicationsDir := t.TempDir()
	filename := copyEPUBFixture(t, publicationsDir)
	server := startTestServer(t, publicationsDir)

	id := base64.RawURLEncoding.EncodeToString([]byte(filename))
	firstResp, _ := getManifestResponse(t, server, id)
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d; want %d", firstResp.StatusCode, http.StatusOK)
	}

	// The second request can succeed only if the publication was cached.
	if err := os.Remove(filepath.Join(publicationsDir, filename)); err != nil {
		t.Fatal(err)
	}

	secondResp, body := getManifestResponse(t, server, id)
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("second request status = %d; want %d; body: %s", secondResp.StatusCode, http.StatusOK, body)
	}
}

func TestManifestLinksAreRelative(t *testing.T) {
	publicationsDir := t.TempDir()
	filename := copyEPUBFixture(t, publicationsDir)
	server := startTestServer(t, publicationsDir)

	id := base64.RawURLEncoding.EncodeToString([]byte(filename))
	resp, body := getManifestResponse(t, server, id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want %d; body: %s", resp.StatusCode, http.StatusOK, body)
	}

	var manifest any
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	checkRelativeHrefs(t, manifest, "$")
}

func startTestServer(t *testing.T, publicationsDir string) *httptest.Server {
	t.Helper()

	s := serve.NewServer(serve.ServerConfig{BaseDirectory: publicationsDir})
	server := httptest.NewServer(s.Routes())
	t.Cleanup(server.Close)
	return server
}

func copyEPUBFixture(t *testing.T, destination string) string {
	t.Helper()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test file")
	}
	fixture := filepath.Join(filepath.Dir(testFile), "..", "test", "9781479819454.epub")

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read EPUB fixture: %v", err)
	}

	const filename = "fixture.epub"
	if err := os.WriteFile(filepath.Join(destination, filename), data, 0600); err != nil {
		t.Fatalf("write EPUB fixture: %v", err)
	}
	return filename
}

func getManifestResponse(t *testing.T, server *httptest.Server, id string) (*http.Response, []byte) {
	t.Helper()

	resp, err := http.Get(fmt.Sprintf("%s/%s/manifest.json", server.URL, id))
	if err != nil {
		t.Fatalf("GET manifest: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read manifest response: %v", err)
	}
	return resp, body
}

func checkRelativeHrefs(t *testing.T, value any, location string) {
	t.Helper()

	switch node := value.(type) {
	case map[string]any:
		if href, ok := node["href"].(string); ok && strings.HasPrefix(href, "/") {
			t.Errorf("%s href = %q; want a relative path", location, href)
		}
		for key, child := range node {
			checkRelativeHrefs(t, child, location+"."+key)
		}
	case []any:
		for i, child := range node {
			checkRelativeHrefs(t, child, fmt.Sprintf("%s[%d]", location, i))
		}
	}
}
