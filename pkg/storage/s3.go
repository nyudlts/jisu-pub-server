package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Backend is a Backend over objects in an S3 (or S3-compatible) bucket. It is
// the "ready but shelved" secondary source: implemented and conformance-tested
// now so the application can later switch to S3 as the primary EPUB source
// without rework, while the HTTP file server remains the current primary path.
//
// Keys are forward-slash, bucket-relative paths (see ObjectInfo.Key). They are
// resolved against an optional, fixed key prefix (S3Config.Prefix) so that a
// deployment can keep its publications under a sub-path of a shared bucket.
//
// Open uses the eager, temp-file strategy: the object is downloaded in full to a
// temporary file and a handle over that file is returned. This gives the handle
// native, cheap random access and therefore satisfies the full ReadSeekCloser
// seek contract without issuing a ranged request per Seek. The temporary file is
// removed when the handle is closed. (A lazy, ranged ReadSeeker is a separate,
// later optimization — see Task C3 — and is intentionally not done here.)
type S3Backend struct {
	client *s3.Client
	bucket string
	prefix string
}

// S3Config carries the non-secret configuration needed to construct an
// S3Backend. It deliberately holds no credential material: see NewS3Backend for
// how credentials are resolved.
type S3Config struct {
	// Bucket is the S3 bucket name. Required; NewS3Backend fails fast if empty.
	Bucket string

	// Region is the AWS region of the bucket (for example "us-east-1"). Optional:
	// when empty the SDK resolves the region from the environment/config chain
	// (AWS_REGION, ~/.aws/config, or the instance metadata service on AWS).
	Region string

	// Endpoint optionally overrides the S3 service endpoint. Leave empty for AWS
	// S3; set it to point at an S3-compatible service (MinIO, LocalStack) or, in
	// tests, at an in-process mock server. When set you almost always also want
	// UsePathStyle.
	Endpoint string

	// Prefix is an optional, fixed key prefix (without leading or trailing
	// slashes) under which this backend's objects live within the bucket. A
	// backend-relative key "epub.epub" with Prefix "books" maps to the S3 key
	// "books/epub.epub". Empty means the objects sit at the bucket root.
	Prefix string

	// UsePathStyle forces path-style addressing (https://endpoint/bucket/key)
	// instead of virtual-host addressing (https://bucket.endpoint/key). It is
	// required for most S3-compatible endpoints (MinIO, LocalStack) and for the
	// in-process test mock, whose host does not vary by bucket.
	UsePathStyle bool

	// Anonymous disables credential resolution entirely and signs no requests.
	// Set it for genuinely public buckets served anonymously over HTTPS (and for
	// the in-process test mock). When false, the SDK default credential chain is
	// used — see NewS3Backend.
	Anonymous bool
}

// NewS3Backend constructs an S3Backend from non-secret configuration, resolving
// credentials exclusively through the AWS SDK default credential chain. No
// credential-bearing values are accepted here or anywhere else in the config:
// per project policy there are deliberately no --s3-secret / --aws-secret style
// inputs, because flags leak to the process table and shell history and env-only
// secrets are what the SDK already expects.
//
// Three credential scenarios are supported without any secret-bearing config:
//
//  1. Public bucket (anonymous). The bucket is world-readable over HTTPS and no
//     credentials are needed. Set S3Config.Anonymous so the SDK signs nothing.
//     (This overlaps with the HTTP backend and that overlap is acceptable.)
//
//  2. Private bucket, running on AWS. Credentials are resolved automatically by
//     the SDK from the IAM instance/task role via the EC2/ECS metadata service.
//     The config here carries only bucket/region/endpoint — no keys.
//
//  3. Private bucket, running off AWS. The operator supplies standard
//     AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY environment variables (or a
//     ~/.aws/credentials profile); the SDK default credential chain resolves
//     them. Again, nothing credential-bearing passes through this config.
//
// Configuration is validated eagerly (fail fast, no fallback): a missing bucket
// is reported here at construction time rather than on first read. A network
// probe of the bucket is intentionally not performed, because a legitimately
// public bucket (scenario 1) may permit GetObject while denying the
// bucket-level permissions a HeadBucket probe would require.
func NewS3Backend(ctx context.Context, cfg S3Config) (*S3Backend, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("storage: s3 backend requires a bucket name (STORAGE_S3_BUCKET)")
	}

	loadOpts := []func(*config.LoadOptions) error{}
	if cfg.Region != "" {
		loadOpts = append(loadOpts, config.WithRegion(cfg.Region))
	}
	if cfg.Anonymous {
		loadOpts = append(loadOpts, config.WithCredentialsProvider(aws.AnonymousCredentials{}))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("storage: loading AWS configuration for bucket %q: %w", cfg.Bucket, err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &S3Backend{
		client: client,
		bucket: cfg.Bucket,
		prefix: strings.Trim(cfg.Prefix, "/"),
	}, nil
}

// fullKey maps a backend-relative key to the absolute S3 object key, applying
// the configured prefix.
func (b *S3Backend) fullKey(key string) string {
	key = strings.TrimPrefix(key, "/")
	if b.prefix == "" {
		return key
	}
	return b.prefix + "/" + key
}

// relKey maps an absolute S3 object key back to the backend-relative key by
// stripping the configured prefix.
func (b *S3Backend) relKey(full string) string {
	if b.prefix == "" {
		return full
	}
	return strings.TrimPrefix(full, b.prefix+"/")
}

// Open implements Backend. It downloads the object in full to a temporary file
// and returns a handle over that file, so the handle is natively seekable and
// satisfies the full ReadSeekCloser contract. The temporary file is deleted when
// the handle is closed.
func (b *S3Backend) Open(ctx context.Context, key string) (ReadSeekCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.fullKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("storage: open %q: %w", key, ErrNotFound)
		}
		return nil, fmt.Errorf("storage: open %q: %w", key, err)
	}
	defer out.Body.Close()

	tmp, err := os.CreateTemp("", "storage-s3-*")
	if err != nil {
		return nil, fmt.Errorf("storage: open %q: creating temp file: %w", key, err)
	}
	if _, err := io.Copy(tmp, out.Body); err != nil {
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

// Stat implements Backend using a HeadObject probe.
func (b *S3Backend) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.fullKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return ObjectInfo{}, fmt.Errorf("storage: stat %q: %w", key, ErrNotFound)
		}
		return ObjectInfo{}, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	info := ObjectInfo{Key: strings.TrimPrefix(key, "/")}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if out.LastModified != nil {
		info.ModTime = *out.LastModified
	}
	return info, nil
}

// List implements Backend by paginating ListObjectsV2 under the given prefix.
// The S3 key space is flat, so no entry is ever reported as a directory. A
// prefix that matches nothing yields an empty slice and a nil error.
func (b *S3Backend) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(b.fullKey(prefix)),
	})

	var infos []ObjectInfo
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("storage: list %q: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			oi := ObjectInfo{Key: b.relKey(*obj.Key)}
			if obj.Size != nil {
				oi.Size = *obj.Size
			}
			if obj.LastModified != nil {
				oi.ModTime = *obj.LastModified
			}
			infos = append(infos, oi)
		}
	}
	return infos, nil
}

// Close implements Backend. The S3 client holds no resources requiring explicit
// shutdown, so Close is a no-op and is safe to call repeatedly.
func (b *S3Backend) Close() error { return nil }

// tempFileReadSeekCloser is the handle returned by S3Backend.Open: an *os.File
// over the downloaded temporary copy that also removes that file on Close, so
// the eager download leaves nothing behind once the caller is done.
type tempFileReadSeekCloser struct {
	*os.File
}

func (h *tempFileReadSeekCloser) Close() error {
	name := h.File.Name()
	err := h.File.Close()
	if rmErr := os.Remove(name); rmErr != nil && err == nil {
		err = rmErr
	}
	return err
}

// isNotFound reports whether err represents a "missing object" condition from
// S3 (or an S3-compatible endpoint), across the several shapes the SDK may use:
// the typed NoSuchKey (GetObject) and NotFound (HeadObject) errors, a generic
// smithy API error carrying one of those codes, or a bare HTTP 404 from a
// non-AWS endpoint. Backends wrap this condition in ErrNotFound so callers can
// detect absence uniformly.
func isNotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	return false
}
