package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/neoscoder/lms-service/internal/platform/config"
)

type ObjectInfo struct {
	Size        int64
	ContentType string
	ETag        string
}

type Store interface {
	PresignUpload(context.Context, string, string, time.Duration) (string, error)
	PresignDownload(context.Context, string, time.Duration) (string, error)
	Head(context.Context, string) (ObjectInfo, error)
	Get(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, string, io.Reader, int64) (ObjectInfo, error)
}

type MinIO struct {
	client  *minio.Client
	presign *minio.Client
	bucket  string
}

func NewMinIO(cfg config.Storage) (*MinIO, error) {
	client, err := newClient(cfg.Endpoint, cfg)
	if err != nil {
		return nil, err
	}
	presign := client
	if cfg.PublicEndpoint != "" && strings.TrimSuffix(cfg.PublicEndpoint, "/") != strings.TrimSuffix(cfg.Endpoint, "/") {
		presign, err = newClient(cfg.PublicEndpoint, cfg)
		if err != nil {
			return nil, fmt.Errorf("initialize public object-storage signer: %w", err)
		}
	}
	return &MinIO{client: client, presign: presign, bucket: cfg.Bucket}, nil
}

func newClient(rawEndpoint string, cfg config.Storage) (*minio.Client, error) {
	endpoint := strings.TrimSuffix(rawEndpoint, "/")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse object-storage endpoint: %w", err)
	}
	host := parsed.Host
	secure := parsed.Scheme == "https"
	if host == "" {
		host = parsed.Path
	}
	client, err := minio.New(host, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: secure, Region: cfg.Region, BucketLookup: minio.BucketLookupPath})
	if err != nil {
		return nil, fmt.Errorf("initialize object storage: %w", err)
	}
	return client, nil
}
func (s *MinIO) PresignUpload(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	u, err := s.presign.PresignedPutObject(ctx, s.bucket, key, ttl)
	if err != nil {
		return "", fmt.Errorf("presign upload: %w", err)
	}
	return u.String(), nil
}
func (s *MinIO) PresignDownload(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := s.presign.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("presign download: %w", err)
	}
	return u.String(), nil
}
func (s *MinIO) Head(ctx context.Context, key string) (ObjectInfo, error) {
	v, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat object: %w", err)
	}
	return ObjectInfo{Size: v.Size, ContentType: v.ContentType, ETag: v.ETag}, nil
}
func (s *MinIO) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	return object, nil
}
func (s *MinIO) Put(ctx context.Context, key, contentType string, reader io.Reader, size int64) (ObjectInfo, error) {
	v, err := s.client.PutObject(ctx, s.bucket, key, reader, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("put object: %w", err)
	}
	return ObjectInfo{Size: v.Size, ContentType: contentType, ETag: v.ETag}, nil
}

type FakeStore struct {
	Objects  map[string]ObjectInfo
	Payloads map[string][]byte
}

func NewFakeStore() *FakeStore {
	return &FakeStore{Objects: map[string]ObjectInfo{}, Payloads: map[string][]byte{}}
}
func (f *FakeStore) PresignUpload(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://storage.test/upload/" + url.PathEscape(key), nil
}
func (f *FakeStore) PresignDownload(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://storage.test/download/" + url.PathEscape(key), nil
}
func (f *FakeStore) Head(_ context.Context, key string) (ObjectInfo, error) {
	v, ok := f.Objects[key]
	if !ok {
		return ObjectInfo{}, fmt.Errorf("object %q not found", key)
	}
	return v, nil
}
func (f *FakeStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	payload, ok := f.Payloads[key]
	if !ok {
		return nil, fmt.Errorf("object %q not found", key)
	}
	return io.NopCloser(strings.NewReader(string(payload))), nil
}
func (f *FakeStore) Put(_ context.Context, key, contentType string, r io.Reader, size int64) (ObjectInfo, error) {
	b, err := io.ReadAll(io.LimitReader(r, size+1))
	if err != nil {
		return ObjectInfo{}, err
	}
	v := ObjectInfo{Size: int64(len(b)), ContentType: contentType, ETag: "fake"}
	f.Objects[key] = v
	f.Payloads[key] = b
	return v, nil
}
