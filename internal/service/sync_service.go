package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"jetstream/internal/config"
)

type SyncService struct {
	squid *SquidService
	cfg   *config.Config
}

func NewSyncService(squid *SquidService, cfg *config.Config) *SyncService {
	return &SyncService{
		squid: squid,
		cfg:   cfg,
	}
}

func (s *SyncService) GetDownloadFormat() string {
	return s.cfg.DownloadFormat
}

func (s *SyncService) SanitizePath(path string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return r.Replace(path)
}

func (s *SyncService) SyncAlbum(albumID string) error {
	fmt.Printf("[SyncService] Syncing Album ID: %s to folder: %s\n", albumID, s.cfg.MusicFolder)
	album, songs, err := s.squid.GetAlbum(albumID)
	if err != nil {
		return err
	}

	artistDir := s.SanitizePath(album.Artist)
	albumDir := s.SanitizePath(album.Title)

	fullDir := filepath.Join(s.cfg.MusicFolder, artistDir, albumDir)
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return fmt.Errorf("failed to create dir %s: %v", fullDir, err)
	}

	for _, song := range songs {
		if err := s.SyncSong(song.ID); err != nil {
			fmt.Printf("[SyncService] Failed to sync song %s: %v\n", song.ID, err)
		}
	}

	return nil
}

func (s *SyncService) SyncSong(songID string) error {
	fmt.Printf("[SyncService] Syncing Song ID: %s\n", songID)
	song, err := s.squid.GetSong(songID)
	if err != nil {
		return err
	}

	// Resolve Stream URL
	trackInfo, err := s.squid.GetStreamURL(song.ID)
	if err != nil {
		return fmt.Errorf("failed to get stream url: %v", err)
	}

	artistDir := s.SanitizePath(song.Artist)
	albumDir := s.SanitizePath(song.Album)
	fullDir := filepath.Join(s.cfg.MusicFolder, artistDir, albumDir)
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return err
	}

	ext := s.cfg.DownloadFormat
	fileName := fmt.Sprintf("%02d - %s.%s", song.Track, s.SanitizePath(song.Title), ext)
	filePath := filepath.Join(fullDir, fileName)

	if _, err := os.Stat(filePath); err == nil {
		return nil // Already exists
	}

	return s.downloadAndTranscode(trackInfo.DownloadURL, filePath, ext)
}

func (s *SyncService) downloadAndTranscode(url, outputPath, format string) error {
	fmt.Printf("[SyncService] Downloading and transcoding to %s: %s\n", format, outputPath)

	// Use ffmpeg to download and transcode on the fly
	// ffmpeg -i [url] -c:a [codec] [path]
	var codec string
	switch format {
	case "opus":
		codec = "libopus"
	case "mp3":
		codec = "libmp3lame"
	case "aac":
		codec = "aac"
	default:
		codec = "copy" // Fallback to copy if unknown
	}

	args := []string{"-i", url, "-c:a", codec, "-y", outputPath}
	cmd := exec.Command("ffmpeg", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %v, output: %s", err, string(output))
	}

	return nil
}
