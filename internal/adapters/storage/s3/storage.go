// Package s3 implements the ports.Storage interface backed by Amazon S3.
// All project data is stored as a single JSON file: <prefix>projects.json
package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

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
}

// New creates a new S3 storage adapter.
// endpoint is optional; when set (e.g. http://localhost:4566), it is used as
// a custom endpoint for local development with LocalStack or MinIO.
func New(ctx context.Context, bucket, region, prefix, endpoint string) (*Storage, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}

	var clientOpts []func(*s3.Options)
	if endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true // required for MinIO / LocalStack
		})
	}

	return &Storage{
		client: s3.NewFromConfig(cfg, clientOpts...),
		bucket: bucket,
		prefix: prefix,
	}, nil
}

// LoadProjects fetches the project store from S3.
// Returns an empty store if the object does not yet exist.
func (s *Storage) LoadProjects(ctx context.Context) (*domain.ProjectStore, error) {
	key := s.key(projectsFile)
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// If the file simply doesn't exist yet, return an empty store.
		var nsk *types.NoSuchKey
		if isNotFound(err, nsk) {
			return &domain.ProjectStore{}, nil
		}
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading s3 body: %w", err)
	}

	var store domain.ProjectStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("unmarshal projects: %w", err)
	}
	return &store, nil
}

// SaveProjects serialises the project store and writes it to S3.
func (s *Storage) SaveProjects(ctx context.Context, store *domain.ProjectStore) error {
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal projects: %w", err)
	}

	key := s.key(projectsFile)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(raw),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

func (s *Storage) key(filename string) string {
	return s.prefix + filename
}

// isNotFound is a helper to detect 404-style errors from AWS without importing
// the full errors package from aws-sdk-go-v2.
func isNotFound(err error, _ *types.NoSuchKey) bool {
	if err == nil {
		return false
	}
	// AWS SDK v2 wraps the error; a simple string check is sufficient here.
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
