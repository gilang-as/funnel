package funnel

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	defaultChunkSize     = 10 * 1024 * 1024 // 10MB
	maxLocalChunks       = 2
	maxConcurrentUploads = 4
	maxUploadRetries     = 3
	s3ReadTimeout        = 30 * time.Second
	s3WriteTimeout       = 5 * time.Minute
)

// S3Client adalah subset operasi S3 yang dipakai, memudahkan mocking di tests.
type S3Client interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	CreateMultipartUpload(ctx context.Context, params *s3.CreateMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	UploadPart(ctx context.Context, params *s3.UploadPartInput, optFns ...func(*s3.Options)) (*s3.UploadPartOutput, error)
	CompleteMultipartUpload(ctx context.Context, params *s3.CompleteMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// Config holds S3Storage configuration (no credentials).
type Config struct {
	Bucket         string
	BaseDir        string
	ChunkSize      int64
	MaxLocalChunks int
}

// S3Storage adalah storage backend untuk anacrolix/torrent yang menyimpan
// file langsung ke S3 via multipart upload berbasis chunk.
type S3Storage struct {
	Bucket         string
	BaseDir        string
	ChunkSize      int64
	MaxLocalChunks int

	client    S3Client
	uploadSem chan struct{} // membatasi concurrent S3 uploads
}

func NewS3Storage(cfg Config, client S3Client) *S3Storage {
	if cfg.ChunkSize == 0 {
		cfg.ChunkSize = defaultChunkSize
	}
	if cfg.MaxLocalChunks == 0 {
		cfg.MaxLocalChunks = maxLocalChunks
	}
	return &S3Storage{
		Bucket:         cfg.Bucket,
		BaseDir:        cfg.BaseDir,
		ChunkSize:      cfg.ChunkSize,
		MaxLocalChunks: cfg.MaxLocalChunks,
		uploadSem:      make(chan struct{}, maxConcurrentUploads),
		client:         client,
	}
}

func (s *S3Storage) OpenTorrent(ctx context.Context, info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	if info.TotalLength() > 100*1024*1024*1024 {
		return storage.TorrentImpl{}, fmt.Errorf("torrent total size (%d bytes) exceeds 100GB limit", info.TotalLength())
	}

	t := newS3Torrent(s, info, infoHash)

	// Load multipart state dari S3 untuk semua file secara paralel.
	var wg sync.WaitGroup
	for _, f := range t.files {
		wg.Add(1)
		go func(st *s3FileState) {
			defer wg.Done()
			st.loadMultipartState()
		}(f)
	}
	wg.Wait()

	// Jika semua file sudah ada di S3, tandai torrent selesai 100%.
	allDone := true
	for _, f := range t.files {
		if f.length > 0 && len(f.partETags) != len(f.chunks) {
			allDone = false
			break
		}
	}
	if allDone {
		log.Println("All files detected on S3. Forcing 100% completion status.")
		for i := 0; i < info.NumPieces(); i++ {
			t.pc.Set(metainfo.PieceKey{InfoHash: infoHash, Index: i}, true)
		}
	}

	go t.saveMetainfo()

	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return &s3Piece{t: t, piece: p}
		},
		Close: func() error { return nil },
	}, nil
}

func (s *S3Storage) Close() error { return nil }

// DeleteTorrentData removes all S3 objects under the torrent's prefix.
func (s *S3Storage) DeleteTorrentData(ctx context.Context, infoHash string) error {
	prefix := infoHash + "/"
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.Bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range out.Contents {
			if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(s.Bucket),
				Key:    obj.Key,
			}); err != nil {
				log.Printf("[WARN] delete %s: %v", *obj.Key, err)
			}
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	return nil
}

// s3ReadCtx returns a context with timeout for S3 read operations.
func s3ReadCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s3ReadTimeout)
}

// s3WriteCtx returns a context with timeout for S3 write/upload operations.
func s3WriteCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s3WriteTimeout)
}
