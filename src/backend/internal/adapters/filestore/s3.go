// Package filestore stores submission files in S3-compatible object storage (SeaweedFS).
package filestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	BucketRaw       = "raw"       // originals from Moodle, stored as opaque bytes
	BucketSnapshots = "snapshots" // extracted code and report text
)

type Store struct{ c *minio.Client }

func New(ctx context.Context, endpoint, accessKey, secretKey string, secure bool) (*Store, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure, Region: "us-east-1", BucketLookup: minio.BucketLookupPath})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}

	for _, b := range []string{BucketRaw, BucketSnapshots} {
		exists, err := c.BucketExists(ctx, b)
		if err != nil {
			return nil, fmt.Errorf("check bucket %s: %w", b, err)
		}
		if !exists {
			// Several processes start together: another one may have created it just now.
			if err := c.MakeBucket(ctx, b, minio.MakeBucketOptions{Region: "us-east-1"}); err != nil && !strings.HasPrefix(minio.ToErrorResponse(err).Code, "BucketAlready") {
				return nil, fmt.Errorf("create bucket %s: %w", b, err)
			}
		}
	}

	return &Store{c: c}, nil
}

func (s *Store) Put(ctx context.Context, bucket, key string, data []byte) error {
	if _, err := s.c.PutObject(ctx, bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return fmt.Errorf("put %s/%s: %w", bucket, key, err)
	}

	return nil
}

var ErrNotFound = errors.New("object not found")

// ErrTooLarge means the object is larger than the caller asked for: cutting it would corrupt it silently.
var ErrTooLarge = errors.New("object is too large")

func (s *Store) Get(ctx context.Context, bucket, key string, maxBytes int64) ([]byte, error) {
	obj, err := s.c.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s/%s: %w", bucket, key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, maxBytes+1))
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, fmt.Errorf("%s/%s: %w", bucket, key, ErrNotFound)
		}
		return nil, fmt.Errorf("read %s/%s: %w", bucket, key, err)
	}

	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s/%s: %w", bucket, key, ErrTooLarge)
	}

	return data, nil
}
