# Funnel

**Funnel** adalah layanan yang bertindak sebagai jembatan antara jaringan BitTorrent dan object storage (S3/MinIO). Tujuannya adalah mengunduh torrent lalu menyimpan hasilnya langsung ke S3 — tanpa menyimpan file secara permanen di disk lokal.

Kasus penggunaan utama: mengotomatiskan pengunduhan konten (misalnya dari Nyaa, dll.) ke cloud storage dengan penggunaan disk yang minimal, sehingga file bisa diakses dari S3 kapan saja tanpa perlu menyimpan ulang.

Cara kerjanya:
1. Terima magnet link / torrent
2. Unduh data piece by piece via BitTorrent
3. Tulis ke disk lokal hanya sebagai buffer sementara (max 2 chunk × 10MB)
4. Segera upload ke S3 via multipart upload begitu chunk selesai diverifikasi
5. Hapus chunk lokal setelah upload berhasil
6. Seed dari S3 (baca langsung dari S3 untuk melayani peer)

## Struktur Proyek

```
cmd/funnel/main.go          # Entry point
pkg/torrent/s3_storage.go   # Custom S3 storage backend untuk anacrolix/torrent
docker-compose.yml          # MinIO lokal untuk development
Dockerfile                  # Build container (CGO_ENABLED=1, static binary)
```

## Commands

```bash
# Jalankan MinIO lokal
docker compose up -d

# Build
go build ./cmd/funnel/

# Run
go run ./cmd/funnel/

# Run dengan race detector
go run -race ./cmd/funnel/
```

## Arsitektur

- `S3Storage` mengimplementasikan `storage.ClientImpl` dari `anacrolix/torrent`
- File dibagi menjadi chunk 10MB, ditulis ke disk lokal sementara
- Setelah chunk selesai diverifikasi, langsung diupload ke S3 via multipart upload
- Maksimal 2 chunk tersimpan di disk sekaligus (`MaxLocalChunks`), sisanya di-GC setelah upload
- State multipart upload dan piece completion di-persist ke S3 untuk resumability
- Batas ukuran torrent: 100GB per torrent

## S3 / MinIO

| Key | Value |
|-----|-------|
| Endpoint | `http://localhost:9000` |
| Bucket | `funnel` |
| Access Key | `user` |
| Secret Key | `password` |
| Console | `http://localhost:9001` |

Data MinIO tersimpan di `.volumes/minio/`.

## Layout S3

```
{infoHash}/files/{name}                         # File final (setelah multipart complete)
{infoHash}/state/{pieceHash}                    # Piece completion marker
{infoHash}/state/multipart/{fileHex}.json       # Multipart upload state (UploadID + ETags)
{infoHash}/metainfo.json                        # Torrent metainfo
```

## Dependensi Utama

- `github.com/anacrolix/torrent` — BitTorrent client library
- `github.com/aws/aws-sdk-go-v2` — AWS/S3 SDK
