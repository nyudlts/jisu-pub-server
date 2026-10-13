//go:build integration

package integration

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGetAssetThroughServer(t *testing.T) {
	publicationsDir := t.TempDir()
	filename := copyEPUBFixture(t, publicationsDir)
	server := startTestServer(t, publicationsDir)

	id := base64.RawURLEncoding.EncodeToString([]byte(filename))

	manifestResp, manifestBody := getManifestResponse(t, server, id)
	if manifestResp.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d; want 200; body: %s", manifestResp.StatusCode, manifestBody)
	}

	var manifest struct {
		ReadingOrder []struct {
			Href string `json:"href"`
		} `json:"readingOrder"`
	}
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(manifest.ReadingOrder) == 0 || manifest.ReadingOrder[0].Href == "" {
		t.Fatal("manifest has no reading-order resource to request")
	}

	href := strings.TrimPrefix(manifest.ReadingOrder[0].Href, "/")
	assetURL, err := url.JoinPath(server.URL, id, href)
	if err != nil {
		t.Fatalf("build asset URL: %v", err)
	}

	resp, err := http.Get(assetURL)
	if err != nil {
		t.Fatalf("GET asset: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("asset status = %d; want 200; body: %s", resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read asset response: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("asset response body is empty")
	}
	if got := resp.Header.Get("Content-Type"); got == "" {
		t.Error("asset response has no Content-Type")
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q; want %q", got, "*")
	}

	t.Logf("requested asset %s; received %d bytes", fmt.Sprintf("/%s/%s", id, href), len(body))
}
