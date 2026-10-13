package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLocalBackendConformance drives the shared Backend conformance suite
// (RunConformance) against LocalBackend. The factory seeds a fresh temp root
// with the shared fixture EPUB at ConformanceFixtureKey on every invocation.
func TestLocalBackendConformance(t *testing.T) {
	RunConformance(t, func() Backend {
		root := t.TempDir()
		data := ConformanceFixtureBytes(t)
		dst := filepath.Join(root, filepath.FromSlash(ConformanceFixtureKey))
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
		require.NoError(t, os.WriteFile(dst, data, 0o644))

		b, err := NewLocalBackend(root)
		require.NoError(t, err)
		return b
	})
}

func TestNewLocalBackendRejectsMissingRoot(t *testing.T) {
	_, err := NewLocalBackend(filepath.Join(t.TempDir(), "nope"))
	require.Error(t, err)
}

func TestNewLocalBackendRejectsFileRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	_, err := NewLocalBackend(f)
	require.Error(t, err)
}

func TestLocalBackendRejectsEscapingKey(t *testing.T) {
	b, err := NewLocalBackend(t.TempDir())
	require.NoError(t, err)
	_, err = b.Stat(context.Background(), "../escape")
	require.Error(t, err)
}
