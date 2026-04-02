package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type Store interface {
	Backend() string
	Root() string
	Location() string
	PutFile(ctx context.Context, tempPath, key, contentType string) error
	Open(ctx context.Context, key string) (ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
}

type Config struct {
	Backend            string
	Root               string
	S3Endpoint         string
	S3Region           string
	S3Bucket           string
	S3AccessKeyID      string
	S3SecretAccessKey  string
	S3UseSSL           bool
	S3UsePathStyle     bool
	S3Prefix           string
	S3AutoCreateBucket bool
}

func NewStore(ctx context.Context, cfg Config) (Store, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "", "local":
		return NewLocalFSStore(cfg.Root)
	case "s3":
		return NewS3Store(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported storage backend: %s", cfg.Backend)
	}
}
