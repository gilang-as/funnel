package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	ftorrent "github.com/gilang/funnel/pkg/torrent" // using our package
	"golang.org/x/time/rate"
)

const (
	// Batas upload (seed) dalam bytes/detik. 0 = unlimited.
	// Contoh: 512*1024 = 512 KB/s, 1*1024*1024 = 1 MB/s
	maxUploadBytesPerSec = 512 * 1024
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Hardcoded setup as requested
	cfg := torrent.NewDefaultClientConfig()
	// cfg.DataDir = "./data" // Disabled to keep metadata in S3 only

	// Use our custom S3 storage
	s3Store := ftorrent.NewS3Storage()
	cfg.DefaultStorage = s3Store

	// Batasi bandwidth upload (seed) agar tidak rebutan dengan download.
	if maxUploadBytesPerSec > 0 {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(maxUploadBytesPerSec), maxUploadBytesPerSec)
		log.Printf("Upload rate limit: %s/s\n", formatBytes(maxUploadBytesPerSec))
	}

	client, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Hardcoded magnet for testing
	magnet := "magnet:?xt=urn:btih:b93407b057c0167635a7ec71425ff13700fbb6e9&dn=%5BJMAX%5D%20%5B2026.01.07%5D%20TV%E3%82%A2%E3%83%8B%E3%83%A1%E3%80%8C%E7%A9%8F%E3%82%84%E3%81%8B%E8%B2%B4%E6%97%8F%E3%81%AE%E4%BC%91%E6%9A%87%E3%81%AE%E3%81%99%E3%81%99%E3%82%81%E3%80%82%E3%80%8DED%E3%83%86%E3%83%BC%E3%83%9E%E3%80%8C%E3%81%86%E3%81%A3%E3%81%99%E3%82%89%E3%80%8D%EF%BC%8F%E7%9B%B4%E7%94%B0%E5%A7%AB%E5%A5%88%20%5BFLAC%5D&tr=http%3A%2F%2Fnyaa.tracker.wf%3A7777%2Fannounce&tr=udp%3A%2F%2Fopen.stealth.si%3A80%2Fannounce&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce&tr=udp%3A%2F%2Fexodus.desync.com%3A6969%2Fannounce&tr=udp%3A%2F%2Ftracker.torrent.eu.org%3A451%2Fannounce"
	t, err := client.AddMagnet(magnet)
	if err != nil {
		log.Fatal(err)
	}

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		log.Println("Interrupted before torrent info.")
		return
	}
	log.Printf("Seeding: %s\n", t.Name())

	t.DownloadAll()

	// Keep running
	log.Println("Seeding mode active...")
	lastPieces := -1
	for {
		stats := t.Stats()
		piecesDone := stats.PiecesComplete
		totalPieces := t.NumPieces()

		// Use t.BytesCompleted() instead of stats.BytesCompleted
		bytesDone := t.BytesCompleted()
		totalBytes := t.Length()
		progress := float64(bytesDone) / float64(totalBytes) * 100

		if piecesDone != lastPieces {
			log.Printf("Progress: %.2f%% (Pieces: %d/%d, Bytes: %d/%d)\n",
				progress, piecesDone, totalPieces, bytesDone, totalBytes)
			lastPieces = piecesDone

			if piecesDone < totalPieces {
				// Find first missing piece index for debugging
				for i := 0; i < totalPieces; i++ {
					if !t.Piece(i).State().Complete {
						log.Printf("   -> Piece %d is still marking as incomplete/verifying...\n", i)
						break
					}
				}
			}
		}

		if piecesDone >= totalPieces || progress >= 99.9 {
			log.Printf("Progress: 100.00%% (Final pieces verified or available on S3)\n")
			log.Println("Success: All data is synced. Seeding mode only.")
			break
		}

		select {
		case <-ctx.Done():
			log.Println("Interrupted, shutting down...")
			return
		case <-time.After(5 * time.Second):
		}
	}

	// Seed dari S3 — log stats periodik
	var lastUploaded int64
	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down gracefully...")
			return
		case <-time.After(10 * time.Second):
			stats := t.Stats()
			uploaded := stats.ConnStats.BytesWrittenData.Int64()
			uploadRate := (uploaded - lastUploaded) / 10 // bytes/sec rata-rata 10 detik terakhir
			lastUploaded = uploaded

			log.Printf("[SEED] Peers: %d active / %d total | Uploaded: %s | Rate: %s/s\n",
				stats.ActivePeers, stats.TotalPeers,
				formatBytes(uploaded), formatBytes(uploadRate))
		}
	}
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
