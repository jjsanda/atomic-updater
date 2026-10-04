package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 wraps the object store. Two clients: `ops` talks to the internal
// endpoint; `presign` is configured with the public endpoint because the
// SigV4 signature covers the Host header — presigned URLs must be minted
// for the hostname devices will actually dial.
type S3 struct {
	ops     *minio.Client
	presign *minio.Client
	bucket  string
}

type S3Config struct {
	Endpoint       string
	PublicEndpoint string
	AccessKey      string
	SecretKey      string
	Bucket         string
	UseSSL         bool
}

func OpenS3(ctx context.Context, cfg S3Config) (*S3, error) {
	creds := credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	ops, err := minio.New(cfg.Endpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	presign := ops
	if cfg.PublicEndpoint != "" && cfg.PublicEndpoint != cfg.Endpoint {
		presign, err = minio.New(cfg.PublicEndpoint, &minio.Options{Creds: creds, Secure: cfg.UseSSL})
		if err != nil {
			return nil, fmt.Errorf("s3 presign client: %w", err)
		}
	}
	s := &S3{ops: ops, presign: presign, bucket: cfg.Bucket}

	// Ensure the bucket exists (dev/e2e convenience; harmless in prod).
	// Retry briefly — compose starts MinIO in parallel with the server.
	deadline := time.Now().Add(60 * time.Second)
	for {
		exists, err := ops.BucketExists(ctx, cfg.Bucket)
		if err == nil {
			if !exists {
				if err := ops.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
					exists, _ = ops.BucketExists(ctx, cfg.Bucket) // lost a race is fine
					if !exists {
						return nil, fmt.Errorf("create bucket %s: %w", cfg.Bucket, err)
					}
				}
			}
			return s, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("object store unreachable after 60s: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *S3) PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	u, err := s.presign.PresignedGetObject(ctx, s.bucket, objectKey, ttl, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

type ObjectStat struct {
	Key  string
	Size int64
}

// ListPrefix returns all objects under the prefix (recursive).
func (s *S3) ListPrefix(ctx context.Context, prefix string) ([]ObjectStat, error) {
	var out []ObjectStat
	for obj := range s.ops.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, ObjectStat{Key: obj.Key, Size: obj.Size})
	}
	return out, nil
}

// ErrNoObject is returned by StatObject/GetObjectBytes for missing keys.
var ErrNoObject = errors.New("object not found")

func (s *S3) StatObject(ctx context.Context, key string) (int64, error) {
	info, err := s.ops.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
			return 0, ErrNoObject
		}
		return 0, err
	}
	return info.Size, nil
}

// GetObjectBytes fetches a small object (sidecar metadata) with a size cap.
func (s *S3) GetObjectBytes(ctx context.Context, key string, maxSize int64) ([]byte, error) {
	obj, err := s.ops.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, maxSize+1))
	if err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
			return nil, ErrNoObject
		}
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("object %s exceeds %d bytes", key, maxSize)
	}
	return data, nil
}

func (s *S3) Ping(ctx context.Context) error {
	exists, err := s.ops.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %s missing", s.bucket)
	}
	return nil
}
