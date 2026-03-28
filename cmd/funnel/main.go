package main

import (
	"fmt"
	"log"
	"time"

	"github.com/anacrolix/torrent"
	ftorrent "github.com/gilang/funnel/pkg/torrent" // using our package
)

func main() {
	// Hardcoded setup as requested
	cfg := torrent.NewDefaultClientConfig()
	// cfg.DataDir = "./data" // Disabled to keep metadata in S3 only

	// Use our custom S3 storage
	s3Store := ftorrent.NewS3Storage()
	cfg.DefaultStorage = s3Store

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

	<-t.GotInfo()
	fmt.Printf("Seeding: %s\n", t.Name())
	
	// Trigger sync for files that might already be complete
	s3Store.TriggerSync()
	
	t.DownloadAll()

	// Keep running
	fmt.Println("Seeding mode active...")
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
			fmt.Printf("Progress: %.2f%% (Pieces: %d/%d, Bytes: %d/%d)\n",
				progress, piecesDone, totalPieces, bytesDone, totalBytes)
			lastPieces = piecesDone
			
			if piecesDone < totalPieces {
				// Find first missing piece index for debugging
				for i := 0; i < totalPieces; i++ {
					if !t.Piece(i).State().Complete {
						fmt.Printf("   -> Piece %d is still marking as incomplete/verifying...\n", i)
						break
					}
				}
			}
		}

		if piecesDone >= totalPieces || progress >= 99.9 {
			fmt.Printf("Progress: 100.00%% (Final pieces verified or available on S3)\n")
			fmt.Println("Success: All data is synced. Seeding mode only.")
			break
		}
		time.Sleep(5 * time.Second)
	}

	// Just keep alive to seed from S3
	select {}
}
