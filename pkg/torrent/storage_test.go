package torrent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ---------------------------------------------------------------------------
// Mock S3 client
// ---------------------------------------------------------------------------

type mockS3 struct {
	mu      sync.Mutex
	objects map[string][]byte

	uploadIDs map[string]string          // s3Key -> uploadID
	parts     map[string]map[int32][]byte // uploadID -> partNum -> data

	uploadCallCount atomic.Int32
	uploadFailUntil int32 // gagalkan N call pertama ke UploadPart
}

func newMockS3() *mockS3 {
	return &mockS3{
		objects:   make(map[string][]byte),
		uploadIDs: make(map[string]string),
		parts:     make(map[string]map[int32][]byte),
	}
}

func (m *mockS3) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, _ := io.ReadAll(params.Body)
	m.mu.Lock()
	m.objects[*params.Key] = data
	m.mu.Unlock()
	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.mu.Lock()
	data, ok := m.objects[*params.Key]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: %s", *params.Key)
	}
	size := int64(len(data))
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(strings.NewReader(string(data))),
		ContentLength: &size,
	}, nil
}

func (m *mockS3) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	m.mu.Lock()
	data, ok := m.objects[*params.Key]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: %s", *params.Key)
	}
	size := int64(len(data))
	return &s3.HeadObjectOutput{ContentLength: &size}, nil
}

func (m *mockS3) CreateMultipartUpload(_ context.Context, params *s3.CreateMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	uploadID := fmt.Sprintf("uid-%s", *params.Key)
	m.mu.Lock()
	m.uploadIDs[*params.Key] = uploadID
	m.parts[uploadID] = make(map[int32][]byte)
	m.mu.Unlock()
	return &s3.CreateMultipartUploadOutput{UploadId: &uploadID}, nil
}

func (m *mockS3) UploadPart(_ context.Context, params *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	n := m.uploadCallCount.Add(1)
	if failUntil := atomic.LoadInt32(&m.uploadFailUntil); n <= failUntil {
		return nil, fmt.Errorf("transient S3 error (call %d)", n)
	}
	data, _ := io.ReadAll(params.Body)
	m.mu.Lock()
	if m.parts[*params.UploadId] == nil {
		m.parts[*params.UploadId] = make(map[int32][]byte)
	}
	m.parts[*params.UploadId][*params.PartNumber] = data
	m.mu.Unlock()
	etag := fmt.Sprintf("etag-%d", *params.PartNumber)
	return &s3.UploadPartOutput{ETag: &etag}, nil
}

func (m *mockS3) CompleteMultipartUpload(_ context.Context, params *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var assembled []byte
	for _, part := range params.MultipartUpload.Parts {
		assembled = append(assembled, m.parts[*params.UploadId][*part.PartNumber]...)
	}
	m.objects[*params.Key] = assembled
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func (m *mockS3) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	m.mu.Lock()
	delete(m.objects, *params.Key)
	m.mu.Unlock()
	return &s3.DeleteObjectOutput{}, nil
}

func (m *mockS3) ListObjectsV2(_ context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var contents []types.Object
	for key := range m.objects {
		if strings.HasPrefix(key, *params.Prefix) {
			k := key
			contents = append(contents, types.Object{Key: &k})
		}
	}
	f := false
	return &s3.ListObjectsV2Output{Contents: contents, IsTruncated: &f}, nil
}

// ---------------------------------------------------------------------------
// slowMockS3: memblokir UploadPart sampai sinyal diterima (untuk test concurrency)
// ---------------------------------------------------------------------------

type slowMockS3 struct {
	s3API
	block    chan struct{}
	released chan struct{}
	once     sync.Once
}

func (m *slowMockS3) UploadPart(ctx context.Context, params *s3.UploadPartInput, opts ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	m.once.Do(func() { close(m.released) })
	<-m.block
	return m.s3API.UploadPart(ctx, params, opts...)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestStorage(t *testing.T, client s3API) *S3Storage {
	t.Helper()
	return &S3Storage{
		Bucket:         "test-bucket",
		BaseDir:        t.TempDir(),
		ChunkSize:      defaultChunkSize,
		MaxLocalChunks: maxLocalChunks,
		uploadSem:      make(chan struct{}, maxConcurrentUploads),
		client:         client,
	}
}

// makeInfo membuat metainfo.Info untuk testing.
// Jika satu file dengan Path kosong → single-file torrent.
// Selainnya → multi-file.
func makeInfo(name string, pieceLen int64, files ...metainfo.FileInfo) *metainfo.Info {
	info := &metainfo.Info{
		Name:        name,
		PieceLength: pieceLen,
	}
	if len(files) == 1 && len(files[0].Path) == 0 {
		info.Length = files[0].Length
	} else {
		info.Files = files
	}
	var total int64
	for _, f := range files {
		total += f.Length
	}
	numPieces := int((total + pieceLen - 1) / pieceLen)
	info.Pieces = make([]byte, numPieces*20)
	return info
}

func singleFile(length int64) metainfo.FileInfo {
	return metainfo.FileInfo{Length: length}
}

func multiFile(name string, length int64) metainfo.FileInfo {
	return metainfo.FileInfo{Path: []string{name}, Length: length}
}

// setupMultipart menyiapkan multipart upload state untuk semua file dalam torrent.
func setupMultipart(t *testing.T, tor *s3Torrent, mock *mockS3) {
	t.Helper()
	for _, path := range tor.orderedFilePaths {
		state := tor.files[path]
		res, err := mock.CreateMultipartUpload(context.Background(), &s3.CreateMultipartUploadInput{
			Bucket: aws.String(tor.s.Bucket),
			Key:    aws.String(state.s3Key()),
		})
		if err != nil {
			t.Fatal(err)
		}
		state.activeMu.Lock()
		state.uploadID = *res.UploadId
		state.partETags = make(map[int]string)
		state.activeMu.Unlock()
	}
}

// writeChunkToDisk menulis data dummy ke file lokal chunk.
func writeChunkToDisk(t *testing.T, chunk *s3Chunk) {
	t.Helper()
	p := chunk.localPath()
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, make([]byte, chunk.end-chunk.start), 0644); err != nil {
		t.Fatal(err)
	}
}

// markPiecesComplete menandai semua piece yang cover chunk sebagai complete.
func markPiecesComplete(tor *s3Torrent, chunk *s3Chunk) {
	fileStart := tor.fileOffsets[chunk.state.relPath]
	pieceLen := tor.info.PieceLength
	first := int((fileStart + chunk.start) / pieceLen)
	last := int((fileStart + chunk.end - 1) / pieceLen)
	for i := first; i <= last; i++ {
		tor.pc.Set(metainfo.PieceKey{InfoHash: tor.infoHash, Index: i}, true)
	}
}

// ---------------------------------------------------------------------------
// Tests: findFile & fileOffsets
// ---------------------------------------------------------------------------

func TestFindFile_MultiFile(t *testing.T) {
	s := newTestStorage(t, newMockS3())
	info := makeInfo("test", 256*1024,
		multiFile("a.txt", 1000),
		multiFile("b.txt", 2000),
		multiFile("c.txt", 500),
	)
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	tests := []struct {
		absOff       int64
		wantFile     string
		wantLocalOff int64
		wantErr      bool
	}{
		{0, "a.txt", 0, false},
		{999, "a.txt", 999, false},
		{1000, "b.txt", 0, false},
		{2999, "b.txt", 1999, false},
		{3000, "c.txt", 0, false},
		{3499, "c.txt", 499, false},
		{3500, "", 0, true}, // out of range
	}

	for _, tc := range tests {
		f, localOff, err := tor.findFile(tc.absOff)
		if tc.wantErr {
			if err == nil {
				t.Errorf("findFile(%d): expected error, got nil", tc.absOff)
			}
			continue
		}
		if err != nil {
			t.Errorf("findFile(%d): unexpected error: %v", tc.absOff, err)
			continue
		}
		if f.relPath != tc.wantFile {
			t.Errorf("findFile(%d): got file %q, want %q", tc.absOff, f.relPath, tc.wantFile)
		}
		if localOff != tc.wantLocalOff {
			t.Errorf("findFile(%d): got localOff %d, want %d", tc.absOff, localOff, tc.wantLocalOff)
		}
	}
}

func TestFileOffsets_Precomputed(t *testing.T) {
	s := newTestStorage(t, newMockS3())
	info := makeInfo("test", 512*1024,
		multiFile("x.flac", 5*1024*1024),
		multiFile("y.flac", 3*1024*1024),
	)
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	if got := tor.fileOffsets["x.flac"]; got != 0 {
		t.Errorf("x.flac offset: got %d, want 0", got)
	}
	if got := tor.fileOffsets["y.flac"]; got != 5*1024*1024 {
		t.Errorf("y.flac offset: got %d, want %d", got, 5*1024*1024)
	}
}

// ---------------------------------------------------------------------------
// Tests: removeFileAndEmptyParents
// ---------------------------------------------------------------------------

func TestRemoveFileAndEmptyParents_CleansUpEmptyDirs(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b", "c")
	os.MkdirAll(dir, 0755)
	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("data"), 0644)

	removeFileAndEmptyParents(filePath, base)

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file seharusnya sudah terhapus")
	}
	for _, d := range []string{
		filepath.Join(base, "a", "b", "c"),
		filepath.Join(base, "a", "b"),
		filepath.Join(base, "a"),
	} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("direktori kosong %s seharusnya sudah terhapus", d)
		}
	}
	if _, err := os.Stat(base); err != nil {
		t.Errorf("baseDir seharusnya masih ada: %v", err)
	}
}

func TestRemoveFileAndEmptyParents_KeepsNonEmptyDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "shared")
	os.MkdirAll(dir, 0755)

	f1 := filepath.Join(dir, "chunk.0")
	f2 := filepath.Join(dir, "chunk.1")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	removeFileAndEmptyParents(f1, base)

	if _, err := os.Stat(f1); !os.IsNotExist(err) {
		t.Error("f1 seharusnya sudah terhapus")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("direktori shared seharusnya masih ada: %v", err)
	}
	if _, err := os.Stat(f2); err != nil {
		t.Errorf("f2 seharusnya masih ada: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: gcLocalChunks
// ---------------------------------------------------------------------------

func TestGCLocalChunks_NoOpWhenBelowLimit(t *testing.T) {
	s := newTestStorage(t, newMockS3())
	s.MaxLocalChunks = 3
	info := makeInfo("test", 512*1024, singleFile(50*1024*1024))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	state.activeMu.Lock()
	state.activeChunks = []int{0, 1} // 2 < limit 3
	state.activeMu.Unlock()

	state.gcLocalChunks()

	state.activeMu.Lock()
	got := len(state.activeChunks)
	state.activeMu.Unlock()

	if got != 2 {
		t.Errorf("activeChunks seharusnya tetap 2, got %d", got)
	}
}

func TestGCLocalChunks_RemovesUploadedChunk(t *testing.T) {
	s := newTestStorage(t, newMockS3())
	s.MaxLocalChunks = 1
	info := makeInfo("test", 512*1024, singleFile(50*1024*1024))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	state.activeMu.Lock()
	state.partETags = make(map[int]string)
	state.activeMu.Unlock()

	// Buat file lokal untuk chunk 0 (uploaded) dan chunk 1 (belum).
	chunk0Path := state.chunks[0].localPath()
	chunk1Path := state.chunks[1].localPath()
	os.MkdirAll(filepath.Dir(chunk0Path), 0755)
	os.WriteFile(chunk0Path, []byte("data0"), 0644)
	os.MkdirAll(filepath.Dir(chunk1Path), 0755)
	os.WriteFile(chunk1Path, []byte("data1"), 0644)

	state.activeMu.Lock()
	state.activeChunks = []int{0, 1}
	state.partETags[1] = "uploaded-etag" // chunk index 0 → part number 1
	state.activeMu.Unlock()

	state.gcLocalChunks()

	if _, err := os.Stat(chunk0Path); !os.IsNotExist(err) {
		t.Error("chunk0 yang sudah diupload seharusnya terhapus dari disk")
	}
	if _, err := os.Stat(chunk1Path); err != nil {
		t.Errorf("chunk1 yang belum diupload seharusnya masih ada: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: syncFromS3 (piece completion)
// ---------------------------------------------------------------------------

func TestSyncFromS3_SkipsMultipartKeys(t *testing.T) {
	mock := newMockS3()
	// Satu piece saja agar tidak ada hash collision (info.Pieces semua zero).
	info := makeInfo("test", 512*1024, singleFile(400*1024))
	var infoHash metainfo.Hash

	// Piece marker yang valid.
	pieceHash := fmt.Sprintf("%x", info.Pieces[0:20])
	validKey := fmt.Sprintf("%s/state/%s", infoHash.HexString(), pieceHash)
	mock.objects[validKey] = []byte{}

	// Multipart state key — harus diabaikan.
	multipartKey := fmt.Sprintf("%s/state/multipart/somefile.json", infoHash.HexString())
	mock.objects[multipartKey] = []byte(`{"UploadID":"uid","PartETags":{}}`)

	s := newTestStorage(t, mock)
	pc := &s3PieceCompletion{
		s:        s,
		info:     info,
		infoHash: infoHash,
		cache:    make(map[int]bool),
	}
	pc.syncFromS3()

	comp, _ := pc.Get(metainfo.PieceKey{InfoHash: infoHash, Index: 0})
	if !comp.Complete {
		t.Error("piece 0 seharusnya complete berdasarkan marker di S3")
	}

	pc.mu.Lock()
	count := len(pc.cache)
	pc.mu.Unlock()
	if count != 1 {
		t.Errorf("hanya 1 piece yang boleh complete (bukan multipart key), got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Tests: upload retry
// ---------------------------------------------------------------------------

func TestDoUpload_RetriesOnTransientFailure(t *testing.T) {
	// Percepat retry untuk test.
	orig := retryDelay
	retryDelay = func(_ int) time.Duration { return time.Millisecond }
	defer func() { retryDelay = orig }()

	mock := newMockS3()
	// Gagalkan 2 call pertama; berhasil di call ke-3.
	atomic.StoreInt32(&mock.uploadFailUntil, 2)

	s := newTestStorage(t, mock)
	info := makeInfo("test", 512*1024, singleFile(10*1024*1024))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	setupMultipart(t, tor, mock)

	chunk := state.chunks[0]
	writeChunkToDisk(t, chunk)
	markPiecesComplete(tor, chunk)

	state.activeMu.Lock()
	chunk.uploading = true
	state.activeMu.Unlock()

	done := make(chan struct{})
	go func() {
		chunk.doUpload()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("doUpload timeout")
	}

	state.activeMu.Lock()
	etag := state.partETags[chunk.index+1]
	state.activeMu.Unlock()

	if etag == "" {
		t.Error("chunk seharusnya berhasil diupload setelah retry")
	}
	if n := mock.uploadCallCount.Load(); n < 3 {
		t.Errorf("UploadPart seharusnya dipanggil minimal 3x, got %d", n)
	}
}

func TestDoUpload_FailsAfterMaxRetries(t *testing.T) {
	orig := retryDelay
	retryDelay = func(_ int) time.Duration { return time.Millisecond }
	defer func() { retryDelay = orig }()

	mock := newMockS3()
	// Selalu gagal.
	atomic.StoreInt32(&mock.uploadFailUntil, 999)

	s := newTestStorage(t, mock)
	info := makeInfo("test", 512*1024, singleFile(10*1024*1024))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	setupMultipart(t, tor, mock)

	chunk := state.chunks[0]
	writeChunkToDisk(t, chunk)
	markPiecesComplete(tor, chunk)

	state.activeMu.Lock()
	chunk.uploading = true
	state.activeMu.Unlock()

	done := make(chan struct{})
	go func() {
		chunk.doUpload()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("doUpload timeout")
	}

	state.activeMu.Lock()
	etag := state.partETags[chunk.index+1]
	state.activeMu.Unlock()

	if etag != "" {
		t.Error("chunk seharusnya tidak berhasil diupload")
	}
	if n := mock.uploadCallCount.Load(); int(n) != maxUploadRetries {
		t.Errorf("UploadPart seharusnya dipanggil tepat %d kali, got %d", maxUploadRetries, n)
	}
}

// ---------------------------------------------------------------------------
// Tests: mutex tidak dipegang selama S3 call
// ---------------------------------------------------------------------------

func TestDoUpload_MutexReleasedDuringS3Call(t *testing.T) {
	block := make(chan struct{})
	released := make(chan struct{})

	slow := &slowMockS3{
		s3API:    newMockS3(),
		block:    block,
		released: released,
	}

	s := newTestStorage(t, slow)
	info := makeInfo("test", 512*1024, singleFile(10*1024*1024))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	setupMultipart(t, tor, slow.s3API.(*mockS3))

	chunk := state.chunks[0]
	writeChunkToDisk(t, chunk)
	markPiecesComplete(tor, chunk)

	state.activeMu.Lock()
	chunk.uploading = true
	state.activeMu.Unlock()

	go chunk.doUpload()

	// Tunggu S3 call dimulai.
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("S3 call tidak dimulai")
	}

	// Mutex harus bisa diakses selama S3 call berlangsung.
	lockAcquired := make(chan bool, 1)
	go func() {
		state.activeMu.Lock()
		state.activeMu.Unlock()
		lockAcquired <- true
	}()

	select {
	case <-lockAcquired:
		// OK: mutex tidak dipegang selama S3 call
	case <-time.After(1 * time.Second):
		t.Error("deadlock: mutex dipegang selama S3 call berlangsung")
	}

	close(block) // biarkan upload selesai
}

// ---------------------------------------------------------------------------
// Tests: MarkComplete offset calculation
// ---------------------------------------------------------------------------

func TestMarkComplete_SpanningTwoFiles(t *testing.T) {
	s := newTestStorage(t, newMockS3())

	pieceLen := int64(512 * 1024) // 512KB
	// File A: 400KB, File B: 800KB — piece 0 spans keduanya.
	info := makeInfo("test", pieceLen,
		multiFile("a.flac", 400*1024),
		multiFile("b.flac", 800*1024),
	)
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	setupMultipart(t, tor, s.client.(*mockS3))

	// Buat file lokal untuk semua chunk.
	for _, path := range tor.orderedFilePaths {
		for _, chunk := range tor.files[path].chunks {
			writeChunkToDisk(t, chunk)
		}
	}

	// Mark semua pieces complete.
	for i := 0; i < info.NumPieces(); i++ {
		tor.pc.Set(metainfo.PieceKey{InfoHash: infoHash, Index: i}, true)
	}

	// MarkComplete pada piece 0 harus selesai tanpa infinite loop.
	piece := &s3Piece{t: tor, piece: info.Piece(0)}
	done := make(chan struct{})
	go func() {
		piece.MarkComplete()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("MarkComplete infinite loop atau terlalu lambat")
	}
}

// ---------------------------------------------------------------------------
// Tests: OpenTorrent — allDone detection
// ---------------------------------------------------------------------------

func TestOpenTorrent_AllDoneWhenFilesOnS3(t *testing.T) {
	mock := newMockS3()
	s := newTestStorage(t, mock)

	pieceLen := int64(256 * 1024)
	fileSize := int64(512 * 1024)
	info := makeInfo("complete.flac", pieceLen, singleFile(fileSize))
	var infoHash metainfo.Hash

	// Simulasikan file sudah ada di S3 dengan ukuran yang tepat.
	tor := newS3Torrent(s, info, infoHash)
	state := tor.files[info.Name]
	mock.mu.Lock()
	mock.objects[state.s3Key()] = make([]byte, fileSize)
	mock.mu.Unlock()

	// OpenTorrent harus mendeteksi file sudah ada dan mark semua pieces complete.
	impl, err := s.OpenTorrent(context.Background(), info, infoHash)
	if err != nil {
		t.Fatalf("OpenTorrent: %v", err)
	}
	defer impl.Close()

	// Semua pieces harus complete.
	for i := 0; i < info.NumPieces(); i++ {
		piece := impl.Piece(info.Piece(i))
		if !piece.Completion().Complete {
			t.Errorf("piece %d seharusnya complete setelah allDone detection", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: readAbsolute — S3 fallback (seeding path)
// ---------------------------------------------------------------------------

func TestReadAbsolute_FallbackToS3(t *testing.T) {
	mock := newMockS3()
	s := newTestStorage(t, mock)

	pieceLen := int64(256 * 1024) // 256KB
	fileSize := int64(512 * 1024) // 512KB = 1 chunk
	info := makeInfo("seed.flac", pieceLen, singleFile(fileSize))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	setupMultipart(t, tor, mock)

	// Siapkan data dan tulis ke S3 secara langsung (simulasi file sudah diupload).
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 251)
	}
	mock.mu.Lock()
	mock.objects[state.s3Key()] = data
	mock.mu.Unlock()

	// Tandai semua piece complete dan partETags terisi (chunk sudah diupload).
	for i := 0; i < info.NumPieces(); i++ {
		tor.pc.Set(metainfo.PieceKey{InfoHash: infoHash, Index: i}, true)
	}
	state.activeMu.Lock()
	state.partETags[1] = "etag-1" // chunk 0 = part 1
	state.activeMu.Unlock()

	// Tidak ada file lokal — harus fallback ke S3.
	buf := make([]byte, fileSize)
	n, err := tor.readAbsolute(buf, 0)
	if err != nil {
		t.Fatalf("readAbsolute: %v", err)
	}
	if n != int(fileSize) {
		t.Fatalf("readAbsolute: got %d bytes, want %d", n, fileSize)
	}
	if string(buf) != string(data) {
		t.Error("data dari S3 tidak cocok dengan data asli")
	}
}

// ---------------------------------------------------------------------------
// Tests: recovery uploadID kosong
// ---------------------------------------------------------------------------

func TestDoUpload_RecoversMissingUploadID(t *testing.T) {
	orig := retryDelay
	retryDelay = func(_ int) time.Duration { return time.Millisecond }
	defer func() { retryDelay = orig }()

	mock := newMockS3()
	s := newTestStorage(t, mock)
	info := makeInfo("test", 512*1024, singleFile(10*1024*1024))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	// Simulasikan: uploadID kosong karena CreateMultipartUpload gagal saat startup.
	state.activeMu.Lock()
	state.uploadID = ""
	state.partETags = make(map[int]string)
	state.activeMu.Unlock()

	chunk := state.chunks[0]
	writeChunkToDisk(t, chunk)
	markPiecesComplete(tor, chunk)

	state.activeMu.Lock()
	chunk.uploading = true
	state.activeMu.Unlock()

	done := make(chan struct{})
	go func() {
		chunk.doUpload()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("doUpload timeout")
	}

	state.activeMu.Lock()
	etag := state.partETags[chunk.index+1]
	uploadID := state.uploadID
	state.activeMu.Unlock()

	if etag == "" {
		t.Error("chunk seharusnya berhasil diupload setelah recovery")
	}
	if uploadID == "" {
		t.Error("uploadID seharusnya terisi setelah recovery CreateMultipartUpload")
	}
}

// ---------------------------------------------------------------------------
// Tests: stale state JSON dihapus setelah finalize
// ---------------------------------------------------------------------------

func TestFinalizeMultipart_DeletesStateKey(t *testing.T) {
	orig := retryDelay
	retryDelay = func(_ int) time.Duration { return time.Millisecond }
	defer func() { retryDelay = orig }()

	mock := newMockS3()
	s := newTestStorage(t, mock)
	s.MaxLocalChunks = 10

	info := makeInfo("test", 256*1024, singleFile(10*1024*1024))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	setupMultipart(t, tor, mock)

	// Taruh state JSON palsu di mock untuk memastikan dihapus setelah upload selesai.
	mock.mu.Lock()
	mock.objects[state.stateKey()] = []byte(`{"UploadID":"uid","PartETags":{}}`)
	mock.mu.Unlock()

	// Upload semua chunk.
	data := make([]byte, 10*1024*1024)
	if _, err := tor.writeAbsolute(data, 0); err != nil {
		t.Fatalf("writeAbsolute: %v", err)
	}
	for i := 0; i < info.NumPieces(); i++ {
		piece := &s3Piece{t: tor, piece: info.Piece(i)}
		piece.MarkComplete()
	}

	// Tunggu semua chunk selesai.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state.activeMu.Lock()
		done := len(state.partETags) == len(state.chunks)
		state.activeMu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// State JSON harus sudah terhapus.
	mock.mu.Lock()
	_, exists := mock.objects[state.stateKey()]
	mock.mu.Unlock()
	if exists {
		t.Error("state JSON seharusnya sudah terhapus setelah multipart selesai")
	}
}

// ---------------------------------------------------------------------------
// Tests: integrasi write → upload → S3
// ---------------------------------------------------------------------------

func TestWriteAndUpload_SingleFile(t *testing.T) {
	orig := retryDelay
	retryDelay = func(_ int) time.Duration { return time.Millisecond }
	defer func() { retryDelay = orig }()

	mock := newMockS3()
	s := newTestStorage(t, mock)
	s.MaxLocalChunks = 10 // jangan GC selama test

	pieceLen := int64(256 * 1024) // 256KB
	fileSize := int64(1024 * 1024) // 1MB
	info := makeInfo("myfile", pieceLen, singleFile(fileSize))
	var infoHash metainfo.Hash
	tor := newS3Torrent(s, info, infoHash)

	state := tor.files[info.Name]
	setupMultipart(t, tor, mock)

	// Tulis data ke semua pieces.
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if _, err := tor.writeAbsolute(data, 0); err != nil {
		t.Fatalf("writeAbsolute: %v", err)
	}

	// Mark semua pieces complete → trigger upload.
	for i := 0; i < info.NumPieces(); i++ {
		piece := &s3Piece{t: tor, piece: info.Piece(i)}
		piece.MarkComplete()
	}

	// Tunggu semua chunk selesai diupload.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state.activeMu.Lock()
		done := len(state.partETags) == len(state.chunks)
		state.activeMu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	state.activeMu.Lock()
	uploadedChunks := len(state.partETags)
	state.activeMu.Unlock()

	if uploadedChunks != len(state.chunks) {
		t.Errorf("chunk terupload: got %d, want %d", uploadedChunks, len(state.chunks))
	}
}
