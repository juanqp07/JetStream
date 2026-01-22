package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"jetstream/pkg/subsonic"
	"strings"
)

// GetSong fetches song details from Squid
func (s *SquidService) GetSong(id string) (*subsonic.Song, error) {
	// ID format: ext-squidwtf-song-{numericID}
	parts := strings.Split(id, "-")
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid id format")
	}
	numericID := parts[3]

	urlStr := fmt.Sprintf("%s/info/?id=%s", s.cfg.SquidURL, numericID)

	// Perform Request
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch song info")
	}

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
	return &subsonic.Song{
		ID:          subsonic.BuildID("squidwtf", "song", fmt.Sprintf("%d", item.ID)),
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
	}, nil
}

// GetAlbum fetches album details from Squid
func (s *SquidService) GetAlbum(id string) (*subsonic.Album, []subsonic.Song, error) {
	// ID format: ext-squidwtf-album-{numericID}
	parts := strings.Split(id, "-")
	if len(parts) < 4 {
		return nil, nil, fmt.Errorf("invalid id format")
	}
	numericID := parts[3]

	urlStr := fmt.Sprintf("%s/album/?id=%s", s.cfg.SquidURL, numericID)

	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")
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
	reqMeta.Header.Set("User-Agent", "Mozilla/5.0")
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
	req.Header.Set("User-Agent", "Mozilla/5.0")
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
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

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

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
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
		req.Header.Set("User-Agent", "Mozilla/5.0")
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
