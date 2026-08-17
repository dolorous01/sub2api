package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const maxImageJobObjectBytes int64 = 20 << 20

type disabledImageJobObjectStore struct{}

func (*disabledImageJobObjectStore) Put(context.Context, string, []byte, string) error {
	return service.ErrImageJobDisabled
}

func (*disabledImageJobObjectStore) Get(context.Context, string) (*service.ImageJobObject, error) {
	return nil, service.ErrImageJobDisabled
}

func (*disabledImageJobObjectStore) Delete(context.Context, string) error {
	return service.ErrImageJobDisabled
}

func (*disabledImageJobObjectStore) Health(context.Context) error {
	return service.ErrImageJobDisabled
}

func validateImageJobObjectKey(key string) error {
	if key == "" {
		return fmt.Errorf("image job object key is required")
	}
	if filepath.IsAbs(key) {
		return fmt.Errorf("image job object key must be relative")
	}
	if strings.Contains(key, "\\") {
		return fmt.Errorf("image job object key must not contain backslashes")
	}
	if strings.IndexByte(key, 0) >= 0 {
		return fmt.Errorf("image job object key must not contain NUL")
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("image job object key must not contain dot segments")
		}
	}
	return nil
}

func NewImageJobObjectStore(cfg config.ImageJobStorageConfig) (service.ImageJobObjectStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "local":
		return NewLocalImageJobObjectStore(cfg.LocalDirectory)
	case "s3":
		return newS3ImageJobObjectStore(cfg)
	default:
		return nil, fmt.Errorf("unsupported image job object storage driver %q", cfg.Driver)
	}
}

func ProvideImageJobObjectStore(cfg *config.Config) (service.ImageJobObjectStore, error) {
	if cfg == nil || !cfg.Gateway.ImageJobs.Enabled {
		return &disabledImageJobObjectStore{}, nil
	}
	return NewImageJobObjectStore(cfg.Gateway.ImageJobs.Storage)
}
