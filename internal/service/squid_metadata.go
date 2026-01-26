package service

import (
	"encoding/json"
	"fmt"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"strings"
)

// GetLyrics fetches lyrics for a track ID
func (s *SquidService) GetLyrics(id string) (string, error) {
	_, _, _, numericID := subsonic.ParseID(id)

	urlStr := fmt.Sprintf("%s/lyrics/?id=%s", s.cfg.SquidURL, numericID)
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lyrics not found")
	}

	var result struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Data, nil
}

// GetSong fetches song details from Squid
func (s *SquidService) GetSong(id string) (*subsonic.Song, error) {
	if val, ok := s.songCache.Load(id); ok {
		return val.(*subsonic.Song), nil
	}

	_, _, _, numericID := subsonic.ParseID(id)

	// Try /info/ first for clean metadata
	urlStr := fmt.Sprintf("%s/info/?id=%s", s.cfg.SquidURL, numericID)
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.client.Do(req)

	if err != nil || resp.StatusCode != http.StatusOK {
		// Fallback to /track/ if /info/ fails (sometimes /track/ has more data or is more reliable)
		log.Printf("[Squid] /info/ failed for %s, trying /track/", numericID)
		urlStr = fmt.Sprintf("%s/track/?id=%s", s.cfg.SquidURL, numericID)
		req, _ = http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err = s.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch song info from both /info/ and /track/")
		}
	}
	defer resp.Body.Close()

	// Parse Response
	var result struct {
		Data struct {
			ID          int64  `json:"id"`
			Title       string `json:"title"`
			Duration    int    `json:"duration"`
			TrackNumber int    `json:"trackNumber"`
			Artist      struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"artist"`
			Album struct {
				ID    int64  `json:"id"`
				Title string `json:"title"`
				Cover string `json:"cover"`
			} `json:"album"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	item := result.Data
	song := &subsonic.Song{
		ID:          subsonic.BuildID("squidwtf", "song", fmt.Sprintf("%d", item.ID)),
		Parent:      subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
		Title:       item.Title,
		Artist:      item.Artist.Name,
		ArtistID:    subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.Artist.ID)),
		Album:       item.Album.Title,
		AlbumID:     subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
		CoverArt:    subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
		Duration:    item.Duration,
		Track:       item.TrackNumber,
		Suffix:      "mp3",
		ContentType: "audio/mpeg",
		IsDir:       false,
		IsVideo:     false,
	}
	s.songCache.Store(id, song)
	return song, nil
}

// GetAlbum fetches album details from Squid
func (s *SquidService) GetAlbum(id string) (*subsonic.Album, []subsonic.Song, error) {
	if val, ok := s.albumCache.Load(id); ok {
		entry := val.(albumCacheEntry)
		return entry.Album, entry.Songs, nil
	}

	// ID format: ext-squidwtf-album-{numericID}
	parts := strings.Split(id, "-")
	if len(parts) < 4 {
		return nil, nil, fmt.Errorf("invalid id format")
	}
	numericID := parts[3]

	urlStr := fmt.Sprintf("%s/album/?id=%s", s.cfg.SquidURL, numericID)

	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("failed to fetch album info")
	}

	// Parse
	var result struct {
		Data struct {
			ID          int64  `json:"id"`
			Title       string `json:"title"`
			Cover       string `json:"cover"`
			ReleaseDate string `json:"releaseDate"`
			Artist      struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"artist"`
			Items []struct {
				Item struct {
					ID          int64  `json:"id"`
					Title       string `json:"title"`
					Duration    int    `json:"duration"`
					TrackNumber int    `json:"trackNumber"`
				} `json:"item"`
			} `json:"items"`
			NumberOfTracks int `json:"numberOfTracks"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
	}

	data := result.Data

	// Map Album
	year := 0
	if len(data.ReleaseDate) >= 4 {
		fmt.Sscanf(data.ReleaseDate, "%d", &year)
	}

	album := &subsonic.Album{
		ID:        subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", data.ID)),
		Title:     data.Title,
		Name:      data.Title,
		Artist:    data.Artist.Name,
		ArtistID:  subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", data.Artist.ID)),
		SongCount: data.NumberOfTracks,
		Year:      year,
		CoverArt:  subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", data.ID)),
	}

	// Map Tracks
	var songs []subsonic.Song
	for _, wrapper := range data.Items {
		t := wrapper.Item
		songs = append(songs, subsonic.Song{
			ID:          subsonic.BuildID("squidwtf", "song", fmt.Sprintf("%d", t.ID)),
			Parent:      album.ID,
			Title:       t.Title,
			Artist:      data.Artist.Name,
			ArtistID:    album.ArtistID,
			Album:       data.Title,
			AlbumID:     album.ID,
			CoverArt:    album.ID,
			Duration:    t.Duration,
			Track:       t.TrackNumber,
			Suffix:      "mp3",
			ContentType: "audio/mpeg",
			IsDir:       false,
			IsVideo:     false,
		})
	}

	s.albumCache.Store(id, albumCacheEntry{Album: album, Songs: songs})
	return album, songs, nil
}

// GetArtist fetches artist details (and top albums/songs usually, but Subsonic getArtist expects albums)
func (s *SquidService) GetArtist(id string) (*subsonic.Artist, []subsonic.Album, error) {
	parts := strings.Split(id, "-")
	if len(parts) < 4 {
		return nil, nil, fmt.Errorf("invalid id format")
	}
	numericID := parts[3]

	// 1. Fetch Artist Metadata (to get picture/name correctly)
	metaURL := fmt.Sprintf("%s/artist/?id=%s", s.cfg.SquidURL, numericID)
	reqMeta, _ := http.NewRequest("GET", metaURL, nil)
	reqMeta.Header.Set("User-Agent", UserAgent)
	respMeta, err := s.client.Do(reqMeta)

	var artistName string
	if err == nil && respMeta.StatusCode == http.StatusOK {
		var metaResult struct {
			Artist struct {
				Name    string `json:"name"`
				Picture string `json:"picture"`
			} `json:"artist"`
		}
		json.NewDecoder(respMeta.Body).Decode(&metaResult)
		artistName = metaResult.Artist.Name
		respMeta.Body.Close()
	}

	// 2. Fetch Artist Albums
	urlStr := fmt.Sprintf("%s/artist/?f=%s", s.cfg.SquidURL, numericID)
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Albums struct {
			Items []struct {
				ID     int64  `json:"id"`
				Title  string `json:"title"`
				Artist struct {
					ID   int64  `json:"id"`
					Name string `json:"name"`
				} `json:"artist"`
			} `json:"items"`
		} `json:"albums"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
	}

	items := result.Albums.Items
	if artistName == "" && len(items) > 0 {
		artistName = items[0].Artist.Name
	}

	artist := &subsonic.Artist{
		ID:         subsonic.BuildID("squidwtf", "artist", numericID),
		Name:       artistName,
		AlbumCount: len(items),
		CoverArt:   subsonic.BuildID("squidwtf", "artist", numericID),
	}

	var albums []subsonic.Album
	for _, alb := range items {
		albums = append(albums, subsonic.Album{
			ID:       subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", alb.ID)),
			Title:    alb.Title,
			Name:     alb.Title,
			Artist:   artistName,
			ArtistID: artist.ID,
		})
	}

	return artist, albums, nil
}

func (s *SquidService) GetPlaylist(id string) (*subsonic.Playlist, []subsonic.Song, error) {
	_, _, _, uuid := subsonic.ParseID(id)

	urlStr := fmt.Sprintf("%s/playlist/?id=%s", s.cfg.SquidURL, uuid)
	log.Printf("[Squid] [DEBUG] GET %s", urlStr)
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[Squid] [ERROR] Playlist API returned status: %d", resp.StatusCode)
		return nil, nil, fmt.Errorf("playlist not found or api error")
	}

	var rawResponse json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&rawResponse); err != nil {
		return nil, nil, err
	}

	// Double check the response structure (some versions might omit .data)
	var result struct {
		Data struct {
			UUID           string `json:"uuid"`
			Title          string `json:"title"`
			NumberOfTracks int    `json:"numberOfTracks"`
			Duration       int    `json:"duration"`
			Items          []struct {
				ID          int64  `json:"id"`
				Title       string `json:"title"`
				Duration    int    `json:"duration"`
				TrackNumber int    `json:"trackNumber"`
				Artist      struct {
					ID   int64  `json:"id"`
					Name string `json:"name"`
				} `json:"artist"`
				Album struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
				} `json:"album"`
			} `json:"items"`
		} `json:"data"`
	}

	if err := json.Unmarshal(rawResponse, &result); err != nil {
		// Try without .data wrapper just in case
		var directResult struct {
			UUID           string        `json:"uuid"`
			Title          string        `json:"title"`
			NumberOfTracks int           `json:"numberOfTracks"`
			Items          []interface{} `json:"items"`
		}
		if err2 := json.Unmarshal(rawResponse, &directResult); err2 == nil && directResult.UUID != "" {
			log.Printf("[Squid] [WARNING] Playlist response was direct (no .data wrapper)")
			// Handle direct if needed, but for now just fail with clear log
			log.Printf("[Squid] [DEBUG] RAW: %s", string(rawResponse))
		}
		return nil, nil, fmt.Errorf("failed to parse playlist response: %v", err)
	}

	data := result.Data
	playlist := &subsonic.Playlist{
		ID:        subsonic.BuildID("squidwtf", "playlist", data.UUID),
		Name:      data.Title,
		SongCount: data.NumberOfTracks,
		Duration:  data.Duration,
	}

	var songs []subsonic.Song
	for _, item := range data.Items {
		songs = append(songs, subsonic.Song{
			ID:          subsonic.BuildID("squidwtf", "song", fmt.Sprintf("%d", item.ID)),
			Parent:      subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
			Title:       item.Title,
			Artist:      item.Artist.Name,
			ArtistID:    subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.Artist.ID)),
			Album:       item.Album.Title,
			AlbumID:     subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
			CoverArt:    subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
			Duration:    item.Duration,
			Track:       item.TrackNumber,
			Suffix:      "mp3",
			ContentType: "audio/mpeg",
			IsDir:       false,
			IsVideo:     false,
		})
	}

	return playlist, songs, nil
}

func (s *SquidService) GetCoverURL(id string) (string, error) {
	// Basic implementation: Reuse GetAlbum or GetSong to extract cover
	// Optimization: Depending on Squid API, maybe a lighter call exists, but for now reuse.

	if strings.Contains(id, "-album-") {
		// ID format: ext-squidwtf-album-{numericID}
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		// We can just query album info directly here or refactor.
		// Let's do a direct quick fetch to avoid full parsing overhead if possible,
		// or just accept the overhead for simplicity now.

		// We can just query album info directly here or refactor.
		urlStr := fmt.Sprintf("%s/album/?id=%s", s.cfg.SquidURL, numericID)

		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")
		resp, err := s.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var result struct {
			Data struct {
				Cover string `json:"cover"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}

		// Format Tidal URL: https://resources.tidal.com/images/{uuid}/320x320.jpg
		// UUID needs "-" replaced by "/"
		uuid := strings.ReplaceAll(result.Data.Cover, "-", "/")
		return fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid), nil
	}

	if strings.Contains(id, "-song-") {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		urlStr := fmt.Sprintf("%s/info/?id=%s", s.cfg.SquidURL, numericID)

		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")
		resp, err := s.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var result struct {
			Data struct {
				Album struct {
					Cover string `json:"cover"`
				} `json:"album"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}

		uuid := strings.ReplaceAll(result.Data.Album.Cover, "-", "/")
		return fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid), nil
	}

	if strings.Contains(id, "-artist-") {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		urlStr := fmt.Sprintf("%s/artist/?id=%s", s.cfg.SquidURL, numericID)
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := s.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var result struct {
			Artist struct {
				Picture string `json:"picture"`
			} `json:"artist"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}

		uuid := strings.ReplaceAll(result.Artist.Picture, "-", "/")
		return fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid), nil
	}

	return "", fmt.Errorf("unsupported type for cover")
}
