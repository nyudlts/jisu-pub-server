// Package storage defines the backend abstraction used to read publications
// (EPUBs and exploded publication directories) from different sources — local
// disk, an HTTP file server, or an S3 bucket — behind a single interface.
//
// A Backend is selected explicitly at startup via configuration; there is no
// silent fallback between backends. One backend is chosen, validated eagerly,
// and used for the lifetime of the process.
//
// This file intentionally contains only the abstraction: the Backend interface,
// the ReadSeekCloser handle it hands out, the ObjectInfo metadata it reports,
// and the sentinel errors backends are expected to return. Concrete backends
// (local, http, s3) live in their own files and are added later.
//
// The shapes here deliberately mirror the existing read abstractions in
// pkg/fetcher (Resource.Read/Stream over explicit start/end offsets) and
// pkg/archive (Entry, and the ReaderAtCloser the zip reader consumes): callers
// need genuinely seekable access to a stored object so that an archive reader
// can be constructed over it without first copying the whole object into memory.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned (or wrapped) by a Backend when the requested key does
// not exist. Callers may test for it with errors.Is. Backends that talk to a
// remote source should wrap their transport-specific "missing object" condition
// (for example an HTTP 404 or an S3 NoSuchKey) in this sentinel so that callers
// can detect absence uniformly regardless of the underlying backend.
var ErrNotFound = errors.New("storage: object not found")

// ErrNotSeekable may be returned by a ReadSeekCloser's Seek method when the
// underlying source cannot satisfy a seek (for example an HTTP server that does
// not advertise Accept-Ranges and whose body is being streamed). Backends are
// encouraged to avoid this by materializing non-seekable sources to a temporary
// file before returning the handle (see Backend.Open), so that the ReadSeekCloser
// contract below always holds. It exists so that a backend which deliberately
// chooses not to materialize can still signal the limitation explicitly rather
// than returning a misleading result.
var ErrNotSeekable = errors.New("storage: object is not seekable")

// ReadSeekCloser is a fully seekable, closable byte stream over a single stored
// object. It is the handle returned by Backend.Open.
//
// It is exactly the composition of io.Reader, io.Seeker, and io.Closer, and the
// standard semantics of each of those interfaces apply. The seek guarantee is
// the important part of the contract and is spelled out explicitly here because
// downstream consumers (notably the zip archive reader, which needs random
// access, and ranged resource reads in pkg/fetcher) depend on it:
//
//   - Seek MUST support all three whence values: io.SeekStart (0),
//     io.SeekCurrent (1), and io.SeekEnd (2).
//   - Seeking to any offset within [0, size] is valid; in particular a backend
//     MUST be able to report the size via Seek(0, io.SeekEnd) and MUST be able
//     to rewind to the beginning via Seek(0, io.SeekStart).
//   - After a successful Seek, the next Read MUST observe bytes from the new
//     offset. Seeking and reading may be interleaved arbitrarily.
//   - Seeking to a negative resulting offset MUST return a non-nil error and
//     MUST NOT change the current offset.
//   - Seeking past the end is allowed (consistent with os.File); a subsequent
//     Read at that position returns io.EOF.
//
// A backend whose native source is not randomly seekable (for example a plain
// HTTP body) is expected to make the source seekable before returning it — for
// instance by downloading to a temporary file — so that this contract always
// holds. Close releases any such temporary resources.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// ObjectInfo describes the metadata of a single object (or pseudo-directory)
// reported by a Backend via Stat and List. It carries no payload; it is purely
// descriptive and cheap to obtain relative to opening the object.
type ObjectInfo struct {
	// Key is the backend-relative identifier of the object, using forward
	// slashes as separators regardless of the host platform. For a local
	// backend rooted at a directory this is the path relative to that root;
	// for http/s3 it is the path relative to the configured base URL or
	// bucket prefix. It is the same value accepted by Backend.Open and
	// Backend.Stat.
	Key string

	// Size is the length of the object in bytes. For a directory (IsDir true)
	// Size is not meaningful and is reported as 0.
	Size int64

	// ModTime is the last-modification time of the object when the backend can
	// determine it. Backends that cannot report a modification time (some HTTP
	// servers omit Last-Modified) leave this as the zero time.Time, which
	// callers can detect with ModTime.IsZero.
	ModTime time.Time

	// IsDir reports whether this entry is a directory/prefix rather than a
	// readable object. Backends over a flat key space (http, s3) may never set
	// this; the local backend sets it for real directories so that callers can
	// distinguish an exploded publication directory from a packaged file.
	IsDir bool
}

// Backend is an explicitly selected source of publication bytes. Exactly one
// Backend is constructed at startup from configuration and used for the whole
// process; backends are never chained or used as fallbacks for one another.
//
// All methods take a context.Context so that remote backends (http, s3) can
// honor cancellation and deadlines; local backends may ignore it beyond
// respecting cancellation. Keys are backend-relative and use forward slashes
// (see ObjectInfo.Key).
type Backend interface {
	// Open returns a seekable, closable handle to the object identified by key.
	//
	// The returned handle satisfies the full ReadSeekCloser contract: the
	// object is randomly seekable even if the backend's native source is not
	// (such a backend is responsible for materializing the object — e.g. to a
	// temporary file — before returning, and for releasing that resource on
	// Close). This is what allows an archive reader to be constructed directly
	// over the handle.
	//
	// If key does not exist, Open returns an error that wraps ErrNotFound.
	// The caller owns the returned handle and MUST Close it.
	Open(ctx context.Context, key string) (ReadSeekCloser, error)

	// Stat returns metadata for the object identified by key without opening a
	// read stream. It is the backend's cheap existence/size/type probe and is
	// used to distinguish packaged files from exploded directories and to size
	// objects ahead of reading.
	//
	// If key does not exist, Stat returns an error that wraps ErrNotFound.
	Stat(ctx context.Context, key string) (ObjectInfo, error)

	// List returns metadata for every object whose key is under the given
	// prefix, recursively. A prefix of "" (or "/") lists the entire backend.
	// The traversal is recursive — equivalent to a filesystem walk — because
	// callers need to enumerate all entries of an exploded publication, not
	// just a single directory level. Order is unspecified.
	//
	// A prefix that matches nothing yields an empty slice and a nil error; a
	// missing prefix is not itself an error.
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)

	// Close releases any resources held by the backend itself (pooled
	// connections, clients, cached handles). It does not affect handles already
	// returned by Open, which are closed independently by their owners. Close is
	// idempotent and safe to call once at shutdown.
	Close() error
}
