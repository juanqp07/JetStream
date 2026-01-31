package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"jetstream/internal/config"
	"jetstream/pkg/subsonic"
	"net/http"
	"sync"
	"time"

	"log/slog"

	"github.com/redis/go-redis/v9"
)

const (
	UserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0"
	CachePrefix = "jetstream:cache:v1:"
)

type URLState struct {
	URL           string
	NextAvailable time.Time
}

type SquidService struct {
	client          *http.Client
	cfg             *config.Config
	redis           *redis.Client
	currentURLIndex int
	urlMutex        sync.RWMutex
	urlStates       []URLState
}

type albumCacheEntry struct {
	Album *subsonic.Album
	Songs []subsonic.Song
}

type playlistCacheEntry struct {
	Playlist *subsonic.Playlist
	Songs    []subsonic.Song
}

type artistCacheEntry struct {
	Artist *subsonic.Artist
	Albums []subsonic.Album
}

type TrackInfo struct {
	DownloadURL string
	MimeType    string
}

func NewSquidService(cfg *config.Config) *SquidService {
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
	})

	// Custom Transport for Connection Pooling
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	states := make([]URLState, 0)
	if len(cfg.SquidURLs) > 0 {
		for _, u := range cfg.SquidURLs {
			states = append(states, URLState{URL: u, NextAvailable: time.Now()})
		}
	} else if cfg.SquidURL != "" {
		states = append(states, URLState{URL: cfg.SquidURL, NextAvailable: time.Now()})
	}

	return &SquidService{
		client: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		cfg:             cfg,
		redis:           rdb,
		currentURLIndex: 0,
		urlStates:       states,
	}
}

// getCurrentURL returns the currently active Squid URL, skipping those on cooldown
func (s *SquidService) getCurrentURL() string {
	s.urlMutex.RLock()
	defer s.urlMutex.RUnlock()

	if len(s.urlStates) == 0 {
		return s.cfg.SquidURL
	}

	now := time.Now()
	// 1. Try to find the first available starting from currentURLIndex
	for i := 0; i < len(s.urlStates); i++ {
		idx := (s.currentURLIndex + i) % len(s.urlStates)
		if s.urlStates[idx].NextAvailable.Before(now) {
			return s.urlStates[idx].URL
		}
	}

	// 2. Fallback: If all are on cooldown, pick the one that becomes available first
	// (But still follow circular logic if possible, or just the next one)
	slog.Warn("All Squid URLs are on cooldown, picking the next in line anyway")
	return s.urlStates[s.currentURLIndex].URL
}

// rotateURL moves to the next fallback URL and marks the current one as temporarily unavailable (cooldown)
func (s *SquidService) markFailure(baseURL string) {
	s.urlMutex.Lock()
	defer s.urlMutex.Unlock()

	cooldown := 30 * time.Minute
	found := false
	for i := range s.urlStates {
		if s.urlStates[i].URL == baseURL {
			s.urlStates[i].NextAvailable = time.Now().Add(cooldown)
			slog.Warn("Marked URL on cooldown", "url", baseURL, "until", s.urlStates[i].NextAvailable)
			found = true
			break
		}
	}

	if found && len(s.urlStates) > 1 {
		s.currentURLIndex = (s.currentURLIndex + 1) % len(s.urlStates)
		slog.Info("Rotating to next URL index", "newIndex", s.currentURLIndex)
	}
}

// tryWithFallback attempts the action with all available URLs
func (s *SquidService) tryWithFallback(ctx context.Context, action func(baseURL string) error) error {
	var lastErr error
	maxAttempts := len(s.urlStates)
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	// We allow walking through the list once. If we hit the end and everything is failed/cooldown, we wrap
	for attempt := 0; attempt < maxAttempts; attempt++ {
		baseURL := s.getCurrentURL()
		err := action(baseURL)
		if err == nil {
			return nil
		}

		lastErr = err

		// Detect 429
		is429 := contains(err.Error(), "429")

		if is429 {
			slog.Warn("Rate limited (429) on endpoint", "baseURL", baseURL)
			s.markFailure(baseURL)
			continue
		}

		slog.Warn("Squid request failed", "baseURL", baseURL, "error", err, "attempt", attempt+1)

		// Any other failure also triggers a rotation and short status check
		s.markFailure(baseURL)
		time.Sleep(100 * time.Millisecond)
	}

	slog.Error("All fallback endpoints failed or on cooldown", "lastErr", lastErr)
	return lastErr
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(substr) > 0 && (s[:len(substr)] == substr || contains(s[1:], substr))))
}

func (s *SquidService) GetStreamURL(ctx context.Context, trackID string) (*TrackInfo, error) {
	_, _, _, rawID := subsonic.ParseID(trackID)
	quality := "LOSSLESS"

	var trackInfo *TrackInfo
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		url := fmt.Sprintf("%s/track/?id=%s&quality=%s", baseURL, rawID, quality)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", UserAgent)

		slog.Debug("Requesting Stream Info", "url", url)

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

		slog.Debug("Decoded Stream URL", "trackID", trackID, "mime", manifest.MimeType)

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
