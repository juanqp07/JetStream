package service

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"jetstream/internal/config"
	"jetstream/pkg/subsonic"

	"github.com/bogem/id3v2/v2"
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

func (s *SyncService) CreateGhostFile(song *subsonic.Song) error {
	artistDir := s.SanitizePath(song.Artist)
	albumDir := s.SanitizePath(song.Album)
	// Put in a dedicated Search Results folder so it's easy to clear, while still being in /music
	fullDir := filepath.Join(s.cfg.MusicFolder, "Search Results", artistDir, albumDir)

	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return err
	}

	// Filename: {Track} - {Title}.mp3
	fileName := fmt.Sprintf("%02d - %s.mp3", song.Track, s.SanitizePath(song.Title))
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
	ghostDir := filepath.Join(s.cfg.MusicFolder, "Search Results")
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
