package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LocalBackend is a Backend over a directory on the local file system. It is the
// parity baseline: the behavior every other backend is measured against. Keys
// are forward-slash, root-relative paths (see ObjectInfo.Key); they are resolved
// against the configured root and are not permitted to escape it.
type LocalBackend struct {
	root string
}

// NewLocalBackend constructs a LocalBackend rooted at dir. The root is validated
// eagerly — consistent with the project's fail-fast, no-fallback policy — so a
// misconfigured root is reported at construction time rather than on first read.
// dir must name an existing directory.
func NewLocalBackend(dir string) (*LocalBackend, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: resolving local root %q: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("storage: local root %q does not exist: %w", dir, err)
		}
		return nil, fmt.Errorf("storage: stat local root %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("storage: local root %q is not a directory", dir)
	}
	return &LocalBackend{root: abs}, nil
}

// resolve maps a backend-relative key to an absolute on-disk path, rejecting any
// key that would escape the root (for example via "..") so that a caller cannot
// read outside the configured directory.
func (b *LocalBackend) resolve(key string) (string, error) {
	clean := path.Clean("/" + strings.TrimPrefix(filepath.ToSlash(key), "/"))
	full := filepath.Join(b.root, filepath.FromSlash(clean))
	if full != b.root && !strings.HasPrefix(full, b.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: key %q escapes local root", key)
	}
	return full, nil
}

// Open implements Backend. The returned *os.File satisfies the full
// ReadSeekCloser contract natively, so no materialization is needed.
func (b *LocalBackend) Open(_ context.Context, key string) (ReadSeekCloser, error) {
	full, err := b.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("storage: open %q: %w", key, ErrNotFound)
		}
		return nil, fmt.Errorf("storage: open %q: %w", key, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("storage: stat opened %q: %w", key, err)
	}
	if info.IsDir() {
		f.Close()
		return nil, fmt.Errorf("storage: open %q: is a directory: %w", key, ErrNotFound)
	}
	return f, nil
}

// Stat implements Backend.
func (b *LocalBackend) Stat(_ context.Context, key string) (ObjectInfo, error) {
	full, err := b.resolve(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ObjectInfo{}, fmt.Errorf("storage: stat %q: %w", key, ErrNotFound)
		}
		return ObjectInfo{}, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	return b.objectInfo(full, info)
}

// List implements Backend. It walks the root recursively and returns one
// ObjectInfo per entry (files and directories) whose key is under prefix. A
// prefix that matches nothing yields an empty slice and a nil error.
func (b *LocalBackend) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	wantPrefix := strings.TrimPrefix(filepath.ToSlash(prefix), "/")

	var infos []ObjectInfo
	err := filepath.WalkDir(b.root, func(apath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if apath == b.root {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		oi, err := b.objectInfo(apath, info)
		if err != nil {
			return err
		}
		if wantPrefix != "" && !strings.HasPrefix(oi.Key, wantPrefix) {
			return nil
		}
		infos = append(infos, oi)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("storage: list %q: %w", prefix, err)
	}
	return infos, nil
}

// Close implements Backend. The local backend holds no process-wide resources,
// so Close is a no-op and is safe to call repeatedly.
func (b *LocalBackend) Close() error { return nil }

// objectInfo builds an ObjectInfo for an absolute on-disk path, deriving the
// backend-relative, forward-slash key from the configured root.
func (b *LocalBackend) objectInfo(full string, info fs.FileInfo) (ObjectInfo, error) {
	rel, err := filepath.Rel(b.root, full)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("storage: relativizing %q: %w", full, err)
	}
	key := filepath.ToSlash(rel)
	if info.IsDir() {
		return ObjectInfo{Key: key, ModTime: info.ModTime(), IsDir: true}, nil
	}
	return ObjectInfo{
		Key:     key,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}
