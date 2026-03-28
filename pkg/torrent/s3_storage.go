package torrent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	g "github.com/anacrolix/generics"               // added
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	pieceMarkerPrefix = "completed-pieces/"
)

// S3Storage wraps standard file storage and adds S3 upload capability for completed files/pieces
type S3Storage struct {
	Bucket    string
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	BaseDir   string
	s3Client  *s3.Client

	uploadedMu     sync.Mutex
	uploaded       map[string]bool // keep track of uploaded files
	uploading      map[string]bool // keep track of in-progress uploads
	currentTorrent *s3Torrent
}

func NewS3Storage() *S3Storage {
	s := &S3Storage{
		Bucket:    "funnel",
		Endpoint:  "http://localhost:9000",
		AccessKey: "user",
		SecretKey: "password",
		Region:    "us-east-1",
		BaseDir:   "./downloads",
		uploaded:  make(map[string]bool),
		uploading: make(map[string]bool),
	}

	cfg, _ := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(s.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(s.AccessKey, s.SecretKey, "")),
	)

	s.s3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(s.Endpoint)
		o.UsePathStyle = true
	})

	return s
}

func (s *S3Storage) OpenTorrent(ctx context.Context, info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	localRoot := filepath.Join(s.BaseDir, infoHash.HexString())

	// Create our custom S3-backed piece completion provider
	pc := &s3PieceCompletion{
		s:        s,
		info:     info,
		infoHash: infoHash,
		cache:    make(map[int]bool),
	}
	// Initial sync from S3
	pc.syncFromS3()

	// Use NewFileOpts with our custom PieceCompletion
	fileStore := storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   localRoot,
		PieceCompletion: pc,
		UsePartFiles:    g.Some(false), // Disable .part suffix
	})

	tImpl, err := fileStore.OpenTorrent(ctx, info, infoHash)
	if err != nil {
		return storage.TorrentImpl{}, err
	}

	t := &s3Torrent{
		s:         s,
		info:      info,
		infoHash:  infoHash,
		tImpl:     tImpl,
		pc:        pc,
		localRoot: localRoot,
	}

	// Upload/Save metainfo to S3
	go t.saveMetainfo()

	originalPiece := tImpl.Piece
	tImpl.Piece = func(p metainfo.Piece) storage.PieceImpl {
		return &filePiece{
			PieceImpl: originalPiece(p),
			t:         t,
			piece:     p,
		}
	}

	s.uploadedMu.Lock()
	s.currentTorrent = t
	s.uploadedMu.Unlock()

	return tImpl, nil
}

func (s *S3Storage) TriggerSync() {
	s.uploadedMu.Lock()
	t := s.currentTorrent
	s.uploadedMu.Unlock()
	if t != nil {
		fmt.Printf("Initial sync trigger for %s: scanning for completed files...\n", t.infoHash.HexString())
		go t.checkAndUploadFiles()
	}
}

func (s *S3Storage) Close() error { return nil }

type s3Torrent struct {
	s         *S3Storage
	info      *metainfo.Info
	infoHash  metainfo.Hash
	tImpl     storage.TorrentImpl
	pc        *s3PieceCompletion
	localRoot string
}

func (t *s3Torrent) saveMetainfo() {
	data, err := json.MarshalIndent(t.info, "", "  ")
	if err != nil {
		return
	}
	s3Key := fmt.Sprintf("%s/metainfo.json", t.infoHash.HexString())
	_, _ = t.s.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(t.s.Bucket),
		Key:    aws.String(s3Key),
		Body:   strings.NewReader(string(data)),
	})
}

// s3PieceCompletion implements storage.PieceCompletion
type s3PieceCompletion struct {
	s        *S3Storage
	info     *metainfo.Info
	infoHash metainfo.Hash
	mu       sync.Mutex
	cache    map[int]bool
}

func (pc *s3PieceCompletion) Get(pk metainfo.PieceKey) (storage.Completion, error) {
	pc.mu.Lock()
	complete := pc.cache[pk.Index]
	pc.mu.Unlock()
	return storage.Completion{Complete: complete}, nil
}

func (pc *s3PieceCompletion) Set(pk metainfo.PieceKey, complete bool) error {
	pc.mu.Lock()
	pc.cache[pk.Index] = complete
	pc.mu.Unlock()

	if complete {
		go pc.uploadMarker(pk.Index)
	}
	return nil
}

func (pc *s3PieceCompletion) Close() error { return nil }

func (pc *s3PieceCompletion) getPieceHash(index int) string {
	if index < 0 || index >= pc.info.NumPieces() {
		return ""
	}
	return hex.EncodeToString(pc.info.Pieces[index*20 : (index+1)*20])
}

func (pc *s3PieceCompletion) syncFromS3() {
	prefix := fmt.Sprintf("%s/state/", pc.infoHash.HexString())
	fmt.Printf("Syncing piece markers for %s from S3...\n", pc.infoHash.HexString())

	paginator := s3.NewListObjectsV2Paginator(pc.s.s3Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(pc.s.Bucket),
		Prefix: aws.String(prefix),
	})

	pieceHashMap := make(map[string]int)
	for i := 0; i < pc.info.NumPieces(); i++ {
		pieceHashMap[pc.getPieceHash(i)] = i
	}

	count := 0
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			break
		}
		for _, obj := range page.Contents {
			hashHex := strings.TrimPrefix(*obj.Key, prefix)
			if idx, ok := pieceHashMap[hashHex]; ok {
				pc.mu.Lock()
				pc.cache[idx] = true
				pc.mu.Unlock()
				count++
			}
		}
	}
	fmt.Printf("Synced %d piece markers for torrent %s.\n", count, pc.infoHash.HexString())
}

func (pc *s3PieceCompletion) uploadMarker(index int) {
	hashHex := pc.getPieceHash(index)
	key := fmt.Sprintf("%s/state/%s", pc.infoHash.HexString(), hashHex)

	_, _ = pc.s.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(pc.s.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	})
}

type filePiece struct {
	storage.PieceImpl
	t     *s3Torrent
	piece metainfo.Piece
}

func (p *filePiece) MarkComplete() error {
	err := p.PieceImpl.MarkComplete()
	if err != nil {
		return err
	}

	// PieceImpl.MarkComplete will call pc.Set, which handles the S3 marker.
	// We just need to trigger the file-level upload check.
	go p.t.checkAndUploadFiles()

	return nil
}

func (p *s3Torrent) checkAndUploadFiles() {
	if len(p.info.Files) == 0 {
		localPath := filepath.Join(p.localRoot, p.info.Name)
		s3Key := fmt.Sprintf("%s/files/%s", p.infoHash.HexString(), p.info.Name)
		p.tryUpload(s3Key, localPath, 0, p.info.Length)
	} else {
		var currentOffset int64
		for _, file := range p.info.Files {
			fileOffset := currentOffset
			currentOffset += file.Length
			subPath := filepath.Join(file.Path...)
			localPath := filepath.Join(p.localRoot, p.info.Name, subPath)
			s3Key := fmt.Sprintf("%s/files/%s/%s", p.infoHash.HexString(), p.info.Name, subPath)
			p.tryUpload(s3Key, localPath, fileOffset, file.Length)
		}
	}
}

func (p *s3Torrent) tryUpload(s3Key, localPath string, offset, length int64) {
	p.s.uploadedMu.Lock()
	if p.s.uploaded[s3Key] || p.s.uploading[s3Key] {
		p.s.uploadedMu.Unlock()
		return
	}

	if p.isFileComplete(offset, length) {
		p.s.uploading[s3Key] = true
		p.s.uploadedMu.Unlock()

		fmt.Printf("File %s complete! Uploading to S3...\n", s3Key)
		if err := p.doUpload(s3Key, localPath); err != nil {
			fmt.Printf("Error uploading %s to S3: %v\n", s3Key, err)
			p.s.uploadedMu.Lock()
			delete(p.s.uploading, s3Key)
			p.s.uploadedMu.Unlock()
		} else {
			p.s.uploadedMu.Lock()
			p.s.uploaded[s3Key] = true
			delete(p.s.uploading, s3Key)
			p.s.uploadedMu.Unlock()
			fmt.Printf("Successfully uploaded %s to S3.\n", s3Key)
		}
	} else {
		p.s.uploadedMu.Unlock()
	}
}

func (p *s3Torrent) isFileComplete(offset, length int64) bool {
	if length == 0 {
		return true
	}
	firstPiece := int(offset / p.info.PieceLength)
	lastPiece := int((offset + length - 1) / p.info.PieceLength)

	// Check coverage via our piece completion provider
	for i := firstPiece; i <= lastPiece; i++ {
		comp, _ := p.pc.Get(metainfo.PieceKey{InfoHash: p.infoHash, Index: i})
		if !comp.Complete {
			return false
		}
	}
	return true
}

func (p *s3Torrent) doUpload(s3Key, localPath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = p.s.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(p.s.Bucket),
		Key:    aws.String(s3Key),
		Body:   file,
	})
	return err
}
