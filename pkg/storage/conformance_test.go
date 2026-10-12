package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is the Backend conformance suite (Task A3). It intentionally
// contains no concrete production backend: RunConformance is a reusable harness
// that the local (B1), http (C2), and s3 (C1) backends drive from their own
// test files in this package. Because those backend tests live in package
// storage, they can call the exported RunConformance helper from this test
// binary directly.
//
// Until a real backend exists, the suite is exercised here against a small
// test-only in-memory double (memBackend) so that `go test ./pkg/storage/...`
// is green and the harness itself is trusted before any backend reuses it.
// RunConformance also skips cleanly when handed a nil factory, so a backend
// test whose prerequisites are unavailable (for example missing mock
// infrastructure) can opt out without failing.

// ConformanceFixtureKey is the key under which every backend factory passed to
// RunConformance must expose the test EPUB. It is a packaged (zipped) EPUB file,
// not an exploded directory, so the single key is satisfiable by flat-key
// backends (http, s3) as well as the local backend.
const ConformanceFixtureKey = "epub.epub"

// conformanceFixturePath is the on-disk source of the fixture bytes, relative to
// this package's directory. It reuses the shared archive testdata so there is a
// single source of truth for the fixture across packages.
const conformanceFixturePath = "../archive/testdata/epub.epub"

// ConformanceFixtureBytes returns the raw bytes of the shared test EPUB. Backend
// test files use it to seed their backend (write the object to a temp dir, serve
// it from an httptest.Server, put it in a mock bucket, …) so that the seeded
// object matches exactly what RunConformance asserts against.
func ConformanceFixtureBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(conformanceFixturePath))
	require.NoError(t, err, "reading conformance fixture %s", conformanceFixturePath)
	require.NotEmpty(t, data, "conformance fixture is empty")
	return data
}

// RunConformance verifies that a Backend honors the storage.Backend and
// ReadSeekCloser contracts documented in storage.go. It is the shared acceptance
// suite reused by every backend.
//
// newBackend must return a freshly constructed Backend that exposes the shared
// test EPUB (ConformanceFixtureBytes) at ConformanceFixtureKey. It is called
// once per invocation; the returned backend is Closed by the suite at the end.
//
// If newBackend is nil the suite is skipped — this lets a backend test bail out
// gracefully when its prerequisites (a mock server, credentials, …) are not
// available rather than reporting a failure.
func RunConformance(t *testing.T, newBackend func() Backend) {
	t.Helper()

	if newBackend == nil {
		t.Skip("storage conformance: no backend factory provided; skipping")
		return
	}

	backend := newBackend()
	if backend == nil {
		t.Skip("storage conformance: backend factory returned nil; skipping")
		return
	}
	t.Cleanup(func() {
		assert.NoError(t, backend.Close(), "Backend.Close must return nil")
	})

	want := ConformanceFixtureBytes(t)
	ctx := context.Background()

	t.Run("Open reads the full object", func(t *testing.T) {
		rsc, err := backend.Open(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		require.NotNil(t, rsc)
		defer rsc.Close()

		got, err := io.ReadAll(rsc)
		require.NoError(t, err)
		assert.Equal(t, want, got, "Open must yield the exact object bytes")
	})

	t.Run("Stat reports size and key", func(t *testing.T) {
		info, err := backend.Stat(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		assert.Equal(t, ConformanceFixtureKey, info.Key, "Stat must echo the requested key")
		assert.Equal(t, int64(len(want)), info.Size, "Stat must report the object size")
		assert.False(t, info.IsDir, "a packaged EPUB object must not be reported as a directory")
	})

	t.Run("Seek honors all whence values", func(t *testing.T) {
		rsc, err := backend.Open(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		defer rsc.Close()

		size := int64(len(want))

		// Seek(0, SeekEnd) reports the size.
		end, err := rsc.Seek(0, io.SeekEnd)
		require.NoError(t, err)
		assert.Equal(t, size, end, "Seek(0, SeekEnd) must return the object size")

		// Rewind to the beginning and read a prefix.
		pos, err := rsc.Seek(0, io.SeekStart)
		require.NoError(t, err)
		assert.Equal(t, int64(0), pos)

		const prefix = 16
		head := make([]byte, prefix)
		_, err = io.ReadFull(rsc, head)
		require.NoError(t, err)
		assert.Equal(t, want[:prefix], head, "read after rewind must observe the start of the object")

		// SeekStart to an interior offset.
		const mid = 1024
		pos, err = rsc.Seek(mid, io.SeekStart)
		require.NoError(t, err)
		assert.Equal(t, int64(mid), pos)
		midByte := make([]byte, 1)
		_, err = io.ReadFull(rsc, midByte)
		require.NoError(t, err)
		assert.Equal(t, want[mid], midByte[0], "read after SeekStart must observe bytes at the new offset")

		// SeekCurrent moves relative to the position left by the previous read.
		pos, err = rsc.Seek(0, io.SeekCurrent)
		require.NoError(t, err)
		assert.Equal(t, int64(mid+1), pos, "SeekCurrent(0) must report the current offset")

		const skip = 32
		pos, err = rsc.Seek(skip, io.SeekCurrent)
		require.NoError(t, err)
		assert.Equal(t, int64(mid+1+skip), pos)
		relByte := make([]byte, 1)
		_, err = io.ReadFull(rsc, relByte)
		require.NoError(t, err)
		assert.Equal(t, want[mid+1+skip], relByte[0], "read after SeekCurrent must observe bytes at the new offset")
	})

	t.Run("Seek to end then read returns EOF", func(t *testing.T) {
		rsc, err := backend.Open(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		defer rsc.Close()

		_, err = rsc.Seek(0, io.SeekEnd)
		require.NoError(t, err)
		n, err := rsc.Read(make([]byte, 8))
		assert.Equal(t, 0, n)
		assert.ErrorIs(t, err, io.EOF, "reading at end-of-object must return io.EOF")
	})

	t.Run("Seek to negative offset errors without moving", func(t *testing.T) {
		rsc, err := backend.Open(ctx, ConformanceFixtureKey)
		require.NoError(t, err)
		defer rsc.Close()

		start, err := rsc.Seek(8, io.SeekStart)
		require.NoError(t, err)
		require.Equal(t, int64(8), start)

		_, err = rsc.Seek(-1, io.SeekStart)
		assert.Error(t, err, "seeking to a negative offset must return an error")

		pos, err := rsc.Seek(0, io.SeekCurrent)
		require.NoError(t, err)
		assert.Equal(t, int64(8), pos, "a failed Seek must not change the current offset")
	})

	t.Run("List includes the fixture object", func(t *testing.T) {
		infos, err := backend.List(ctx, "")
		require.NoError(t, err)

		var found *ObjectInfo
		for i := range infos {
			if infos[i].Key == ConformanceFixtureKey {
				found = &infos[i]
				break
			}
		}
		require.NotNil(t, found, "List(\"\") must enumerate %q", ConformanceFixtureKey)
		assert.Equal(t, int64(len(want)), found.Size, "listed object size must match the fixture")
		assert.False(t, found.IsDir, "the packaged EPUB object must not be listed as a directory")
	})

	t.Run("List of a non-matching prefix is empty, not an error", func(t *testing.T) {
		infos, err := backend.List(ctx, "this/prefix/matches/nothing/")
		assert.NoError(t, err, "a prefix that matches nothing is not an error")
		assert.Empty(t, infos, "a non-matching prefix must yield no entries")
	})

	t.Run("Open of a missing key wraps ErrNotFound", func(t *testing.T) {
		_, err := backend.Open(ctx, "does-not-exist.epub")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound, "Open of a missing key must wrap ErrNotFound")
	})

	t.Run("Stat of a missing key wraps ErrNotFound", func(t *testing.T) {
		_, err := backend.Stat(ctx, "does-not-exist.epub")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound, "Stat of a missing key must wrap ErrNotFound")
	})
}

// --- Test-only in-memory backend ---------------------------------------------
//
// memBackend is a minimal, flat-key-space Backend used solely to self-verify the
// conformance harness above. It is not a production backend and is never
// exported; the real local/http/s3 backends arrive in later tasks.

type memBackend struct {
	objects map[string][]byte
}

func newMemBackend(objects map[string][]byte) *memBackend {
	return &memBackend{objects: objects}
}

func (b *memBackend) Open(_ context.Context, key string) (ReadSeekCloser, error) {
	data, ok := b.objects[key]
	if !ok {
		return nil, fmt.Errorf("mem: open %q: %w", key, ErrNotFound)
	}
	return nopCloser{bytes.NewReader(data)}, nil
}

func (b *memBackend) Stat(_ context.Context, key string) (ObjectInfo, error) {
	data, ok := b.objects[key]
	if !ok {
		return ObjectInfo{}, fmt.Errorf("mem: stat %q: %w", key, ErrNotFound)
	}
	return ObjectInfo{Key: key, Size: int64(len(data))}, nil
}

func (b *memBackend) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	for key, data := range b.objects {
		if prefix == "" || prefix == "/" || strings.HasPrefix(key, prefix) {
			out = append(out, ObjectInfo{Key: key, Size: int64(len(data))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (b *memBackend) Close() error { return nil }

// nopCloser adds a no-op Close to a *bytes.Reader so it satisfies ReadSeekCloser.
type nopCloser struct {
	*bytes.Reader
}

func (nopCloser) Close() error { return nil }

// TestRunConformanceWithMemBackend self-verifies the harness: a correct backend
// must pass the full suite. This gives a green signal now, before any production
// backend reuses RunConformance.
func TestRunConformanceWithMemBackend(t *testing.T) {
	RunConformance(t, func() Backend {
		return newMemBackend(map[string][]byte{
			ConformanceFixtureKey: ConformanceFixtureBytes(t),
		})
	})
}

// TestRunConformanceSkipsWithoutBackend documents the skippable-with-no-backend
// behavior required by the task: a nil factory skips rather than fails.
func TestRunConformanceSkipsWithoutBackend(t *testing.T) {
	var skipped bool
	t.Run("nil-factory", func(st *testing.T) {
		// t.Skip calls runtime.Goexit; deferred funcs still run, so we can
		// observe the skip state after RunConformance returns via Goexit.
		defer func() { skipped = st.Skipped() }()
		RunConformance(st, nil)
	})
	if !skipped {
		t.Fatal("RunConformance(nil) must skip, not fail")
	}
}
