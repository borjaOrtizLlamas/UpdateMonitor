// Package s3 implements the ports.Storage interface backed by Amazon S3.
// All project data is stored as a single JSON file: <prefix>projects.json
package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/bortizllamas/updatemonitor/internal/domain"
)

const projectsFile = "projects.json"

// Storage is the S3-backed implementation of ports.Storage.
type Storage struct {
	client *s3.Client
	bucket string
	prefix string
	log    *slog.Logger
}

// New creates a new S3 storage adapter.
// endpoint is optional; when set (e.g. http://localhost:4566), it is used as
// a custom endpoint for local development with LocalStack or MinIO.
func New(ctx context.Context, bucket, region, prefix, endpoint string, log *slog.Logger) (*Storage, error) {
	log.Debug("s3: loading AWS config", "region", region, "endpoint", endpoint)

	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		log.Error("s3: failed to load AWS config", "err", err)
		return nil, fmt.Errorf("loading aws config: %w", err)
	}
	log.Debug("s3: AWS config loaded", "region", cfg.Region)

	var clientOpts []func(*s3.Options)
	if endpoint != "" {
		log.Debug("s3: using custom endpoint (LocalStack/MinIO)", "endpoint", endpoint)
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	} else {
		log.Debug("s3: no custom endpoint — using real AWS S3")
	}

	st := &Storage{
		client: s3.NewFromConfig(cfg, clientOpts...),
		bucket: bucket,
		prefix: prefix,
		log:    log,
	}
	log.Debug("s3: client created", "bucket", bucket, "prefix", prefix)
	return st, nil
}

// LoadProjects fetches the project store from S3.
// Returns an empty store if the object does not yet exist.
func (s *Storage) LoadProjects(ctx context.Context) (*domain.ProjectStore, error) {
	key := s.key(projectsFile)
	s.log.Debug("s3: LoadProjects called", "bucket", s.bucket, "key", key)

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if isNotFound(err, nsk) {
			s.log.Debug("s3: projects.json does not exist yet — returning empty store",
				"bucket", s.bucket, "key", key)
			return &domain.ProjectStore{}, nil
		}
		s.log.Error("s3: GetObject failed",
			"bucket", s.bucket, "key", key, "err", err)
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.log.Error("s3: failed to read response body", "key", key, "err", err)
		return nil, fmt.Errorf("reading s3 body: %w", err)
	}
	s.log.Debug("s3: GetObject success", "key", key, "bytes", len(raw))

	var store domain.ProjectStore
	if err := json.Unmarshal(raw, &store); err != nil {
		s.log.Error("s3: failed to unmarshal projects JSON", "key", key, "err", err)
		return nil, fmt.Errorf("unmarshal projects: %w", err)
	}
	s.log.Debug("s3: projects loaded", "count", len(store.Projects))
	return &store, nil
}

// SaveProjects serialises the project store and writes it to S3.
func (s *Storage) SaveProjects(ctx context.Context, store *domain.ProjectStore) error {
	key := s.key(projectsFile)
	s.log.Debug("s3: SaveProjects called", "bucket", s.bucket, "key", key, "projects", len(store.Projects))

	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		s.log.Error("s3: failed to marshal projects", "err", err)
		return fmt.Errorf("marshal projects: %w", err)
	}
	s.log.Debug("s3: marshalled projects JSON", "bytes", len(raw))

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(raw),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		s.log.Error("s3: PutObject failed",
			"bucket", s.bucket, "key", key, "bytes", len(raw), "err", err)
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	s.log.Debug("s3: PutObject success", "bucket", s.bucket, "key", key, "bytes", len(raw))
	return nil
}

func (s *Storage) key(filename string) string {
	return s.prefix + filename
}

// isNotFound detects 404-style errors from AWS SDK v2.
func isNotFound(err error, _ *types.NoSuchKey) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "NoSuchKey") || contains(err.Error(), "StatusCode: 404")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
