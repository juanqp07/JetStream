package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"jetstream/internal/config"
	"jetstream/pkg/subsonic"
	"net/http"
	"sync"
	"time"
)

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0"

type SquidService struct {
	client     *http.Client
	cfg        *config.Config
	songCache  sync.Map // map[string]*subsonic.Song
	albumCache sync.Map // map[string]albumCacheEntry
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
	return &SquidService{
		client: &http.Client{
			Timeout: 30 * time.Second, // Increased timeout for potentially slow upstream responses
		},
		cfg: cfg,
	}
}

func (s *SquidService) GetStreamURL(trackID string) (*TrackInfo, error) {
	// 1. Construct URL
	_, _, _, rawID := subsonic.ParseID(trackID)

	quality := "LOSSLESS"
	url := fmt.Sprintf("%s/track/?id=%s&quality=%s", s.cfg.SquidURL, rawID, quality)

	// 2. Make Request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", UserAgent)

	fmt.Printf("[Squid] Requesting Stream Info: %s\n", url)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error connecting to squid: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream squid error: %d", resp.StatusCode)
	}

	// 3. Parse Response
	var result struct {
		Data struct {
			Manifest string `json:"manifest"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode json response: %v", err)
	}

	// 4. Decode Manifest
	manifestJSON, err := base64.StdEncoding.DecodeString(result.Data.Manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to decode manifest base64: %v", err)
	}

	var manifest struct {
		URLs     []string `json:"urls"`
		MimeType string   `json:"mimeType"`
	}

	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest json: %v", err)
	}

	if len(manifest.URLs) == 0 {
		return nil, fmt.Errorf("no download urls found in manifest")
	}

	fmt.Printf("[Squid] Decoded Stream URL: %s\n", manifest.URLs[0])

	return &TrackInfo{
		DownloadURL: manifest.URLs[0],
		MimeType:    manifest.MimeType,
	}, nil
}
