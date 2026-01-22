package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"jetstream/internal/config"
	"net/http"
	"strings"
	"time"
)

type SquidService struct {
	client *http.Client
	cfg    *config.Config
}

type TrackInfo struct {
	DownloadURL string
	MimeType    string
}

func NewSquidService(cfg *config.Config) *SquidService {
	return &SquidService{
		client: &http.Client{Timeout: 10 * time.Second},
		cfg:    cfg,
	}
}

func (s *SquidService) GetStreamURL(trackID string) (*TrackInfo, error) {
	// 1. Construct URL
	// Strip prefix if present
	rawID := trackID
	if strings.Contains(trackID, "song-") {
		parts := strings.Split(trackID, "song-")
		rawID = parts[len(parts)-1]
	}

	quality := "LOSSLESS" // Default to lossless
	url := fmt.Sprintf("%s/track/?id=%s&quality=%s", s.cfg.SquidURL, rawID, quality)

	// 2. Make Request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Headers matching C# implementation
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")

	// Log the URL we are requesting
	fmt.Printf("[Squid] Requesting Stream Info: %s\n", url)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}

	// 3. Parse Response
	var result struct {
		Data struct {
			Manifest string `json:"manifest"` // Base64 encoded
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// 4. Decode Manifest
	manifestJSON, err := base64.StdEncoding.DecodeString(result.Data.Manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to decode manifest: %v", err)
	}

	var manifest struct {
		URLs     []string `json:"urls"`
		MimeType string   `json:"mimeType"`
	}

	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest json: %v", err)
	}

	if len(manifest.URLs) == 0 {
		return nil, fmt.Errorf("no download urls found")
	}

	fmt.Printf("[Squid] Decoded Stream URL: %s\n", manifest.URLs[0])

	// 5. Return Info
	return &TrackInfo{
		DownloadURL: manifest.URLs[0],
		MimeType:    manifest.MimeType,
	}, nil
}
