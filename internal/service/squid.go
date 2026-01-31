package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"jetstream/internal/config"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0"

type SquidService struct {
	client          *http.Client
	cfg             *config.Config
	redis           *redis.Client
	currentURLIndex int
	urlMutex        sync.RWMutex
}

type albumCacheEntry struct {
	Album *subsonic.Album
	Songs []subsonic.Song
}

type TrackInfo struct {
	DownloadURL string
	MimeType    string
}

func NewSquidService(cfg *config.Config) *SquidService {
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
	})

	return &SquidService{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		cfg:             cfg,
		redis:           rdb,
		currentURLIndex: 0,
	}
}

// getCurrentURL returns the currently active Squid URL
func (s *SquidService) getCurrentURL() string {
	s.urlMutex.RLock()
	defer s.urlMutex.RUnlock()
	if len(s.cfg.SquidURLs) == 0 {
		return s.cfg.SquidURL
	}
	return s.cfg.SquidURLs[s.currentURLIndex]
}

// rotateURL moves to the next fallback URL
func (s *SquidService) rotateURL() {
	s.urlMutex.Lock()
	defer s.urlMutex.Unlock()
	if len(s.cfg.SquidURLs) > 0 {
		s.currentURLIndex = (s.currentURLIndex + 1) % len(s.cfg.SquidURLs)
		log.Printf("[Squid] Rotated to fallback URL: %s", s.cfg.SquidURLs[s.currentURLIndex])
	}
}

// tryWithFallback attempts the action with all available URLs
func (s *SquidService) tryWithFallback(action func(baseURL string) error) error {
	var lastErr error
	maxAttempts := len(s.cfg.SquidURLs)
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		baseURL := s.getCurrentURL()
		err := action(baseURL)
		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("[Squid] Request failed with %s: %v", baseURL, err)

		if attempt < maxAttempts-1 {
			s.rotateURL()
		}
	}

	log.Printf("[Squid] All endpoints failed")
	return lastErr
}

func (s *SquidService) GetStreamURL(trackID string) (*TrackInfo, error) {
	_, _, _, rawID := subsonic.ParseID(trackID)
	quality := "LOSSLESS"

	var trackInfo *TrackInfo
	err := s.tryWithFallback(func(baseURL string) error {
		url := fmt.Sprintf("%s/track/?id=%s&quality=%s", baseURL, rawID, quality)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", UserAgent)

		fmt.Printf("[Squid] Requesting Stream Info: %s\n", url)

		resp, err := s.client.Do(req)
		if err != nil {
			return fmt.Errorf("network error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		var result struct {
			Data struct {
				Manifest string `json:"manifest"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("failed to decode json: %v", err)
		}

		manifestJSON, err := base64.StdEncoding.DecodeString(result.Data.Manifest)
		if err != nil {
			return fmt.Errorf("failed to decode manifest: %v", err)
		}

		var manifest struct {
			URLs     []string `json:"urls"`
			MimeType string   `json:"mimeType"`
		}

		if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
			return fmt.Errorf("failed to parse manifest: %v", err)
		}

		if len(manifest.URLs) == 0 {
			return fmt.Errorf("no download urls in manifest")
		}

		fmt.Printf("[Squid] Decoded Stream URL: %s\n", manifest.URLs[0])

		trackInfo = &TrackInfo{
			DownloadURL: manifest.URLs[0],
			MimeType:    manifest.MimeType,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return trackInfo, nil
}
func (s *SquidService) GetRedis() *redis.Client {
	return s.redis
}
