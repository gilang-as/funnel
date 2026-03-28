package torrent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	defaultChunkSize = 10 * 1024 * 1024 // 10MB
	maxLocalChunks   = 2                // Max chunks to keep on disk
)

// S3Storage manages chunk-based storage for torrents with S3 synchronization
type S3Storage struct {
	Bucket         string
	Endpoint       string
	AccessKey      string
	SecretKey      string
	Region         string
	BaseDir        string
	ChunkSize      int64 // Custom chunk size (min 5MB)
	MaxLocalChunks int   // Max concurrent chunks on disk
	s3Client       *s3.Client

	mu             sync.Mutex
	currentTorrent *s3Torrent
}

func NewS3Storage() *S3Storage {
	s := &S3Storage{
		Bucket:         "funnel",
		Endpoint:       "http://localhost:9000",
		AccessKey:      "user",
		SecretKey:      "password",
		Region:         "us-east-1",
		BaseDir:        "./downloads",
		ChunkSize:      defaultChunkSize,
		MaxLocalChunks: maxLocalChunks,
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
	// Enforce 100GB total limit per torrent
	if info.TotalLength() > 100*1024*1024*1024 {
		return storage.TorrentImpl{}, fmt.Errorf("torrent total size (%d bytes) exceeds 100GB limit", info.TotalLength())
	}

	t := &s3Torrent{
		s:        s,
		info:     info,
		infoHash: infoHash,
		files:    make(map[string]*s3FileState),
	}

	if len(info.Files) == 0 {
		fmt.Printf("Initializng single-file torrent: %s (%d bytes)\n", info.Name, info.Length)
		t.orderedFilePaths = append(t.orderedFilePaths, info.Name)
		t.initFile(info.Name, info.Length)
	} else {
		fmt.Printf("Initializng multi-file torrent: %d files\n", len(info.Files))
		for _, f := range info.Files {
			path := filepath.Join(f.Path...)
			t.orderedFilePaths = append(t.orderedFilePaths, path)
			t.initFile(path, f.Length)
		}
	}

	// Piece completion provider (S3 backed)
	pc := &s3PieceCompletion{
		s:        s,
		info:     info,
		infoHash: infoHash,
		cache:    make(map[int]bool),
	}
	pc.syncFromS3()

	t.pc = pc

	s.mu.Lock()
	s.currentTorrent = t
	s.mu.Unlock()

	// Metainfo
	go t.saveMetainfo()

	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return &s3Piece{
				t:     t,
				piece: p,
			}
		},
		Close: func() error { return nil },
	}, nil
}

func (s *S3Storage) TriggerSync() {
	// Not needed for new architecture as it uploads as it goes
}

func (s *S3Storage) Close() error { return nil }

type s3Torrent struct {
	s                *S3Storage
	info             *metainfo.Info
	infoHash         metainfo.Hash
	pc               *s3PieceCompletion
	files            map[string]*s3FileState
	orderedFilePaths []string
	mu               sync.Mutex
}

func (t *s3Torrent) initFile(relPath string, length int64) {
	fmt.Printf("[INIT] File: %s (Size: %d)\n", relPath, length)
	chunkSize := t.s.ChunkSize
	if chunkSize < defaultChunkSize {
		chunkSize = defaultChunkSize
	}
	// S3 limit: 10,000 parts per file. (5TB / 10,000 = 500MB)
	// We ensure each file doesn't exceed 10,000 parts.
	if length > chunkSize*10000 {
		chunkSize = (length / 10000) + 1
	}

	numChunks := int((length + chunkSize - 1) / chunkSize)
	if length == 0 {
		numChunks = 0
	}

	state := &s3FileState{
		t:            t,
		relPath:      relPath,
		length:       length,
		chunkSize:    chunkSize,
		chunks:       make([]*s3Chunk, numChunks),
		localLimit:   t.s.MaxLocalChunks,
	}

	if numChunks == 0 {
		fmt.Printf("[INIT] Empty File: %s\n", relPath)
	}

	for i := 0; i < numChunks; i++ {
		start := int64(i) * chunkSize
		end := start + chunkSize
		if end > length {
			end = length
		}
		state.chunks[i] = &s3Chunk{
			index: i,
			start: start,
			end:   end,
			state: state,
		}
	}

	t.files[relPath] = state
	go state.loadMultipartState()
}

func (t *s3Torrent) saveMetainfo() {
	data, _ := json.MarshalIndent(t.info, "", "  ")
	s3Key := fmt.Sprintf("%s/metainfo.json", t.infoHash.HexString())
	_, _ = t.s.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(t.s.Bucket),
		Key:    aws.String(s3Key),
		Body:   strings.NewReader(string(data)),
	})
}

// s3FileState manages a single file's multipart upload and chunks
type s3FileState struct {
	t            *s3Torrent
	relPath      string
	length       int64
	chunkSize    int64
	chunks       []*s3Chunk
	uploadID     string
	partETags    map[int]string
	localLimit   int
	activeMu     sync.Mutex
	activeChunks []int // Simple LRU-like list
}

func (f *s3FileState) loadMultipartState() {
	// Try to load existing UploadID from S3
	stateKey := fmt.Sprintf("%s/state/multipart/%s.json", f.t.infoHash.HexString(), hex.EncodeToString([]byte(f.relPath)))
	output, err := f.t.s.s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(f.t.s.Bucket),
		Key:    aws.String(stateKey),
	})

	f.activeMu.Lock()
	defer f.activeMu.Unlock()
	f.partETags = make(map[int]string)

	if err == nil {
		defer output.Body.Close()
		var data struct {
			UploadID  string
			PartETags map[int]string
		}
		if json.NewDecoder(output.Body).Decode(&data) == nil {
			f.uploadID = data.UploadID
			f.partETags = data.PartETags
			fmt.Printf("Resuming multipart upload for %s: %s\n", f.relPath, f.uploadID)
		}
	}

	if f.length == 0 {
		s3Key := f.getS3Key()
		_, err := f.t.s.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket: aws.String(f.t.s.Bucket),
			Key:    aws.String(s3Key),
			Body:   strings.NewReader(""),
		})
		if err == nil {
			fmt.Printf("[UPLOADED] %s | Empty File to S3\n", f.relPath)
		}
		return
	}

	if f.uploadID == "" {
		s3Key := f.getS3Key()
		res, err := f.t.s.s3Client.CreateMultipartUpload(context.TODO(), &s3.CreateMultipartUploadInput{
			Bucket: aws.String(f.t.s.Bucket),
			Key:    aws.String(s3Key),
		})
		if err == nil {
			f.uploadID = *res.UploadId
			f.saveState()
		}
	}
}

func (f *s3FileState) getS3Key() string {
	if len(f.t.info.Files) == 0 {
		return fmt.Sprintf("%s/files/%s", f.t.infoHash.HexString(), f.t.info.Name)
	}
	return fmt.Sprintf("%s/files/%s/%s", f.t.infoHash.HexString(), f.t.info.Name, f.relPath)
}

func (f *s3FileState) saveState() {
	stateKey := fmt.Sprintf("%s/state/multipart/%s.json", f.t.infoHash.HexString(), hex.EncodeToString([]byte(f.relPath)))
	data := struct {
		UploadID  string
		PartETags map[int]string
	}{
		UploadID:  f.uploadID,
		PartETags: f.partETags,
	}
	bytes, _ := json.Marshal(data)
	_, _ = f.t.s.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(f.t.s.Bucket),
		Key:    aws.String(stateKey),
		Body:   strings.NewReader(string(bytes)),
	})
}

// s3Chunk represents a 5MB segment of a file
type s3Chunk struct {
	index     int
	start     int64
	end       int64
	state     *s3FileState
	uploading bool
}

func (c *s3Chunk) localPath() string {
	return filepath.Join(c.state.t.s.BaseDir, c.state.t.infoHash.HexString(), "chunks", hex.EncodeToString([]byte(c.state.relPath)), fmt.Sprintf("chunk.%d", c.index))
}

func (c *s3Chunk) isComplete() bool {
	// Check if all pieces covering this chunk are complete
	fileOffsetInTorrent := c.state.getFileOffsetInTorrent()
	absStart := fileOffsetInTorrent + c.start
	absEnd := fileOffsetInTorrent + c.end

	pieceLength := c.state.t.info.PieceLength
	firstPiece := int(absStart / pieceLength)
	lastPiece := int((absEnd - 1) / pieceLength)

	for i := firstPiece; i <= lastPiece; i++ {
		comp, _ := c.state.t.pc.Get(metainfo.PieceKey{InfoHash: c.state.t.infoHash, Index: i})
		if !comp.Complete {
			return false
		}
	}
	return true
}

func (f *s3FileState) getFileOffsetInTorrent() int64 {
	var offset int64
	for _, path := range f.t.orderedFilePaths {
		if path == f.relPath {
			return offset
		}
		offset += f.t.files[path].length
	}
	return 0
}

func (c *s3Chunk) uploadIfComplete() {
	c.state.activeMu.Lock()
	if c.uploading || c.state.partETags[c.index+1] != "" {
		c.state.activeMu.Unlock()
		return
	}

	if c.isComplete() {
		c.uploading = true
		c.state.activeMu.Unlock()
		go c.doUpload()
	} else {
		c.state.activeMu.Unlock()
	}
}

func (c *s3Chunk) doUpload() {
	path := c.localPath()
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	s3Key := c.state.getS3Key()
	res, err := c.state.t.s.s3Client.UploadPart(context.TODO(), &s3.UploadPartInput{
		Bucket:     aws.String(c.state.t.s.Bucket),
		Key:        aws.String(s3Key),
		PartNumber: aws.Int32(int32(c.index + 1)),
		UploadId:   aws.String(c.state.uploadID),
		Body:       file,
	})

	c.state.activeMu.Lock()
	c.uploading = false
	if err == nil {
		c.state.partETags[c.index+1] = *res.ETag
		fmt.Printf("[UPLOADED] %s | Chunk %d/%d to S3\n", c.state.relPath, c.index+1, len(c.state.chunks))
		c.state.saveState()

		// NOW upload piece markers for this chunk
		go c.uploadPieceMarkers()

		c.state.checkFullCompletion()
	} else {
		fmt.Printf("[FAILED] %s | Chunk %d/%d upload: %v\n", c.state.relPath, c.index+1, len(c.state.chunks), err)
	}
	c.state.activeMu.Unlock()

	c.state.gcLocalChunks()
}

func (c *s3Chunk) uploadPieceMarkers() {
	fileOffsetInTorrent := c.state.getFileOffsetInTorrent()
	absStart := fileOffsetInTorrent + c.start
	absEnd := fileOffsetInTorrent + c.end

	pieceLength := c.state.t.info.PieceLength
	firstPiece := int(absStart / pieceLength)
	lastPiece := int((absEnd - 1) / pieceLength)

	for i := firstPiece; i <= lastPiece; i++ {
		c.state.t.pc.uploadMarker(i)
	}
}

func (f *s3FileState) checkFullCompletion() {
	if f.length == 0 { return }
	if len(f.partETags) == len(f.chunks) {
		fmt.Printf("File %s fully uploaded! Finalizing S3 Multipart Upload...\n", f.relPath)
		var parts []types.CompletedPart
		for num, etag := range f.partETags {
			parts = append(parts, types.CompletedPart{
				PartNumber: aws.Int32(int32(num)),
				ETag:       aws.String(etag),
			})
		}
		sort.Slice(parts, func(i, j int) bool {
			return *parts[i].PartNumber < *parts[j].PartNumber
		})

		s3Key := f.getS3Key()
		_, err := f.t.s.s3Client.CompleteMultipartUpload(context.TODO(), &s3.CompleteMultipartUploadInput{
			Bucket:          aws.String(f.t.s.Bucket),
			Key:             aws.String(s3Key),
			UploadId:        aws.String(f.uploadID),
			MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
		})
		if err == nil {
			fmt.Printf("File %s successfully completed in S3.\n", f.relPath)
		}
	}
}

func (f *s3FileState) gcLocalChunks() {
	f.activeMu.Lock()
	defer f.activeMu.Unlock()

	for len(f.activeChunks) > f.localLimit {
		// Remove oldest chunk that is already uploaded
		removedIdx := -1
		for i, idx := range f.activeChunks {
			if f.partETags[idx+1] != "" {
				removedIdx = i
				break
			}
		}
		if removedIdx == -1 {
			break
		}

		idx := f.activeChunks[removedIdx]
		f.activeChunks = append(f.activeChunks[:removedIdx], f.activeChunks[removedIdx+1:]...)
		os.Remove(f.chunks[idx].localPath())
		fmt.Printf("GC: Local chunk %d of %s removed.\n", idx, f.relPath)
	}
}

// s3Piece implements storage.PieceImpl
type s3Piece struct {
	t     *s3Torrent
	piece metainfo.Piece
}

func (p *s3Piece) ReadAt(b []byte, off int64) (n int, err error) {
	absOff := p.piece.Offset() + off
	return p.t.readAbsolute(b, absOff)
}

func (p *s3Piece) WriteAt(b []byte, off int64) (n int, err error) {
	absOff := p.piece.Offset() + off
	return p.t.writeAbsolute(b, absOff)
}

func (t *s3Torrent) readAbsolute(b []byte, absOff int64) (int, error) {
	var nTotal int
	for nTotal < len(b) {
		segOff := absOff + int64(nTotal)
		target := b[nTotal:]
		
		state, localOff, err := t.findFile(segOff)
		if err != nil {
			return nTotal, err
		}

		chunkIdx := int(localOff / state.chunkSize)
		chunk := state.chunks[chunkIdx]
		chunkOff := localOff % state.chunkSize

		// Max bytes available in this chunk segment
		canReadInChunk := int(min(int64(len(target)), chunk.end-localOff))
		readTarget := target[:canReadInChunk]

		readDone := false
		// 1. Try local chunk
		path := chunk.localPath()
		if f, err := os.Open(path); err == nil {
			n, err := f.ReadAt(readTarget, chunkOff)
			f.Close()
			if err == nil || (n > 0 && err == io.EOF) {
				nTotal += n
				readDone = true
			}
		}

		if readDone {
			continue
		}

		// 2. Fallback to S3
		state.activeMu.Lock()
		isUploaded := state.partETags[chunkIdx+1] != ""
		state.activeMu.Unlock()

		if isUploaded {
			s3Key := state.getS3Key()
			// For S3 proxying, we can actually fetch the whole remaining buffer if it's all in S3,
			// but to be safe and simple, we stick to the chunk boundary logic.
			output, err := t.s.s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
				Bucket: aws.String(t.s.Bucket),
				Key:    aws.String(s3Key),
				Range:  aws.String(fmt.Sprintf("bytes=%d-%d", localOff, localOff+int64(canReadInChunk)-1)),
			})
			if err == nil {
				n, err := io.ReadFull(output.Body, readTarget)
				output.Body.Close()
				if err == nil || err == io.ErrUnexpectedEOF {
					nTotal += n
					continue
				}
			}
		}

		return nTotal, fmt.Errorf("chunk %d not available locally or on S3 (file: %s)", chunkIdx, state.relPath)
	}
	return nTotal, nil
}

func (t *s3Torrent) writeAbsolute(b []byte, absOff int64) (int, error) {
	var nTotal int
	for nTotal < len(b) {
		segOff := absOff + int64(nTotal)
		target := b[nTotal:]

		state, localOff, err := t.findFile(segOff)
		if err != nil {
			return nTotal, err
		}

		chunkIdx := int(localOff / state.chunkSize)
		chunk := state.chunks[chunkIdx]
		chunkOff := localOff % state.chunkSize

		canWrite := int(min(int64(len(target)), chunk.end-localOff))
		writeTarget := target[:canWrite]

		path := chunk.localPath()
		os.MkdirAll(filepath.Dir(path), 0755)
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			return nTotal, err
		}

		n, err := f.WriteAt(writeTarget, chunkOff)
		f.Close()
		if err != nil {
			return nTotal + n, err
		}

		state.activeMu.Lock()
		exists := false
		for _, idx := range state.activeChunks {
			if idx == chunkIdx {
				exists = true
				break
			}
		}
		if !exists {
			state.activeChunks = append(state.activeChunks, chunkIdx)
		}
		state.activeMu.Unlock()

		nTotal += n
	}
	return nTotal, nil
}

func (t *s3Torrent) findFile(absOff int64) (*s3FileState, int64, error) {
	var current int64
	for _, path := range t.orderedFilePaths {
		f := t.files[path]
		if absOff >= current && absOff < current+f.length {
			return f, absOff - current, nil
		}
		current += f.length
	}
	return nil, 0, fmt.Errorf("offset %d out of range", absOff)
}

func (p *s3Piece) MarkComplete() error {
	pk := metainfo.PieceKey{InfoHash: p.t.infoHash, Index: p.piece.Index()}
	p.t.pc.Set(pk, true)

	// Trigger chunk upload check for all chunks covered by this piece
	start := p.piece.Offset()
	end := start + p.piece.Length()

	for off := start; off < end; {
		state, localOff, err := p.t.findFile(off)
		if err != nil {
			break
		}

		chunkIdx := int(localOff / state.chunkSize)
		chunk := state.chunks[chunkIdx]
		chunk.uploadIfComplete()

		// Move to next chunk boundary
		off = start + (chunk.end - localOff)
		if off <= start {
			off++
		} // safety
	}

	return nil
}

func (p *s3Piece) MarkNotComplete() error { return nil }
func (p *s3Piece) Completion() storage.Completion {
	pk := metainfo.PieceKey{InfoHash: p.t.infoHash, Index: p.piece.Index()}
	c, _ := p.t.pc.Get(pk)
	return c
}

// s3PieceCompletion implementation (same as before)
type s3PieceCompletion struct {
	s        *S3Storage
	info     *metainfo.Info
	infoHash metainfo.Hash
	mu       sync.Mutex
	cache    map[int]bool
}

func (pc *s3PieceCompletion) Get(pk metainfo.PieceKey) (storage.Completion, error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return storage.Completion{Complete: pc.cache[pk.Index]}, nil
}

func (pc *s3PieceCompletion) Set(pk metainfo.PieceKey, complete bool) error {
	pc.mu.Lock()
	pc.cache[pk.Index] = complete
	pc.mu.Unlock()
	return nil
}

func (pc *s3PieceCompletion) Close() error { return nil }

func (pc *s3PieceCompletion) uploadMarker(index int) {
	hash := hex.EncodeToString(pc.info.Pieces[index*20 : (index+1)*20])
	key := fmt.Sprintf("%s/state/%s", pc.infoHash.HexString(), hash)
	_, _ = pc.s.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(pc.s.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	})
}

func (pc *s3PieceCompletion) syncFromS3() {
	prefix := fmt.Sprintf("%s/state/", pc.infoHash.HexString())
	paginator := s3.NewListObjectsV2Paginator(pc.s.s3Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(pc.s.Bucket),
		Prefix: aws.String(prefix),
	})

	pieceMap := make(map[string]int)
	for i := 0; i < pc.info.NumPieces(); i++ {
		hash := hex.EncodeToString(pc.info.Pieces[i*20 : (i+1)*20])
		pieceMap[hash] = i
	}

	for paginator.HasMorePages() {
		page, _ := paginator.NextPage(context.TODO())
		for _, obj := range page.Contents {
			hash := strings.TrimPrefix(*obj.Key, prefix)
			if idx, ok := pieceMap[hash]; ok {
				pc.mu.Lock()
				pc.cache[idx] = true
				pc.mu.Unlock()
			}
		}
	}
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
