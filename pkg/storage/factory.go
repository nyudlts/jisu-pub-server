package storage

import (
	"context"
	"fmt"
	"strings"
)

// Mode names the storage backend to construct. It is selected explicitly at
// startup — there is deliberately no fallback between modes (see package doc and
// the project's storage plan): one mode is chosen, its required configuration is
// validated eagerly, and the matching backend is used for the whole process.
type Mode string

const (
	// ModeLocal selects the local-disk backend (LocalBackend). It is the parity
	// baseline; required config is the root directory (STORAGE_LOCAL_ROOT).
	ModeLocal Mode = "local"

	// ModeS3 selects the S3 backend (S3Backend). It is the secondary,
	// ready-but-shelved source; required config is the bucket name
	// (STORAGE_S3_BUCKET). Credentials are never config — see NewS3Backend.
	ModeS3 Mode = "s3"

	// ModeHTTP selects the HTTP file-server backend (HTTPBackend). It is the
	// primary runtime path and the default; required config is the base URL
	// (STORAGE_BASE_URL).
	ModeHTTP Mode = "http"
)

// DefaultMode is the mode assumed when none is configured. It is the HTTP file
// server, the project's primary runtime path.
const DefaultMode = ModeHTTP

// Config is the explicit, non-secret configuration consumed by NewBackend. Mode
// chooses which backend is built; only the nested configuration for that mode is
// consulted. No field carries credential material — per project policy secrets
// are never passed as configuration (AWS credentials come exclusively from the
// SDK default credential chain; see S3Config / NewS3Backend).
type Config struct {
	// Mode selects the backend to construct. Empty is treated as DefaultMode.
	Mode Mode

	// LocalRoot is the root directory for ModeLocal (STORAGE_LOCAL_ROOT).
	LocalRoot string

	// S3 configures ModeS3 (bucket/region/endpoint; STORAGE_S3_* ).
	S3 S3Config

	// HTTP configures ModeHTTP (base URL; STORAGE_BASE_URL).
	HTTP HTTPConfig
}

// NewBackend constructs the Backend selected by cfg.Mode, validating that
// mode's required configuration eagerly and failing fast — naming the missing
// variable — if it is absent. An empty mode defaults to DefaultMode; an
// unrecognized mode is an error. There is no fallback between modes: exactly the
// selected backend is built, or an error is returned.
//
// ctx is forwarded to backends that need it at construction time (S3, which
// resolves AWS configuration via the SDK default credential chain).
func NewBackend(ctx context.Context, cfg Config) (Backend, error) {
	mode := cfg.Mode
	if strings.TrimSpace(string(mode)) == "" {
		mode = DefaultMode
	}

	switch mode {
	case ModeLocal:
		if strings.TrimSpace(cfg.LocalRoot) == "" {
			return nil, fmt.Errorf("storage: mode %q requires a local root directory (STORAGE_LOCAL_ROOT)", mode)
		}
		b, err := NewLocalBackend(cfg.LocalRoot)
		if err != nil {
			return nil, err
		}
		return b, nil

	case ModeS3:
		if strings.TrimSpace(cfg.S3.Bucket) == "" {
			return nil, fmt.Errorf("storage: mode %q requires a bucket name (STORAGE_S3_BUCKET)", mode)
		}
		b, err := NewS3Backend(ctx, cfg.S3)
		if err != nil {
			return nil, err
		}
		return b, nil

	case ModeHTTP:
		if strings.TrimSpace(cfg.HTTP.BaseURL) == "" {
			return nil, fmt.Errorf("storage: mode %q requires a base URL (STORAGE_BASE_URL)", mode)
		}
		b, err := NewHTTPBackend(cfg.HTTP)
		if err != nil {
			return nil, err
		}
		return b, nil

	default:
		return nil, fmt.Errorf("storage: unknown mode %q (valid modes: %s)", cfg.Mode, strings.Join(modeNames(), ", "))
	}
}

// modeNames returns the valid mode strings in a stable order for error messages.
func modeNames() []string {
	return []string{string(ModeLocal), string(ModeS3), string(ModeHTTP)}
}
