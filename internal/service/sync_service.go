package service

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"jetstream/internal/config"
	"jetstream/pkg/subsonic"

	"github.com/bogem/id3v2/v2"
)

type SyncService struct {
	squid *SquidService
	cfg   *config.Config
}

func NewSyncService(squid *SquidService, cfg *config.Config) *SyncService {
	s := &SyncService{
		squid: squid,
		cfg:   cfg,
	}
	go s.StartCleanupWorker()
	return s
}

func (s *SyncService) StartCleanupWorker() {
	log.Printf("[SyncService] Starting Cleanup Worker (Interval: 30m, Retention: 1h)")
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanupOldGhostFiles()
	}
}

func (s *SyncService) cleanupOldGhostFiles() {
	ghostDir := s.cfg.SearchFolder
	if _, err := os.Stat(ghostDir); os.IsNotExist(err) {
		return
	}

	retention := 1 * time.Hour
	now := time.Now()
	removedCount := 0

	err := filepath.Walk(ghostDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && now.Sub(info.ModTime()) > retention {
			if err := os.Remove(path); err == nil {
				removedCount++
			}
		}
		return nil
	})

	if err != nil {
		log.Printf("[SyncService] Error during cleanup: %v", err)
	}

	if removedCount > 0 {
		log.Printf("[SyncService] Cleanup complete. Removed %d old ghost files.", removedCount)
	}

	// Optional: Remove empty directories
	s.removeEmptyDirs(ghostDir)
}

func (s *SyncService) removeEmptyDirs(dir string) {
	// Walk from bottom up to remove empty leaf directories
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, f := range files {
		if f.IsDir() {
			s.removeEmptyDirs(filepath.Join(dir, f.Name()))
		}
	}

	// After subdirs are handled, check if this one is now empty
	if dir == s.cfg.SearchFolder {
		return // Don't remove the root search folder
	}

	files, err = os.ReadDir(dir)
	if err == nil && len(files) == 0 {
		os.Remove(dir)
	}
}

func (s *SyncService) GetDownloadFormat() string {
	return s.cfg.DownloadFormat
}

func (s *SyncService) SearchFolder() string {
	return s.cfg.SearchFolder
}

func (s *SyncService) SanitizePath(path string) string {
	// Remove invalid filesystem characters
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", "\x00", "",
	)
	sanitized := r.Replace(path)

	// Trim leading/trailing spaces and dots (problematic on Windows/some filesystems)
	sanitized = strings.Trim(sanitized, " .")

	// Limit length to avoid "FileName too long" errors (usually 255 but let's be safe)
	if len(sanitized) > 150 {
		sanitized = sanitized[:150]
	}

	return sanitized
}

func (s *SyncService) SyncAlbum(albumID string) error {
	log.Printf("[SyncService] Syncing Album ID: %s to folder: %s", albumID, s.cfg.MusicFolder)
	album, songs, err := s.squid.GetAlbum(albumID)
	if err != nil {
		return err
	}

	artistDir := s.SanitizePath(album.Artist)
	albumDir := s.SanitizePath(album.Title)

	fullDir := filepath.Join(s.cfg.MusicFolder, "raid", artistDir, albumDir)
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return fmt.Errorf("failed to create dir %s: %v", fullDir, err)
	}

	// Parallel Sync with concurrency limit
	concurrencyLimit := 3
	semaphore := make(chan struct{}, concurrencyLimit)
	var wg sync.WaitGroup

	for _, song := range songs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if err := s.SyncSong(id); err != nil {
				log.Printf("[SyncService] Failed to sync song %s: %v", id, err)
			}
		}(song.ID)
	}

	wg.Wait()
	return nil
}

func (s *SyncService) SyncSong(songID string) error {
	log.Printf("[SyncService] Syncing Song ID: %s", songID)
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
	// Put in 'raid' folder as requested for permanent storage
	fullDir := filepath.Join(s.cfg.MusicFolder, "raid", artistDir, albumDir)
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return err
	}

	ext := s.cfg.DownloadFormat
	// New Filename Format: {Track} - [{ID}] {Title}.{ext}
	fileName := fmt.Sprintf("%02d - [%s] %s.%s", song.Track, songID, s.SanitizePath(song.Title), ext)
	filePath := filepath.Join(fullDir, fileName)

	if _, err := os.Stat(filePath); err == nil {
		log.Printf("[SyncService] File already exists: %s", filePath)
		return nil // Already exists
	}

	return s.downloadAndTranscode(trackInfo.DownloadURL, filePath, ext)
}

func (s *SyncService) CreateGhostFile(song *subsonic.Song) error {
	artistDir := s.SanitizePath(song.Artist)
	albumDir := s.SanitizePath(song.Album)
	// Use the configured search folder
	fullDir := filepath.Join(s.cfg.SearchFolder, artistDir, albumDir)
	log.Printf("[SyncService] Creating ghost file in: %s", fullDir)

	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return err
	}

	// Filename: {Track} - [{ID}] {Title}.mp3
	fileName := fmt.Sprintf("%02d - [%s] %s.mp3", song.Track, song.ID, s.SanitizePath(song.Title))
	filePath := filepath.Join(fullDir, fileName)

	// Check if file already exists (real or ghost)
	if info, err := os.Stat(filePath); err == nil {
		if info.Size() > 10000 {
			return nil // Real file exists
		}
		// Ghost exists, maybe we update it or just skip
		return nil
	}

	// Create a dummy file (1KB)
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	f.Write(make([]byte, 1024))
	f.Close()

	// Add ID3 Tags
	tag, err := id3v2.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return err
	}
	defer tag.Close()

	tag.SetTitle(song.Title)
	tag.SetArtist(song.Artist)
	tag.SetAlbum(song.Album)
	tag.SetYear(strconv.Itoa(song.Year))
	tag.SetGenre(song.Genre)
	// Track number as string
	tag.AddTextFrame(tag.CommonID("Track number/Position in set"), id3v2.EncodingUTF8, strconv.Itoa(song.Track))

	// Embed Cover Art
	if song.CoverArt != "" {
		if artData, err := s.downloadArt(song.CoverArt); err == nil {
			pic := id3v2.PictureFrame{
				Encoding:    id3v2.EncodingUTF8,
				MimeType:    "image/jpeg",
				PictureType: id3v2.PTFrontCover,
				Description: "Front Cover",
				Picture:     artData,
			}
			tag.AddAttachedPicture(pic)
		}
	}

	// Add Tidal ID as custom TXXX frame for recovery
	tag.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{
		Encoding:    id3v2.EncodingUTF8,
		Description: "TIDAL_ID",
		Value:       song.ID,
	})

	return tag.Save()
}

func (s *SyncService) ClearSearchCache() error {
	ghostDir := s.cfg.SearchFolder
	if _, err := os.Stat(ghostDir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(ghostDir)
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

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download art: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (s *SyncService) downloadAndTranscode(url, outputPath, format string) error {
	// Set a reasonable timeout for transcode operations
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	log.Printf("[SyncService] Downloading and transcoding to %s: %s", format, outputPath)

	// Use ffmpeg to download and transcode on the fly
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

	args := []string{"-i", url, "-c:a", codec, "-y", outputPath}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg timed out after 10 minutes")
		}
		return fmt.Errorf("ffmpeg failed: %v, output: %s", err, string(output))
	}

	return nil
}
