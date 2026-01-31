package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"jetstream/internal/config"
	"jetstream/pkg/subsonic"
	"log/slog"
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

func (s *SyncService) SyncAlbum(ctx context.Context, album *subsonic.Album, songs []subsonic.Song) error {
	slog.Info("Syncing all tracks for album", "album", album.Title)
	for _, song := range songs {
		if err := s.SyncSong(ctx, &song); err != nil {
			slog.Error("Failed to sync song", "title", song.Title, "error", err)
		}
	}
	return nil
}

func (s *SyncService) SyncSong(ctx context.Context, song *subsonic.Song) error {
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
			slog.Debug("Saving cover.jpg for album", "dir", targetDir)
			coverData, err := s.downloadArt(ctx, song.CoverArt)
			if err == nil {
				os.WriteFile(coverPath, coverData, 0644)
			} else {
				slog.Warn("Failed to save cover.jpg", "error", err)
			}
		}
	}

	// 3. Check if song file exists and is complete
	if _, err := os.Stat(outputPath); err == nil {
		// Verify integrity
		if err := s.VerifyIntegrity(outputPath); err == nil {
			// Ensure metadata sidecar also exists
			s.saveMetadata(song, outputPath)
			return nil // Already synced and complete
		}
		slog.Warn("Existing file is corrupt or incomplete. Re-syncing.", "path", outputPath)
	}

	// 4. Get Stream URL
	info, err := s.squid.GetStreamURL(ctx, song.ID)
	if err != nil {
		return err
	}

	// 5. Download and Transcode
	slog.Info("Downloading and transcoding", "format", format, "path", outputPath)
	return s.downloadAndTranscode(ctx, song, info.DownloadURL, outputPath, format)
}

func (s *SyncService) downloadAndTranscode(ctx context.Context, song *subsonic.Song, url, outputPath, format string) error {
	// Root context with timeout for the whole operation
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
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
		coverPath, cleanup, err = s.downloadCoverToTemp(ctx, song.CoverArt)
		if err != nil {
			slog.Warn("Failed to download cover art", "songID", song.ID, "error", err)
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
		ffmpegFormat = "opus"
	case "mp3":
		ffmpegFormat = "mp3"
	case "aac":
		ffmpegFormat = "adts"
	}

	if ffmpegFormat != "" {
		args = append(args, "-f", ffmpegFormat)
	}
	args = append(args, "-y", tmpOutputPath)

	slog.Debug("FFmpeg command", "args", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			os.Remove(tmpOutputPath)
			return fmt.Errorf("ffmpeg timed out")
		}

		slog.Warn("FFmpeg failed, retrying without complex mapping", "error", err, "output", string(output))

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

		slog.Debug("Fallback FFmpeg command", "args", strings.Join(argsNoCover, " "))
		cmdFallback := exec.CommandContext(ctx, "ffmpeg", argsNoCover...)
		if fallbackOutput, fallbackErr := cmdFallback.CombinedOutput(); fallbackErr != nil {
			slog.Error("Fallback FFmpeg failed", "error", fallbackErr, "output", string(fallbackOutput))
			os.Remove(tmpOutputPath)
			return fmt.Errorf("ffmpeg failed: %v", fallbackErr)
		}
	}

	if err := os.Rename(tmpOutputPath, outputPath); err != nil {
		slog.Error("Failed to move temp file", "from", tmpOutputPath, "to", outputPath, "error", err)
		return err
	}

	if info, err := os.Stat(outputPath); err == nil {
		slog.Info("Successfully synced", "path", outputPath, "sizeMB", float64(info.Size())/1024/1024)
		// Perform immediate integrity check
		if err := s.VerifyIntegrity(outputPath); err != nil {
			slog.Error("File integrity check failed after sync, removing", "path", outputPath, "error", err)
			os.Remove(outputPath)
			return err
		}
		// Save metadata sidecar
		s.saveMetadata(song, outputPath)
	}

	return nil
}

func (s *SyncService) downloadCoverToTemp(ctx context.Context, coverID string) (string, func(), error) {
	coverData, err := s.downloadArt(ctx, coverID)
	if err != nil {
		return "", nil, err
	}

	tmpFile, err := os.CreateTemp("", "cover-*.jpg")
	if err != nil {
		return "", nil, err
	}

	if _, err := tmpFile.Write(coverData); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, err
	}
	tmpFile.Close()

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	slog.Debug("Downloaded cover art to temp file", "path", tmpFile.Name(), "size", len(coverData))
	return tmpFile.Name(), cleanup, nil
}

func (s *SyncService) downloadArt(ctx context.Context, coverID string) ([]byte, error) {
	var url string
	var err error
	if strings.HasPrefix(coverID, "http") {
		url = coverID
	} else {
		url, err = s.squid.GetCoverURL(ctx, coverID)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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

func (s *SyncService) GetDownloadFormat() string {
	f := os.Getenv("DOWNLOAD_FORMAT")
	if f == "" {
		return "opus"
	}
	return f
}

// VerifyIntegrity checks if an audio file is valid using ffprobe
func (s *SyncService) VerifyIntegrity(path string) error {
	// Root context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use ffprobe to check if it's a valid audio file and has a duration
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	}

	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffprobe failed: %v (output: %s)", err, string(output))
	}

	durationStr := strings.TrimSpace(string(output))
	if durationStr == "" || durationStr == "N/A" {
		return fmt.Errorf("could not determine file duration")
	}

	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return fmt.Errorf("failed to parse duration: %v", err)
	}

	if duration <= 1.0 { // Sanity check: must be at least 1 second
		return fmt.Errorf("file duration too short (%.2fs)", duration)
	}

	return nil
}

// MaintenanceScan crawls the music folder and verifies all files
func (s *SyncService) MaintenanceScan(ctx context.Context) (int, int, error) {
	root := "/music/jetstream"
	var total, corrupt int

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}

		// Check extensions
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".opus" && ext != ".mp3" && ext != ".aac" && ext != ".flac" {
			return nil
		}

		total++
		if err := s.VerifyIntegrity(path); err != nil {
			corrupt++
			slog.Warn("Found corrupt file, deleting", "path", path, "error", err)
			os.Remove(path)
			os.Remove(path + ".json")
		} else {
			// If file is good, check if we can index its metadata
			jsonPath := path + ".json"
			if data, err := os.ReadFile(jsonPath); err == nil {
				var song subsonic.Song
				if err := json.Unmarshal(data, &song); err == nil {
					// Index ID to Path in Redis
					s.redis.Set(ctx, "path:"+song.ID, path, 90*24*time.Hour)
				}
			}
		}

		// Check context
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		return nil
	})

	return total, corrupt, err
}

func (s *SyncService) saveMetadata(song *subsonic.Song, mediaPath string) {
	jsonPath := mediaPath + ".json"
	data, err := json.MarshalIndent(song, "", "  ")
	if err != nil {
		slog.Error("Failed to marshal generic song metadata", "error", err)
		return
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		slog.Error("Failed to save metadata sidecar", "path", jsonPath, "error", err)
	} else {
		slog.Debug("Saved metadata sidecar", "path", jsonPath)
	}

	// Also index this ID to this path in Redis for fast lookup (long-lived)
	s.redis.Set(context.Background(), "path:"+song.ID, mediaPath, 90*24*time.Hour)
}
