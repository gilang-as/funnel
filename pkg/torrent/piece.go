package torrent

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// s3PieceCompletion melacak piece mana yang sudah selesai,
// dengan cache in-memory yang disinkronkan dari S3 saat startup.
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

// uploadMarker menyimpan marker di S3 untuk menandai piece sudah selesai.
func (pc *s3PieceCompletion) uploadMarker(index int) {
	hash := hex.EncodeToString(pc.info.Pieces[index*20 : (index+1)*20])
	key := fmt.Sprintf("%s/state/%s", pc.infoHash.HexString(), hash)
	_, _ = pc.s.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(pc.s.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	})
}

// syncFromS3 memuat status completion piece dari S3 saat startup.
// Key multipart state ({infoHash}/state/multipart/...) diabaikan.
func (pc *s3PieceCompletion) syncFromS3() {
	prefix := fmt.Sprintf("%s/state/", pc.infoHash.HexString())
	multipartPrefix := fmt.Sprintf("%s/state/multipart/", pc.infoHash.HexString())

	pieceMap := make(map[string]int, pc.info.NumPieces())
	for i := 0; i < pc.info.NumPieces(); i++ {
		hash := hex.EncodeToString(pc.info.Pieces[i*20 : (i+1)*20])
		pieceMap[hash] = i
	}

	var continuationToken *string
	for {
		res, err := pc.s.client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
			Bucket:            aws.String(pc.s.Bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			break
		}

		for _, obj := range res.Contents {
			// Abaikan multipart state keys.
			if strings.HasPrefix(*obj.Key, multipartPrefix) {
				continue
			}
			hash := strings.TrimPrefix(*obj.Key, prefix)
			if idx, ok := pieceMap[hash]; ok {
				pc.mu.Lock()
				pc.cache[idx] = true
				pc.mu.Unlock()
			}
		}

		if !*res.IsTruncated || res.NextContinuationToken == nil {
			break
		}
		continuationToken = res.NextContinuationToken
	}
}
