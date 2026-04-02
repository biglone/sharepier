package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Store struct {
	client   *minio.Client
	root     string
	bucket   string
	prefix   string
	endpoint string
}

func NewS3Store(ctx context.Context, cfg Config) (*S3Store, error) {
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		return nil, errors.New("storage root is required for s3 backend")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	endpoint := strings.TrimSpace(cfg.S3Endpoint)
	bucket := strings.TrimSpace(cfg.S3Bucket)
	accessKeyID := strings.TrimSpace(cfg.S3AccessKeyID)
	secretAccessKey := strings.TrimSpace(cfg.S3SecretAccessKey)

	switch {
	case endpoint == "":
		return nil, errors.New("s3 endpoint is required")
	case bucket == "":
		return nil, errors.New("s3 bucket is required")
	case accessKeyID == "":
		return nil, errors.New("s3 access key is required")
	case secretAccessKey == "":
		return nil, errors.New("s3 secret access key is required")
	}

	bucketLookup := minio.BucketLookupAuto
	if cfg.S3UsePathStyle {
		bucketLookup = minio.BucketLookupPath
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure:       cfg.S3UseSSL,
		Region:       strings.TrimSpace(cfg.S3Region),
		BucketLookup: bucketLookup,
	})
	if err != nil {
		return nil, err
	}

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if !cfg.S3AutoCreateBucket {
			return nil, fmt.Errorf("s3 bucket %q does not exist", bucket)
		}
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: strings.TrimSpace(cfg.S3Region)}); err != nil {
			return nil, err
		}
	}

	return &S3Store{
		client:   client,
		root:     root,
		bucket:   bucket,
		prefix:   normalizeObjectPrefix(cfg.S3Prefix),
		endpoint: endpoint,
	}, nil
}

func (s *S3Store) Backend() string {
	return "s3"
}

func (s *S3Store) Root() string {
	return s.root
}

func (s *S3Store) Location() string {
	if s.prefix == "" {
		return fmt.Sprintf("s3://%s@%s", s.bucket, s.endpoint)
	}

	return fmt.Sprintf("s3://%s/%s@%s", s.bucket, s.prefix, s.endpoint)
}

func (s *S3Store) PutFile(ctx context.Context, tempPath, key, contentType string) error {
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}

	_, err := s.client.FPutObject(ctx, s.bucket, s.objectKey(key), tempPath, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return err
	}

	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func (s *S3Store) Open(ctx context.Context, key string) (ReadSeekCloser, error) {
	object, err := s.client.GetObject(ctx, s.bucket, s.objectKey(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}

	if _, err := object.Stat(); err != nil {
		object.Close()
		return nil, err
	}

	return object, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, s.objectKey(key), minio.RemoveObjectOptions{})
	if err != nil {
		var objectError minio.ErrorResponse
		if errors.As(err, &objectError) && objectError.Code == "NoSuchKey" {
			return nil
		}
		return err
	}

	return nil
}

func (s *S3Store) objectKey(key string) string {
	cleanKey := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(key)), "/")
	if s.prefix == "" {
		return cleanKey
	}

	return s.prefix + "/" + cleanKey
}

func normalizeObjectPrefix(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "/")
	if value == "" || value == "." {
		return ""
	}

	return value
}
