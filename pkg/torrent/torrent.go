package torrent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3Torrent struct {
	s                *S3Storage
	info             *metainfo.Info
	infoHash         metainfo.Hash
	pc               *s3PieceCompletion
	files            map[string]*s3FileState
	orderedFilePaths []string
	fileOffsets      map[string]int64 // absolute offset tiap file di dalam torrent (precomputed)
	mu               sync.Mutex
}

func newS3Torrent(s *S3Storage, info *metainfo.Info, infoHash metainfo.Hash) *s3Torrent {
	t := &s3Torrent{
		s:           s,
		info:        info,
		infoHash:    infoHash,
		files:       make(map[string]*s3FileState),
		fileOffsets: make(map[string]int64),
	}

	pc := &s3PieceCompletion{
		s:        s,
		info:     info,
		infoHash: infoHash,
		cache:    make(map[int]bool),
	}
	t.pc = pc
	pc.syncFromS3()

	var offset int64
	if len(info.Files) == 0 {
		// Single-file torrent.
		t.orderedFilePaths = append(t.orderedFilePaths, info.Name)
		t.fileOffsets[info.Name] = 0
		t.initFile(info.Name, info.Length)
	} else {
		for _, f := range info.Files {
			path := filepath.Join(f.Path...)
			t.orderedFilePaths = append(t.orderedFilePaths, path)
			t.fileOffsets[path] = offset
			t.initFile(path, f.Length)
			offset += f.Length
		}
	}

	return t
}

func (t *s3Torrent) initFile(relPath string, length int64) {
	fmt.Printf("[INIT] File: %s (Size: %d)\n", relPath, length)
	chunkSize := t.s.ChunkSize
	if chunkSize < defaultChunkSize {
		chunkSize = defaultChunkSize
	}
	// S3 limit: 10.000 parts per file.
	if length > chunkSize*10000 {
		chunkSize = (length / 10000) + 1
	}

	numChunks := int((length + chunkSize - 1) / chunkSize)
	if length == 0 {
		numChunks = 0
	}

	state := &s3FileState{
		t:          t,
		relPath:    relPath,
		length:     length,
		chunkSize:  chunkSize,
		chunks:     make([]*s3Chunk, numChunks),
		localLimit: t.s.MaxLocalChunks,
	}

	for i := 0; i < numChunks; i++ {
		end := int64(i+1) * chunkSize
		if end > length {
			end = length
		}
		state.chunks[i] = &s3Chunk{
			index: i,
			start: int64(i) * chunkSize,
			end:   end,
			state: state,
		}
	}

	t.files[relPath] = state
}

func (t *s3Torrent) saveMetainfo() {
	data, _ := json.MarshalIndent(t.info, "", "  ")
	key := fmt.Sprintf("%s/metainfo.json", t.infoHash.HexString())
	_, _ = t.s.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(t.s.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(string(data)),
	})
}

// findFile mencari file dan offset lokal dari absolute offset dalam torrent.
// Kompleksitas O(n) — untuk torrent dengan banyak file, pertimbangkan binary search.
func (t *s3Torrent) findFile(absOff int64) (*s3FileState, int64, error) {
	for _, path := range t.orderedFilePaths {
		f := t.files[path]
		fileStart := t.fileOffsets[path]
		if absOff >= fileStart && absOff < fileStart+f.length {
			return f, absOff - fileStart, nil
		}
	}
	return nil, 0, fmt.Errorf("offset %d out of range", absOff)
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
		canRead := int(minInt64(int64(len(target)), chunk.end-localOff))
		readTarget := target[:canRead]

		// 1. Coba baca dari file lokal.
		if f, err := os.Open(chunk.localPath()); err == nil {
			n, readErr := f.ReadAt(readTarget, chunkOff)
			f.Close()
			if readErr == nil || (n > 0 && readErr == io.EOF) {
				nTotal += n
				continue
			}
		}

		// 2. Fallback ke S3 jika chunk sudah diupload.
		state.activeMu.Lock()
		uploaded := state.partETags[chunkIdx+1] != ""
		state.activeMu.Unlock()

		if uploaded {
			output, err := t.s.client.GetObject(context.TODO(), &s3.GetObjectInput{
				Bucket: aws.String(t.s.Bucket),
				Key:    aws.String(state.s3Key()),
				Range:  aws.String(fmt.Sprintf("bytes=%d-%d", localOff, localOff+int64(canRead)-1)),
			})
			if err == nil {
				n, readErr := io.ReadFull(output.Body, readTarget)
				output.Body.Close()
				if readErr == nil || readErr == io.ErrUnexpectedEOF {
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
		canWrite := int(minInt64(int64(len(target)), chunk.end-localOff))

		path := chunk.localPath()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nTotal, err
		}
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			return nTotal, err
		}
		n, writeErr := f.WriteAt(target[:canWrite], chunkOff)
		f.Close()
		if writeErr != nil {
			return nTotal + n, writeErr
		}

		state.activeMu.Lock()
		found := false
		for _, idx := range state.activeChunks {
			if idx == chunkIdx {
				found = true
				break
			}
		}
		if !found {
			state.activeChunks = append(state.activeChunks, chunkIdx)
		}
		needsGC := len(state.activeChunks) > state.localLimit
		state.activeMu.Unlock()

		nTotal += n
		if needsGC {
			state.gcLocalChunks()
		}
	}
	return nTotal, nil
}

// s3Piece mengimplementasikan storage.PieceImpl.
type s3Piece struct {
	t     *s3Torrent
	piece metainfo.Piece
}

func (p *s3Piece) ReadAt(b []byte, off int64) (int, error) {
	return p.t.readAbsolute(b, p.piece.Offset()+off)
}

func (p *s3Piece) WriteAt(b []byte, off int64) (int, error) {
	return p.t.writeAbsolute(b, p.piece.Offset()+off)
}

func (p *s3Piece) MarkComplete() error {
	pk := metainfo.PieceKey{InfoHash: p.t.infoHash, Index: p.piece.Index()}
	p.t.pc.Set(pk, true)

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

		// Maju ke awal chunk berikutnya (dalam koordinat absolut torrent).
		nextOff := p.t.fileOffsets[state.relPath] + chunk.end
		if nextOff <= off {
			break // safety: hindari infinite loop
		}
		off = nextOff
	}

	return nil
}

func (p *s3Piece) MarkNotComplete() error { return nil }

func (p *s3Piece) Completion() storage.Completion {
	pk := metainfo.PieceKey{InfoHash: p.t.infoHash, Index: p.piece.Index()}
	c, _ := p.t.pc.Get(pk)
	return c
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
