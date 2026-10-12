package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewBackendConstructsEachMode asserts that every valid mode, given its
// required (non-secret) configuration, constructs a working backend that can
// read the shared fixture.
func TestNewBackendConstructsEachMode(t *testing.T) {
	data := ConformanceFixtureBytes(t)
	ctx := context.Background()

	t.Run("local", func(t *testing.T) {
		root := t.TempDir()
		dst := filepath.Join(root, filepath.FromSlash(ConformanceFixtureKey))
		require.NoError(t, os.WriteFile(dst, data, 0o600))

		b, err := NewBackend(ctx, Config{Mode: ModeLocal, LocalRoot: root})
		require.NoError(t, err)
		require.NotNil(t, b)
		defer b.Close()
		assert.IsType(t, (*LocalBackend)(nil), b)

		info, err := b.Stat(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		assert.Equal(t, ConformanceFixtureKey, info.Key)
	})

	t.Run("http", func(t *testing.T) {
		srv := newHTTPFileMock(data, true)
		defer srv.Close()

		b, err := NewBackend(ctx, Config{Mode: ModeHTTP, HTTP: HTTPConfig{BaseURL: srv.URL}})
		require.NoError(t, err)
		require.NotNil(t, b)
		defer b.Close()
		assert.IsType(t, (*HTTPBackend)(nil), b)

		info, err := b.Stat(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		assert.Equal(t, ConformanceFixtureKey, info.Key)
	})

	t.Run("s3", func(t *testing.T) {
		srv := newS3Mock("test-bucket", map[string][]byte{ConformanceFixtureKey: data})
		defer srv.Close()

		b, err := NewBackend(ctx, Config{Mode: ModeS3, S3: S3Config{
			Bucket:       "test-bucket",
			Region:       "us-east-1",
			Endpoint:     srv.URL,
			UsePathStyle: true,
			Anonymous:    true,
		}})
		require.NoError(t, err)
		require.NotNil(t, b)
		defer b.Close()
		assert.IsType(t, (*S3Backend)(nil), b)

		info, err := b.Stat(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		assert.Equal(t, ConformanceFixtureKey, info.Key)
	})
}

// TestNewBackendDefaultsToHTTP asserts that an empty mode is treated as the
// primary path (http) rather than erroring.
func TestNewBackendDefaultsToHTTP(t *testing.T) {
	data := ConformanceFixtureBytes(t)
	srv := newHTTPFileMock(data, true)
	defer srv.Close()

	b, err := NewBackend(context.Background(), Config{HTTP: HTTPConfig{BaseURL: srv.URL}})
	require.NoError(t, err)
	require.NotNil(t, b)
	defer b.Close()
	assert.IsType(t, (*HTTPBackend)(nil), b)
}

// TestNewBackendUnknownMode asserts an unrecognized mode is rejected (no
// fallback) and that the error names the offending value.
func TestNewBackendUnknownMode(t *testing.T) {
	_, err := NewBackend(context.Background(), Config{Mode: Mode("ftp")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
	assert.Contains(t, err.Error(), "ftp")
}

// TestNewBackendFailsFastOnMissingConfig asserts each mode validates its
// required config eagerly and names the missing variable.
func TestNewBackendFailsFastOnMissingConfig(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		cfg     Config
		wantVar string
	}{
		{"local without root", Config{Mode: ModeLocal}, "STORAGE_LOCAL_ROOT"},
		{"s3 without bucket", Config{Mode: ModeS3}, "STORAGE_S3_BUCKET"},
		{"http without base url", Config{Mode: ModeHTTP}, "STORAGE_BASE_URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := NewBackend(ctx, tc.cfg)
			require.Error(t, err)
			assert.Nil(t, b)
			assert.Contains(t, err.Error(), tc.wantVar)
		})
	}
}
