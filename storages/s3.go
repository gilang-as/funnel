package storages

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	funnel "github.com/gilang/funnel"
)

// S3Config holds all S3 connection parameters including credentials.
type S3Config struct {
	Bucket         string
	Endpoint       string
	AccessKey      string
	SecretKey      string
	Region         string
	BaseDir        string
	ChunkSize      int64
	MaxLocalChunks int
}

// NewS3Storage creates a funnel.S3Storage backed by real AWS S3 / MinIO.
func NewS3Storage(cfg S3Config) (*funnel.S3Storage, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("S3 config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = true
	})
	return funnel.NewS3Storage(funnel.Config{
		Bucket:         cfg.Bucket,
		BaseDir:        cfg.BaseDir,
		ChunkSize:      cfg.ChunkSize,
		MaxLocalChunks: cfg.MaxLocalChunks,
	}, client), nil
}
