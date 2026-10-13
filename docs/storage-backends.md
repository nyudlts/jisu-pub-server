# Storage Backend Operator Guide

This server supports three storage modes selected explicitly at startup.
There is no graceful fallback between backends: one mode is chosen, validated
at process start, and used for the whole runtime.

## Configuration precedence

1. CLI flag value
2. Environment variable value
3. Built-in default (for `STORAGE`, default is `http`)

## Common policy

- Do not pass credential material in CLI flags.
- S3 credentials are resolved only via the AWS SDK default credential chain.
- Missing required mode config causes startup failure with a named variable.

## Mode: HTTP (primary, default)

Use this when EPUBs are already hosted by a public HTTP(S) file server.

Required:
- `STORAGE=http`
- `STORAGE_BASE_URL=<https://host/path>`

Optional CLI equivalents:
- `--storage http`
- `--base-url <https://host/path>`

Example:

```bash
STORAGE=http \
STORAGE_BASE_URL=https://files.example.com/books \
rwp serve
```

Seek and range behavior:
- Preferred path: server advertises `Accept-Ranges: bytes`; backend uses range requests.
- Compatibility path: if ranges are unsupported, backend downloads the file to a temporary local file and serves reads from that file.
- Operator recommendation: enable byte-range support on the file server for better partial-read performance.

## Mode: Local

Use this for local/regression operation where EPUB files are on disk.

Required:
- `STORAGE=local`
- `STORAGE_LOCAL_ROOT=<directory-with-epubs>`

Optional CLI equivalents:
- `--storage local`
- `--local-root <directory-with-epubs>`

Example:

```bash
STORAGE=local \
STORAGE_LOCAL_ROOT=/data/books \
rwp serve
```

## Mode: S3 (ready, secondary)

Use this when EPUBs are stored in an S3-compatible bucket.

Required:
- `STORAGE=s3`
- `STORAGE_S3_BUCKET=<bucket-name>`

Optional:
- `STORAGE_S3_REGION=<region>`
- `STORAGE_S3_ENDPOINT=<endpoint>`

Optional CLI equivalents:
- `--storage s3`
- `--s3-bucket <bucket-name>`
- `--s3-region <region>`
- `--s3-endpoint <endpoint>`

Example:

```bash
STORAGE=s3 \
STORAGE_S3_BUCKET=my-books \
STORAGE_S3_REGION=us-east-1 \
rwp serve
```

Credentials:
- On AWS: use IAM role credentials.
- Off AWS: use standard AWS credential env vars or shared credentials files.
- No `--s3-access-key`, `--s3-secret`, or other secret-bearing flags.

## Quick CLI examples

```bash
# HTTP (default mode)
rwp serve --storage http --base-url https://files.example.com/books

# Local
rwp serve --storage local --local-root /data/books

# S3
rwp serve --storage s3 --s3-bucket my-books --s3-region us-east-1
```
