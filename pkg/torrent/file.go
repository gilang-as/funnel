package torrent

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// retryDelay menentukan jeda antar retry upload. Bisa di-override di tests.
var retryDelay = func(attempt int) time.Duration {
	return time.Duration(attempt+1) * 2 * time.Second
}

// s3FileState mengelola satu file: multipart upload ke S3 dan chunk lokal.
type s3FileState struct {
	t          *s3Torrent
	relPath    string
	length     int64
	chunkSize  int64
	chunks     []*s3Chunk
	uploadID   string
	partETags  map[int]string
	localLimit int

	activeMu     sync.Mutex
	activeChunks []int // indeks chunk yang ada di disk lokal
}

func (f *s3FileState) s3Key() string {
	if len(f.t.info.Files) == 0 {
		return fmt.Sprintf("%s/files/%s", f.t.infoHash.HexString(), f.t.info.Name)
	}
	return fmt.Sprintf("%s/files/%s/%s", f.t.infoHash.HexString(), f.t.info.Name, f.relPath)
}

func (f *s3FileState) stateKey() string {
	return fmt.Sprintf("%s/state/multipart/%s.json",
		f.t.infoHash.HexString(), hex.EncodeToString([]byte(f.relPath)))
}

// loadMultipartState memuat state upload dari S3. S3 calls dilakukan di luar mutex.
func (f *s3FileState) loadMultipartState() {
	// Phase 1: baca semua data dari S3 tanpa lock.
	var savedUploadID string
	var savedETags map[int]string

	rctx, rcancel := s3ReadCtx()
	defer rcancel()
	output, err := f.t.s.client.GetObject(rctx, &s3.GetObjectInput{
		Bucket: aws.String(f.t.s.Bucket),
		Key:    aws.String(f.stateKey()),
	})
	if err == nil {
		var data struct {
			UploadID  string
			PartETags map[int]string
		}
		if json.NewDecoder(output.Body).Decode(&data) == nil {
			savedUploadID = data.UploadID
			savedETags = data.PartETags
			log.Printf("Resuming multipart upload for %s: %s\n", f.relPath, savedUploadID)
		}
		output.Body.Close()
	}

	// Periksa apakah file sudah ada di S3 (source of truth).
	alreadyOnS3 := false
	if f.length > 0 {
		hctx, hcancel := s3ReadCtx()
		res, err := f.t.s.client.HeadObject(hctx, &s3.HeadObjectInput{
			Bucket: aws.String(f.t.s.Bucket),
			Key:    aws.String(f.s3Key()),
		})
		hcancel()
		if err == nil && *res.ContentLength == f.length {
			log.Printf("File %s already on S3, skipping.\n", f.relPath)
			alreadyOnS3 = true
		}
	}

	// Buat multipart upload baru jika perlu.
	var newUploadID string
	if !alreadyOnS3 && f.length > 0 && savedUploadID == "" {
		wctx, wcancel := s3WriteCtx()
		res, err := f.t.s.client.CreateMultipartUpload(wctx, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(f.t.s.Bucket),
			Key:    aws.String(f.s3Key()),
		})
		wcancel()
		if err == nil {
			newUploadID = *res.UploadId
		}
	}

	// Phase 2: update state di bawah lock.
	f.activeMu.Lock()
	f.partETags = make(map[int]string)
	if savedETags != nil {
		f.uploadID = savedUploadID
		f.partETags = savedETags
	}
	if alreadyOnS3 {
		for i := range f.chunks {
			f.partETags[i+1] = "ALREADY_ON_S3"
		}
		f.markAllPiecesComplete()
	}
	if newUploadID != "" {
		f.uploadID = newUploadID
	}
	f.activeMu.Unlock()

	// Phase 3: simpan state baru jika upload ID baru dibuat.
	if newUploadID != "" {
		f.saveState()
	}

	// Upload file kosong ke S3.
	if f.length == 0 {
		wctx, wcancel := s3WriteCtx()
		_, err := f.t.s.client.PutObject(wctx, &s3.PutObjectInput{
			Bucket: aws.String(f.t.s.Bucket),
			Key:    aws.String(f.s3Key()),
			Body:   strings.NewReader(""),
		})
		wcancel()
		if err == nil {
			log.Printf("[UPLOADED] %s | Empty file\n", f.relPath)
		}
	}
}

func (f *s3FileState) markAllPiecesComplete() {
	fileStart := f.t.fileOffsets[f.relPath]
	pieceLen := f.t.info.PieceLength
	first := int(fileStart / pieceLen)
	last := int((fileStart + f.length - 1) / pieceLen)
	for i := first; i <= last; i++ {
		f.t.pc.Set(metainfo.PieceKey{InfoHash: f.t.infoHash, Index: i}, true)
	}
}

// saveState menyimpan multipart state ke S3. Tidak boleh dipanggil sambil memegang activeMu.
func (f *s3FileState) saveState() {
	f.activeMu.Lock()
	uploadID := f.uploadID
	partETags := make(map[int]string, len(f.partETags))
	for k, v := range f.partETags {
		partETags[k] = v
	}
	f.activeMu.Unlock()

	f.saveStateWith(uploadID, partETags)
}

func (f *s3FileState) saveStateWith(uploadID string, partETags map[int]string) {
	data := struct {
		UploadID  string
		PartETags map[int]string
	}{uploadID, partETags}
	b, _ := json.Marshal(data)
	ctx, cancel := s3WriteCtx()
	defer cancel()
	_, _ = f.t.s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(f.t.s.Bucket),
		Key:    aws.String(f.stateKey()),
		Body:   strings.NewReader(string(b)),
	})
}

// gcLocalChunks menghapus chunk lokal yang sudah diupload hingga di bawah localLimit.
// O(n) single pass: kumpulkan kandidat hapus, update state di bawah lock, lalu hapus file di luar lock.
func (f *s3FileState) gcLocalChunks() {
	f.activeMu.Lock()

	excess := len(f.activeChunks) - f.localLimit
	if excess <= 0 {
		f.activeMu.Unlock()
		return
	}

	var toRemove []int
	var newActive []int
	for _, idx := range f.activeChunks {
		if excess > 0 && f.partETags[idx+1] != "" {
			toRemove = append(toRemove, idx)
			excess--
		} else {
			newActive = append(newActive, idx)
		}
	}
	f.activeChunks = newActive
	f.activeMu.Unlock()

	for _, idx := range toRemove {
		removeFileAndEmptyParents(f.chunks[idx].localPath(), f.t.s.BaseDir)
		log.Printf("GC: Local chunk %d of %s removed.\n", idx, f.relPath)
	}
}

// finalizeMultipart menyelesaikan multipart upload di S3.
// Dipanggil di luar mutex.
func (f *s3FileState) finalizeMultipart(uploadID string, partETags map[int]string) {
	log.Printf("File %s fully uploaded! Finalizing multipart...\n", f.relPath)
	var parts []types.CompletedPart
	for num, etag := range partETags {
		parts = append(parts, types.CompletedPart{
			PartNumber: aws.Int32(int32(num)),
			ETag:       aws.String(etag),
		})
	}
	sort.Slice(parts, func(i, j int) bool {
		return *parts[i].PartNumber < *parts[j].PartNumber
	})
	ctx, cancel := s3WriteCtx()
	defer cancel()
	_, err := f.t.s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(f.t.s.Bucket),
		Key:             aws.String(f.s3Key()),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err == nil {
		log.Printf("File %s completed in S3.\n", f.relPath)
		// Hapus state multipart — tidak lagi dibutuhkan.
		if _, derr := f.t.s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(f.t.s.Bucket),
			Key:    aws.String(f.stateKey()),
		}); derr != nil {
			log.Printf("[WARN] cleanup state %s: %v\n", f.relPath, derr)
		}
	} else {
		log.Printf("[FAILED] Finalizing %s: %v\n", f.relPath, err)
	}
}

// s3Chunk adalah segmen file yang disimpan sementara di disk lalu diupload ke S3.
type s3Chunk struct {
	index     int
	start     int64 // offset dari awal file
	end       int64 // offset dari awal file (eksklusif)
	state     *s3FileState
	uploading bool
}

func (c *s3Chunk) localPath() string {
	return filepath.Join(
		c.state.t.s.BaseDir,
		c.state.t.infoHash.HexString(),
		"chunks",
		hex.EncodeToString([]byte(c.state.relPath)),
		fmt.Sprintf("chunk.%d", c.index),
	)
}

func (c *s3Chunk) isComplete() bool {
	fileStart := c.state.t.fileOffsets[c.state.relPath]
	pieceLen := c.state.t.info.PieceLength
	first := int((fileStart + c.start) / pieceLen)
	last := int((fileStart + c.end - 1) / pieceLen)
	for i := first; i <= last; i++ {
		comp, _ := c.state.t.pc.Get(metainfo.PieceKey{InfoHash: c.state.t.infoHash, Index: i})
		if !comp.Complete {
			return false
		}
	}
	return true
}

func (c *s3Chunk) uploadIfComplete() {
	c.state.activeMu.Lock()
	if c.uploading || c.state.partETags[c.index+1] != "" || !c.isComplete() {
		c.state.activeMu.Unlock()
		return
	}
	c.uploading = true
	c.state.activeMu.Unlock()
	go c.doUpload()
}

func (c *s3Chunk) doUpload() {
	// Batasi concurrent uploads ke S3.
	c.state.t.s.uploadSem <- struct{}{}
	defer func() { <-c.state.t.s.uploadSem }()

	path := c.localPath()

	// Capture uploadID under lock before starting uploads to avoid reading it without lock.
	c.state.activeMu.Lock()
	uploadID := c.state.uploadID
	c.state.activeMu.Unlock()

	// Jika uploadID kosong (CreateMultipartUpload gagal saat startup), coba buat baru.
	if uploadID == "" {
		wctx, wcancel := s3WriteCtx()
		res, cerr := c.state.t.s.client.CreateMultipartUpload(wctx, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(c.state.t.s.Bucket),
			Key:    aws.String(c.state.s3Key()),
		})
		wcancel()
		if cerr != nil {
			log.Printf("[FAILED] %s | CreateMultipartUpload: %v\n", c.state.relPath, cerr)
			c.state.activeMu.Lock()
			c.uploading = false
			c.state.activeMu.Unlock()
			return
		}
		uploadID = *res.UploadId
		c.state.activeMu.Lock()
		c.state.uploadID = uploadID
		c.state.activeMu.Unlock()
		c.state.saveState()
	}

	// Upload dengan retry.
	var (
		etag string
		err  error
	)
	for attempt := 0; attempt < maxUploadRetries; attempt++ {
		etag, err = c.uploadOnce(path, uploadID)
		if err == nil {
			break
		}
		log.Printf("[RETRY %d/%d] %s | Chunk %d: %v\n",
			attempt+1, maxUploadRetries, c.state.relPath, c.index+1, err)
		if attempt < maxUploadRetries-1 {
			time.Sleep(retryDelay(attempt))
		}
	}

	// Update state di bawah lock, lalu lakukan S3 calls di luar lock.
	c.state.activeMu.Lock()
	c.uploading = false
	if err != nil {
		c.state.activeMu.Unlock()
		log.Printf("[FAILED] %s | Chunk %d/%d: %v\n", c.state.relPath, c.index+1, len(c.state.chunks), err)
		return
	}

	c.state.partETags[c.index+1] = etag
	for i, idx := range c.state.activeChunks {
		if idx == c.index {
			c.state.activeChunks = append(c.state.activeChunks[:i], c.state.activeChunks[i+1:]...)
			break
		}
	}
	shouldFinalize := len(c.state.partETags) == len(c.state.chunks)
	// Salin data yang dibutuhkan untuk S3 calls di luar lock.
	partETagsCopy := make(map[int]string, len(c.state.partETags))
	for k, v := range c.state.partETags {
		partETagsCopy[k] = v
	}
	c.state.activeMu.Unlock()

	// S3 calls di luar lock — tidak memblokir operasi file lain.
	log.Printf("[UPLOADED] %s | Chunk %d/%d\n", c.state.relPath, c.index+1, len(c.state.chunks))
	c.state.saveStateWith(uploadID, partETagsCopy)
	go c.uploadPieceMarkers()
	if shouldFinalize {
		c.state.finalizeMultipart(uploadID, partETagsCopy)
	}
	removeFileAndEmptyParents(path, c.state.t.s.BaseDir)

	c.state.gcLocalChunks()
}

func (c *s3Chunk) uploadOnce(path, uploadID string) (etag string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	ctx, cancel := s3WriteCtx()
	defer cancel()
	res, err := c.state.t.s.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(c.state.t.s.Bucket),
		Key:        aws.String(c.state.s3Key()),
		PartNumber: aws.Int32(int32(c.index + 1)),
		UploadId:   aws.String(uploadID),
		Body:       f,
	})
	if err != nil {
		return "", err
	}
	return *res.ETag, nil
}

func (c *s3Chunk) uploadPieceMarkers() {
	fileStart := c.state.t.fileOffsets[c.state.relPath]
	pieceLen := c.state.t.info.PieceLength
	first := int((fileStart + c.start) / pieceLen)
	last := int((fileStart + c.end - 1) / pieceLen)
	for i := first; i <= last; i++ {
		c.state.t.pc.uploadMarker(i)
	}
}

// removeFileAndEmptyParents menghapus file dan direktori parent yang kosong
// sampai baseDir.
func removeFileAndEmptyParents(path, baseDir string) {
	os.Remove(path)

	absBase, _ := filepath.Abs(baseDir)
	curr := filepath.Dir(path)

	for {
		absCurr, _ := filepath.Abs(curr)
		if absCurr == absBase || curr == "." || curr == "/" {
			break
		}
		if err := os.Remove(curr); err != nil {
			break // direktori tidak kosong atau tidak ada
		}
		log.Printf("GC: Removed empty directory: %s\n", curr)
		curr = filepath.Dir(curr)
	}
}
