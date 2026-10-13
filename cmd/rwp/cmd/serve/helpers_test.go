package serve

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/readium/go-toolkit/pkg/manifest"
	"github.com/readium/go-toolkit/pkg/mediatype"
)

// parseCoding: plain values, surrounding spaces, mixed case, and parameters such as gzip; q=0.5.

func TestParseCoding(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain value", input: "gzip", want: "gzip"},
		{name: "surrounding spaces", input: "  gzip  ", want: "gzip"},
		{name: "mixed case", input: "GzIp", want: "gzip"},
		{name: "parameter", input: "gzip; q=0.5", want: "gzip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCoding(tt.input); got != tt.want {
				t.Errorf("parseCoding(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

// supportsEncoding: encoding present or absent, multiple header values, comma-separated values, mixed case, and parameters.

func TestSupportsEncoding(t *testing.T) {
	tests := []struct {
		name     string
		values   []string
		encoding string
		want     bool
	}{
		{name: "present", values: []string{"gzip"}, encoding: "gzip", want: true},
		{name: "absent", values: []string{"br"}, encoding: "gzip", want: false},
		{name: "multiple header values", values: []string{"br", "gzip"}, encoding: "gzip", want: true},
		{name: "comma-separated values", values: []string{"br, gzip"}, encoding: "gzip", want: true},
		{name: "mixed case", values: []string{"GzIp"}, encoding: "gzip", want: true},
		{name: "with parameter", values: []string{"gzip; q=0.5"}, encoding: "gzip", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			for _, value := range tt.values {
				req.Header.Add("Accept-Encoding", value)
			}

			if got := supportsEncoding(req, tt.encoding); got != tt.want {
				t.Errorf("supportsEncoding() = %t; want %t", got, tt.want)
			}
		})
	}
}

// convertURLValuesToMap: empty input, keys with no values, and repeated values (checks that the first value is used).

func TestConvertURLValuesToMap(t *testing.T) {
	tests := []struct {
		name  string
		input url.Values
		want  map[string]string
	}{
		{
			name:  "empty input",
			input: url.Values{},
			want:  map[string]string{},
		},
		{
			name:  "keys with no values are omitted",
			input: url.Values{"empty": nil},
			want:  map[string]string{},
		},
		{
			name:  "uses first repeated value",
			input: url.Values{"key": {"first", "second"}},
			want:  map[string]string{"key": "first"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertURLValuesToMap(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("convertURLValuesToMap(%v) = %v; want %v", tt.input, got, tt.want)
			}
		})
	}
}

// makeRelative: removes a leading / from the main link and alternate links; leaves relative links unchanged.

func TestMakeRelative(t *testing.T) {
	tests := []struct {
		name              string
		link              manifest.Link
		wantHref          string
		wantAlternateHref string
	}{
		{
			name: "removes leading slash from links",
			link: manifest.Link{
				Href: "/chapter.xhtml",
				Alternates: []manifest.Link{
					{Href: "/chapter.html"},
				},
			},
			wantHref:          "/chapter.xhtml",
			wantAlternateHref: "/chapter.html",
		},
		{
			name: "keeps relative links unchanged",
			link: manifest.Link{
				Href: "chapter.xhtml",
				Alternates: []manifest.Link{
					{Href: "chapter.html"},
				},
			},
			wantHref:          "chapter.xhtml",
			wantAlternateHref: "chapter.html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRelative(tt.link)

			wantHref := strings.TrimPrefix(tt.wantHref, "/")
			if got.Href != wantHref {
				t.Errorf("Href = %q; want %q", got.Href, wantHref)
			}
			if len(got.Alternates) != 1 {
				t.Fatalf("got %d alternates; want 1", len(got.Alternates))
			}
			wantAltHref := strings.TrimPrefix(tt.wantAlternateHref, "/")
			if got.Alternates[0].Href != wantAltHref {
				t.Errorf("alternate Href = %q; want %q", got.Alternates[0].Href, wantAltHref)
			}
		})
	}
}

// conformsToAsMimetype: empty profiles use the default MIME type; Divina and Audiobook profiles select their MIME types; test which profile wins when both are present.

func TestConformsToAsMimetype(t *testing.T) {
	tests := []struct {
		name     string
		profiles manifest.Profiles
		wantMIME string
	}{
		{
			name:     "empty profiles use default",
			profiles: manifest.Profiles{},
			wantMIME: mediatype.ReadiumWebpubManifest.String(),
		},
		{
			name:     "Divina profile",
			profiles: manifest.Profiles{manifest.ProfileDivina},
			wantMIME: mediatype.ReadiumDivinaManifest.String(),
		},
		{
			name:     "Audiobook profile",
			profiles: manifest.Profiles{manifest.ProfileAudiobook},
			wantMIME: mediatype.ReadiumAudiobookManifest.String(),
		},
		{
			name:     "first profile wins",
			profiles: manifest.Profiles{manifest.ProfileDivina, manifest.ProfileAudiobook},
			wantMIME: mediatype.ReadiumDivinaManifest.String(),
		},
		{
			name:     "first profile wins in reverse order",
			profiles: manifest.Profiles{manifest.ProfileAudiobook, manifest.ProfileDivina},
			wantMIME: mediatype.ReadiumAudiobookManifest.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conformsToAsMimetype(tt.profiles); got != tt.wantMIME {
				t.Errorf("conformsToAsMimetype(%v) = %q; want %q", tt.profiles, got, tt.wantMIME)
			}
		})
	}
}
