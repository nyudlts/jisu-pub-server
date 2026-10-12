package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// HTTPBackend is a Backend over a public HTTP(S) file server rooted at a base
// URL. It is the project's PRIMARY source: books are already hosted and served
// publicly as EPUBs, so this is the main runtime path.
//
// Keys are forward-slash, base-relative paths (see ObjectInfo.Key); they are
// resolved against the configured base URL and are cleaned so a key cannot
// escape that base (for example via ".."). The backend reads objects over plain
// GET/HEAD requests:
//
//   - When the server advertises Accept-Ranges: bytes, Open returns a handle
//     that satisfies seeks with ranged GET requests, so an archive reader can be
//     constructed over a hosted EPUB without downloading it in full.
//   - When ranges are unsupported, Open falls back to downloading the whole
//     object to a temporary file and returns a handle over that file, which is
//     natively seekable. The temporary file is removed when the handle is closed.
//
// No authentication is required for a public file server. The HTTP client and a
// set of default request headers are nonetheless injectable (HTTPConfig.Client,
// HTTPConfig.Header) so that auth headers could be added later without a
// redesign — note these are programmatic hooks, not CLI flags: per project
// policy no credential-bearing values are accepted as configuration.
type HTTPBackend struct {
	base   *url.URL
	client *http.Client
	header http.Header
}

// HTTPConfig carries the non-secret configuration needed to construct an
// HTTPBackend.
type HTTPConfig struct {
	// BaseURL is the root URL the file server exposes the publications under,
	// for example "https://files.example.com/books". Required; NewHTTPBackend
	// fails fast if it is empty or not an absolute http/https URL.
	BaseURL string

	// Client optionally overrides the HTTP client used for every request. When
	// nil a default client is used. It is exposed so callers can supply a client
	// with custom transport, timeouts, or (later) authenticating round-trippers
	// without this backend being redesigned.
	Client *http.Client

	// Header optionally supplies default headers added to every request (for
	// example a future Authorization header). It is a programmatic hook only;
	// no credential-bearing values are ever accepted through CLI flags or env
	// config per project policy.
	Header http.Header
}

// NewHTTPBackend constructs an HTTPBackend from non-secret configuration,
// validating the base URL eagerly — consistent with the project's fail-fast,
// no-fallback policy — so a misconfigured URL is reported at construction time
// rather than on first read.
func NewHTTPBackend(cfg HTTPConfig) (*HTTPBackend, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("storage: http backend requires a base URL (STORAGE_BASE_URL)")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("storage: parsing base URL %q: %w", cfg.BaseURL, err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("storage: http backend base URL %q must be http or https", cfg.BaseURL)
	}
	if base.Host == "" {
		return nil, fmt.Errorf("storage: http backend base URL %q has no host", cfg.BaseURL)
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPBackend{
		base:   base,
		client: client,
		header: cfg.Header.Clone(),
	}, nil
}

// objectURL maps a backend-relative key to the absolute object URL.
func (b *HTTPBackend) objectURL(key string) string { return b.resolve(key, false) }

// dirURL maps a backend-relative directory key to the absolute URL of its
// listing, with a trailing slash so a file server returns the directory index
// rather than redirecting.
func (b *HTTPBackend) dirURL(dir string) string { return b.resolve(dir, true) }

// resolve joins a cleaned, base-relative path onto the base URL. The key is
// cleaned against "/" so that ".." segments cannot escape the configured base
// (a path-traversal / SSRF guard), and each remaining segment is URL-escaped by
// url.URL.JoinPath.
func (b *HTTPBackend) resolve(key string, dir bool) string {
	clean := strings.Trim(path.Clean("/"+strings.TrimPrefix(key, "/")), "/")
	var segs []string
	if clean != "" && clean != "." {
		segs = strings.Split(clean, "/")
	}
	u := b.base.JoinPath(segs...)
	s := u.String()
	if dir && !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}

// newRequest builds a request for the given method and URL, applying the
// backend's default headers.
func (b *HTTPBackend) newRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for name, values := range b.header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	return req, nil
}

// head probes an object with a HEAD request, returning its metadata and whether
// the server advertises byte-range support for it. A 404 is reported as
// ErrNotFound.
func (b *HTTPBackend) head(ctx context.Context, key string) (ObjectInfo, bool, error) {
	req, err := b.newRequest(ctx, http.MethodHead, b.objectURL(key))
	if err != nil {
		return ObjectInfo{}, false, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return ObjectInfo{}, false, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ObjectInfo{}, false, fmt.Errorf("storage: stat %q: %w", key, ErrNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ObjectInfo{}, false, fmt.Errorf("storage: stat %q: unexpected status %s", key, resp.Status)
	}

	info := ObjectInfo{Key: strings.TrimPrefix(key, "/")}
	if resp.ContentLength >= 0 {
		info.Size = resp.ContentLength
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if t, perr := http.ParseTime(lm); perr == nil {
			info.ModTime = t
		}
	}
	acceptRanges := strings.Contains(strings.ToLower(resp.Header.Get("Accept-Ranges")), "bytes")
	return info, acceptRanges, nil
}

// Stat implements Backend using a HEAD probe.
func (b *HTTPBackend) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	info, _, err := b.head(ctx, key)
	return info, err
}

// Open implements Backend. When the server advertises byte-range support and a
// known size, it returns a lazy, ranged handle; otherwise it downloads the
// object in full to a temporary file and returns a handle over that file. Either
// way the returned handle satisfies the full ReadSeekCloser contract. A 404 is
// reported as ErrNotFound.
func (b *HTTPBackend) Open(ctx context.Context, key string) (ReadSeekCloser, error) {
	info, acceptRanges, err := b.head(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: %w", key, unwrapStat(err))
	}
	if acceptRanges && info.Size > 0 {
		return &httpRangeReader{
			ctx:    ctx,
			client: b.client,
			header: b.header,
			url:    b.objectURL(key),
			size:   info.Size,
		}, nil
	}
	return b.downloadToTemp(ctx, key)
}

// downloadToTemp fetches the whole object and buffers it to a temporary file,
// returning a seekable handle over that file. It is the fallback used when the
// server does not support ranged requests, so that the ReadSeekCloser seek
// contract still holds. The temporary file is removed when the handle is closed.
func (b *HTTPBackend) downloadToTemp(ctx context.Context, key string) (ReadSeekCloser, error) {
	req, err := b.newRequest(ctx, http.MethodGet, b.objectURL(key))
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: %w", key, err)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("storage: open %q: %w", key, ErrNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("storage: open %q: unexpected status %s", key, resp.Status)
	}

	tmp, err := os.CreateTemp("", "storage-http-*")
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: creating temp file: %w", key, err)
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("storage: open %q: buffering object: %w", key, err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("storage: open %q: rewinding temp file: %w", key, err)
	}
	return &tempFileReadSeekCloser{File: tmp}, nil
}

// List implements Backend by walking the file server's directory listings under
// the given prefix, which is treated as a directory path. A prefix that matches
// no directory (a 404 on its listing) yields an empty slice and a nil error, as
// required by the Backend contract.
func (b *HTTPBackend) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	dir := strings.Trim(path.Clean("/"+strings.TrimPrefix(prefix, "/")), "/")
	if dir == "." {
		dir = ""
	}
	var out []ObjectInfo
	visited := map[string]bool{}
	if err := b.walk(ctx, dir, visited, &out); err != nil {
		return nil, fmt.Errorf("storage: list %q: %w", prefix, err)
	}
	return out, nil
}

// walk fetches the directory listing at dir, appending a file ObjectInfo for
// every linked object and recursing into every linked subdirectory. A missing
// directory is not an error: it contributes nothing.
func (b *HTTPBackend) walk(ctx context.Context, dir string, visited map[string]bool, out *[]ObjectInfo) error {
	if visited[dir] {
		return nil
	}
	visited[dir] = true

	req, err := b.newRequest(ctx, http.MethodGet, b.dirURL(dir))
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	links, err := parseDirLinks(resp.Body)
	if err != nil {
		return err
	}
	for _, href := range links {
		seg, isDir, ok := normalizeHref(href)
		if !ok {
			continue
		}
		childKey := seg
		if dir != "" {
			childKey = dir + "/" + seg
		}
		if isDir {
			if err := b.walk(ctx, childKey, visited, out); err != nil {
				return err
			}
			continue
		}
		info, serr := b.Stat(ctx, childKey)
		if serr != nil {
			if isErrNotFound(serr) {
				continue
			}
			return serr
		}
		*out = append(*out, info)
	}
	return nil
}

// Close implements Backend. The HTTP backend holds no resources requiring
// explicit shutdown, so Close is a no-op and is safe to call repeatedly.
func (b *HTTPBackend) Close() error { return nil }

// normalizeHref decides whether a directory-listing href refers to a child entry
// of the current directory and, if so, returns its decoded name, whether it is a
// subdirectory (trailing slash), and ok=true. Absolute, parent ("..") and
// external links — anything that does not strictly descend into the listing —
// are rejected so a listing cannot walk outside the configured base.
func normalizeHref(href string) (name string, isDir bool, ok bool) {
	if i := strings.IndexAny(href, "?#"); i >= 0 {
		href = href[:i]
	}
	if href == "" || href == "." || href == "./" {
		return "", false, false
	}
	if strings.HasPrefix(href, "/") || strings.HasPrefix(href, "..") || strings.Contains(href, "://") {
		return "", false, false
	}
	isDir = strings.HasSuffix(href, "/")
	trimmed := strings.TrimSuffix(href, "/")
	if strings.Contains(trimmed, "/") {
		// A nested path in a single href is unexpected for a directory index;
		// ignore it rather than guessing its structure.
		return "", false, false
	}
	decoded, err := url.PathUnescape(trimmed)
	if err != nil {
		decoded = trimmed
	}
	if decoded == "" || decoded == "." || decoded == ".." {
		return "", false, false
	}
	return decoded, isDir, true
}

// parseDirLinks extracts the href targets of every anchor in an HTML directory
// listing. It tolerates the listing formats emitted by common file servers
// (Go's http.FileServer, nginx autoindex, Apache), all of which render entries
// as <a href="…"> links.
func parseDirLinks(r io.Reader) ([]string, error) {
	z := html.NewTokenizer(r)
	var links []string
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				return links, nil
			}
			return links, z.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "a" || !hasAttr {
				continue
			}
			for {
				key, val, more := z.TagAttr()
				if string(key) == "href" {
					links = append(links, string(val))
				}
				if !more {
					break
				}
			}
		}
	}
}

// httpRangeReader is the handle returned by HTTPBackend.Open when the server
// supports byte ranges. It satisfies the ReadSeekCloser contract by issuing a
// ranged GET starting at the current offset whenever a read needs bytes the
// currently open response body is not positioned to supply. Seeks are therefore
// cheap (they only move the logical offset) and reads lazily (re)open a ranged
// request as needed. This keeps partial EPUB reads from transferring the whole
// object. (A further optimization — reusing a single stream across adjacent
// seeks — is out of scope here; see Task C3.)
type httpRangeReader struct {
	ctx    context.Context
	client *http.Client
	header http.Header
	url    string
	size   int64

	pos     int64         // logical offset of the next Read
	body    io.ReadCloser // current response body, or nil if none is open
	bodyPos int64         // offset the open body will next supply
}

func (r *httpRangeReader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	if r.body == nil || r.bodyPos != r.pos {
		if err := r.reopen(); err != nil {
			return 0, err
		}
	}
	n, err := r.body.Read(p)
	r.pos += int64(n)
	r.bodyPos += int64(n)
	if err == io.EOF {
		r.closeBody()
	}
	return n, err
}

func (r *httpRangeReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.pos + offset
	case io.SeekEnd:
		abs = r.size + offset
	default:
		return 0, fmt.Errorf("storage: invalid seek whence %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("storage: seek to negative offset %d", abs)
	}
	if r.body != nil && abs != r.bodyPos {
		r.closeBody()
	}
	r.pos = abs
	return abs, nil
}

func (r *httpRangeReader) Close() error {
	r.closeBody()
	return nil
}

func (r *httpRangeReader) closeBody() {
	if r.body != nil {
		r.body.Close()
		r.body = nil
	}
}

// reopen issues a ranged GET starting at the current offset and installs the
// response body as the active stream. If the server ignores the range and
// returns the whole object (status 200), the leading bytes up to the current
// offset are discarded so the contract still holds.
func (r *httpRangeReader) reopen() error {
	r.closeBody()

	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	for name, values := range r.header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", r.pos))

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("storage: ranged read: unexpected status %s", resp.Status)
	}
	if resp.StatusCode == http.StatusOK && r.pos > 0 {
		if _, err := io.CopyN(io.Discard, resp.Body, r.pos); err != nil {
			resp.Body.Close()
			return fmt.Errorf("storage: ranged read: discarding to offset %d: %w", r.pos, err)
		}
	}
	r.body = resp.Body
	r.bodyPos = r.pos
	return nil
}

// unwrapStat normalizes an error produced by head so that Open can re-wrap it
// with its own prefix while preserving an ErrNotFound sentinel for callers.
func unwrapStat(err error) error {
	if isErrNotFound(err) {
		return ErrNotFound
	}
	return err
}

// isErrNotFound reports whether err is or wraps ErrNotFound.
func isErrNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// compile-time assurance that HTTPBackend implements Backend.
var _ Backend = (*HTTPBackend)(nil)
