package service

import (
	"context"
	"fmt"
	"io"
	"jetstream/internal/config"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type SyncService struct {
	squid *SquidService
	redis *redis.Client
	cfg   *config.Config
}

func NewSyncService(squid *SquidService, cfg *config.Config) *SyncService {
	return &SyncService{
		squid: squid,
		redis: squid.GetRedis(),
		cfg:   cfg,
	}
}

func (s *SyncService) SyncAlbum(album *subsonic.Album, songs []subsonic.Song) error {
	log.Printf("[SyncService] Syncing all tracks for album: %s", album.Title)
	for _, song := range songs {
		if err := s.SyncSong(&song); err != nil {
			log.Printf("[SyncService] Failed to sync song %s: %v", song.Title, err)
		}
	}
	return nil
}

func (s *SyncService) SyncSong(song *subsonic.Song) error {
	// 1. Determine local path
	artistDir := s.SanitizePath(song.Artist)
	albumDir := s.SanitizePath(song.Album)
	targetDir := filepath.Join("/music/jetstream", artistDir, albumDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	format := s.GetDownloadFormat()

	fileName := fmt.Sprintf("%02d - [%s] %s.%s", song.Track, song.ID, s.SanitizePath(song.Title), format)
	outputPath := filepath.Join(targetDir, fileName)

	// 2. Save cover art as cover.jpg in the directory (best for Navidrome/Opus)
	if song.CoverArt != "" {
		coverPath := filepath.Join(targetDir, "cover.jpg")
		if _, err := os.Stat(coverPath); os.IsNotExist(err) {
			log.Printf("[SyncService] Saving cover.jpg for album in: %s", targetDir)
			coverData, err := s.downloadArt(song.CoverArt)
			if err == nil {
				os.WriteFile(coverPath, coverData, 0644)
			} else {
				log.Printf("[SyncService] Failed to save cover.jpg: %v", err)
			}
		}
	}

	// 3. Check if song file exists
	if _, err := os.Stat(outputPath); err == nil {
		return nil // Already synced
	}

	// 4. Get Stream URL
	info, err := s.squid.GetStreamURL(song.ID)
	if err != nil {
		return err
	}

	// 5. Download and Transcode
	log.Printf("[SyncService] Downloading and transcoding to %s: %s", format, outputPath)
	return s.downloadAndTranscode(song, info.DownloadURL, outputPath, format)
}

func (s *SyncService) downloadAndTranscode(song *subsonic.Song, url, outputPath, format string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	var codec string
	switch format {
	case "opus":
		codec = "libopus"
	case "mp3":
		codec = "libmp3lame"
	case "aac":
		codec = "aac"
	default:
		codec = "copy"
	}

	// Download cover art to a temp file first
	var coverPath string
	var cleanup func()
	if song.CoverArt != "" {
		var err error
		coverPath, cleanup, err = s.downloadCoverToTemp(song.CoverArt)
		if err != nil {
			log.Printf("[SyncService] Failed to download cover art for %s: %v (continuing without it)", song.ID, err)
		} else {
			defer cleanup()
		}
	}

	// Build FFmpeg args based on format
	args := []string{"-i", url}

	// Add cover art as second input if available
	if coverPath != "" {
		args = append(args, "-i", coverPath)
	}

	// Format-specific encoding
	switch format {
	case "opus":
		args = append(args, "-c:a", codec, "-b:a", "128k")
		// Opus with Ogg doesn't play nice with MJPEG stream mapping in all FFmpeg versions.
		// Since we now save cover.jpg in the folder, we'll skip embedding to ensure speed and stability.
		args = append(args, "-map", "0:a")

	case "mp3":
		args = append(args, "-c:a", codec, "-q:a", "0")
		if coverPath != "" {
			args = append(args,
				"-map", "0:a",
				"-map", "1:0",
				"-c:v", "copy",
				"-id3v2_version", "3",
				"-metadata:s:v", "title=Album cover",
				"-metadata:s:v", "comment=Cover (front)",
			)
		} else {
			args = append(args, "-id3v2_version", "3")
		}

	case "aac":
		args = append(args, "-c:a", codec, "-b:a", "192k")
		if coverPath != "" {
			args = append(args,
				"-map", "0:a",
				"-map", "1:0",
				"-c:v", "copy",
				"-disposition:v:0", "attached_pic",
			)
		}

	default:
		args = append(args, "-c:a", "copy")
	}

	// Add comprehensive metadata
	args = append(args,
		"-metadata", "title="+song.Title,
		"-metadata", "artist="+song.Artist,
		"-metadata", "album_artist="+song.Artist,
		"-metadata", "album="+song.Album,
	)

	if song.Track > 0 {
		args = append(args, "-metadata", "track="+strconv.Itoa(song.Track))
	}
	if song.Year > 0 {
		args = append(args, "-metadata", "date="+strconv.Itoa(song.Year))
	}
	if song.Genre != "" {
		args = append(args, "-metadata", "genre="+song.Genre)
	}
	args = append(args, "-metadata", "comment=Synced by JetStream [ID:"+song.ID+"]")

	// Output to a temp file first to ensure atomicity
	tmpOutputPath := outputPath + ".tmp"

	// Help FFmpeg identify the format since we use .tmp extension
	var ffmpegFormat string
	switch format {
	case "opus":
		ffmpegFormat = "opus" // or "ogg"
	case "mp3":
		ffmpegFormat = "mp3"
	case "aac":
		ffmpegFormat = "adts" // standard for AAC streams
	}

	if ffmpegFormat != "" {
		args = append(args, "-f", ffmpegFormat)
	}
	args = append(args, "-y", tmpOutputPath)

	// Log the command for debugging
	log.Printf("[SyncService] FFmpeg command: ffmpeg %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			os.Remove(tmpOutputPath)
			return fmt.Errorf("ffmpeg timed out")
		}

		log.Printf("[SyncService] FFmpeg failed (Exit: %v). Output: %s", err, string(output))
		log.Printf("[SyncService] Retrying without cover art or complex mapping...")

		// Fallback: Transcode without cover art
		argsNoCover := []string{"-i", url}
		argsNoCover = append(argsNoCover, "-c:a", codec)
		if format == "opus" {
			argsNoCover = append(argsNoCover, "-b:a", "128k")
		} else if format == "mp3" {
			argsNoCover = append(argsNoCover, "-q:a", "0", "-id3v2_version", "3")
		} else if format == "aac" {
			argsNoCover = append(argsNoCover, "-b:a", "192k")
		}

		argsNoCover = append(argsNoCover,
			"-metadata", "title="+song.Title,
			"-metadata", "artist="+song.Artist,
			"-metadata", "album="+song.Album,
			"-y", tmpOutputPath,
		)

		log.Printf("[SyncService] Fallback FFmpeg command: ffmpeg %s", strings.Join(argsNoCover, " "))
		cmdFallback := exec.CommandContext(ctx, "ffmpeg", argsNoCover...)
		if fallbackOutput, fallbackErr := cmdFallback.CombinedOutput(); fallbackErr != nil {
			log.Printf("[SyncService] Fallback FFmpeg also failed: %v. Output: %s", fallbackErr, string(fallbackOutput))
			os.Remove(tmpOutputPath)
			return fmt.Errorf("ffmpeg failed: %v", fallbackErr)
		}
	}

	// Rename temp file to final destination
	if err := os.Rename(tmpOutputPath, outputPath); err != nil {
		log.Printf("[SyncService] Failed to move temp file to %s: %v", outputPath, err)
		return err
	}

	// Verify output file
	if info, err := os.Stat(outputPath); err == nil {
		sizeMB := float64(info.Size()) / 1024 / 1024
		log.Printf("[SyncService] Successfully created %s (%.2f MB)", outputPath, sizeMB)
	}

	return nil
}

func (s *SyncService) downloadCoverToTemp(coverID string) (string, func(), error) {
	// Download cover art
	coverData, err := s.downloadArt(coverID)
	if err != nil {
		return "", nil, err
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "cover-*.jpg")
	if err != nil {
		return "", nil, err
	}

	// Write data
	if _, err := tmpFile.Write(coverData); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, err
	}
	tmpFile.Close()

	// Return path and cleanup function
	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	log.Printf("[SyncService] Downloaded cover art to temp file: %s (%d bytes)", tmpFile.Name(), len(coverData))
	return tmpFile.Name(), cleanup, nil
}

func (s *SyncService) downloadArt(coverID string) ([]byte, error) {
	// If coverID looks like a URL, use it, otherwise use squid service to proxy
	var url string
	var err error
	if strings.HasPrefix(coverID, "http") {
		url = coverID
	} else {
		url, err = s.squid.GetCoverURL(coverID)
		if err != nil {
			return nil, err
		}
	}

	// Create request with proper headers
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "image/*,*/*")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download art: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (s *SyncService) SanitizePath(p string) string {
	p = strings.ReplaceAll(p, "/", "_")
	p = strings.ReplaceAll(p, "\\", "_")
	p = strings.ReplaceAll(p, ":", "_")
	p = strings.ReplaceAll(p, "*", "_")
	p = strings.ReplaceAll(p, "?", "_")
	p = strings.ReplaceAll(p, "\"", "_")
	p = strings.ReplaceAll(p, "<", "_")
	p = strings.ReplaceAll(p, ">", "_")
	p = strings.ReplaceAll(p, "|", "_")
	return strings.TrimSpace(p)
}

func (s *SyncService) SearchFolder() string {
	return os.Getenv("SEARCH_FOLDER")
}

func (s *SyncService) GetDownloadFormat() string {
	f := os.Getenv("DOWNLOAD_FORMAT")
	if f == "" {
		return "opus"
	}
	return f
}
