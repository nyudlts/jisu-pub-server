package serve

import (
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/readium/go-toolkit/cmd/rwp/cmd/serve/cache"
	"github.com/readium/go-toolkit/pkg/storage"
	"github.com/readium/go-toolkit/pkg/streamer"
)

type ServerConfig struct {
	Debug             bool
	BaseDirectory     string
	JSONIndent        string
	InferA11yMetadata streamer.InferA11yMetadata

	// Backend is the storage backend publications are read from. When nil, the
	// server defaults to a local-disk backend rooted at BaseDirectory, preserving
	// the original behavior.
	Backend storage.Backend
}

type Server struct {
	config ServerConfig
	router *mux.Router
	lfu    *cache.TinyLFU

	backend     storage.Backend
	backendOnce sync.Once
	backendErr  error
}

const MaxCachedPublicationAmount = 10
const MaxCachedPublicationTTL = time.Second * time.Duration(600)

func NewServer(config ServerConfig) *Server {
	return &Server{
		config:  config,
		backend: config.Backend,
		lfu:     cache.NewTinyLFU(MaxCachedPublicationAmount, MaxCachedPublicationTTL),
	}
}

// storageBackend returns the server's storage backend, lazily constructing a
// local-disk backend rooted at BaseDirectory when none was injected. Selection
// is explicit and validated eagerly: a misconfigured local root fails here
// rather than silently falling back.
func (s *Server) storageBackend() (storage.Backend, error) {
	s.backendOnce.Do(func() {
		if s.backend != nil {
			return
		}
		b, err := storage.NewLocalBackend(s.config.BaseDirectory)
		if err != nil {
			s.backendErr = err
			return
		}
		s.backend = b
	})
	return s.backend, s.backendErr
}
