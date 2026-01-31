package service

import (
	"context"
	"encoding/json"
	"fmt"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GetLyrics fetches lyrics for a track ID
func (s *SquidService) GetLyrics(id string) (string, error) {
	_, _, _, numericID := subsonic.ParseID(id)

	urlStr := fmt.Sprintf("%s/lyrics/?id=%s", s.getCurrentURL(), numericID)
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
	ctx := context.Background()
	cacheKey := fmt.Sprintf("song:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var song subsonic.Song
		if err := json.Unmarshal([]byte(val), &song); err == nil {
			return &song, nil
		}
	}

	_, _, _, numericID := subsonic.ParseID(id)

	// Try /info/ first for clean metadata
	urlStr := fmt.Sprintf("%s/info/?id=%s", s.getCurrentURL(), numericID)
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.client.Do(req)

	if err != nil || resp.StatusCode != http.StatusOK {
		// Fallback to /track/ if /info/ fails (sometimes /track/ has more data or is more reliable)
		log.Printf("[Squid] /info/ failed for %s, trying /track/", numericID)
		urlStr = fmt.Sprintf("%s/track/?id=%s", s.getCurrentURL(), numericID)
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

	// Cache Result
	if data, err := json.Marshal(song); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return song, nil
}

func (s *SquidService) GetAlbum(id string) (*subsonic.Album, []subsonic.Song, error) {
	ctx := context.Background()
	cacheKey := fmt.Sprintf("album:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var entry albumCacheEntry
		if err := json.Unmarshal([]byte(val), &entry); err == nil {
			return entry.Album, entry.Songs, nil
		}
	}

	// ID format: ext-squidwtf-album-{numericID}
	parts := strings.Split(id, "-")
	if len(parts) < 4 {
		return nil, nil, fmt.Errorf("invalid id format")
	}
	numericID := parts[3]

	var album *subsonic.Album
	var songs []subsonic.Song

	err := s.tryWithFallback(func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/album/?id=%s", baseURL, numericID)

		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
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
			return err
		}

		data := result.Data

		// Map Album
		year := 0
		if len(data.ReleaseDate) >= 4 {
			fmt.Sscanf(data.ReleaseDate, "%d", &year)
		}

		album = &subsonic.Album{
			ID:        subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", data.ID)),
			Title:     data.Title,
			Name:      data.Title,
			SongCount: data.NumberOfTracks,
			Year:      year,
			CoverArt:  subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", data.ID)),
			Artist:    data.Artist.Name,
			ArtistID:  subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", data.Artist.ID)),
		}

		// Map Tracks
		songs = []subsonic.Song{}
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

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	// Cache Result
	entry := albumCacheEntry{Album: album, Songs: songs}
	if data, err := json.Marshal(entry); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}
	return album, songs, nil
}

// GetArtist fetches artist details (and top albums/songs usually, but Subsonic getArtist expects albums)
func (s *SquidService) GetArtist(id string) (*subsonic.Artist, []subsonic.Album, error) {
	ctx := context.Background()
	cacheKey := fmt.Sprintf("artist:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var entry artistCacheEntry
		if err := json.Unmarshal([]byte(val), &entry); err == nil {
			return entry.Artist, entry.Albums, nil
		}
	}

	parts := strings.Split(id, "-")
	if len(parts) < 4 {
		return nil, nil, fmt.Errorf("invalid id format")
	}
	numericID := parts[3]

	// Parallel Requests
	var (
		artistName string
		items      []struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Artist struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"artist"`
		}
		wg sync.WaitGroup
	)

	wg.Add(2)

	// 1. Fetch Artist Metadata
	go func() {
		defer wg.Done()
		metaURL := fmt.Sprintf("%s/artist/?id=%s", s.getCurrentURL(), numericID)
		reqMeta, _ := http.NewRequest("GET", metaURL, nil)
		reqMeta.Header.Set("User-Agent", UserAgent)
		respMeta, err := s.client.Do(reqMeta)

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
	}()

	// 2. Fetch Artist Albums
	var errAlbums error
	go func() {
		defer wg.Done()
		urlStr := fmt.Sprintf("%s/artist/?f=%s", s.getCurrentURL(), numericID)
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := s.client.Do(req)
		if err != nil {
			errAlbums = err
			return
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
			errAlbums = err
			return
		}
		items = result.Albums.Items
	}()

	wg.Wait()

	if errAlbums != nil {
		return nil, nil, errAlbums
	}

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
	seenAlbums := make(map[string]bool)

	for _, alb := range items {
		albumID := subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", alb.ID))
		if seenAlbums[albumID] {
			continue
		}
		seenAlbums[albumID] = true

		albums = append(albums, subsonic.Album{
			ID:       albumID,
			Title:    alb.Title,
			Name:     alb.Title,
			Artist:   artistName,
			ArtistID: artist.ID,
			CoverArt: albumID, // Explicitly set CoverArt to Album ID
		})
	}

	// Cache Result
	entry := artistCacheEntry{Artist: artist, Albums: albums}
	if data, err := json.Marshal(entry); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return artist, albums, nil
}

func (s *SquidService) GetPlaylist(id string) (*subsonic.Playlist, []subsonic.Song, error) {
	ctx := context.Background()
	cacheKey := fmt.Sprintf("playlist:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var entry playlistCacheEntry
		if err := json.Unmarshal([]byte(val), &entry); err == nil {
			return entry.Playlist, entry.Songs, nil
		}
	}

	_, _, _, uuid := subsonic.ParseID(id)

	urlStr := fmt.Sprintf("%s/playlist/?id=%s", s.getCurrentURL(), uuid)
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

	// Correct structure: Root has "playlist" and "items"
	var result struct {
		Playlist struct {
			UUID           string `json:"uuid"`
			Title          string `json:"title"`
			SquareImage    string `json:"squareImage"`
			NumberOfTracks int    `json:"numberOfTracks"`
			Duration       int    `json:"duration"`
		} `json:"playlist"`
		Items []struct {
			Item struct {
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
			} `json:"item"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil, err
	}

	data := result.Playlist
	if data.UUID == "" {
		return nil, nil, fmt.Errorf("playlist not found (empty uuid)")
	}

	playlist := &subsonic.Playlist{
		ID:        subsonic.BuildID("squidwtf", "playlist", data.UUID),
		Name:      data.Title,
		SongCount: data.NumberOfTracks,
		Duration:  data.Duration,
		CoverArt:  subsonic.BuildID("squidwtf", "playlist", data.UUID),
	}

	var songs []subsonic.Song
	for _, wrapper := range result.Items {
		item := wrapper.Item
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

	// Cache Result
	entry := playlistCacheEntry{Playlist: playlist, Songs: songs}
	if data, err := json.Marshal(entry); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return playlist, songs, nil
}

func (s *SquidService) GetCoverURL(id string) (string, error) {
	ctx := context.Background()
	cacheKey := fmt.Sprintf("cover:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil && val != "" {
		return val, nil
	}

	var coverURL string
	var err error

	if strings.Contains(id, "-album-") {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		urlStr := fmt.Sprintf("%s/album/?id=%s", s.getCurrentURL(), numericID)
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err2 := s.client.Do(req)
		if err2 != nil {
			return "", err2
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

		if result.Data.Cover == "" {
			return "", fmt.Errorf("no cover art for album")
		}
		uuid := strings.ToLower(strings.ReplaceAll(result.Data.Cover, "-", "/"))
		coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid)
	} else if strings.Contains(id, "-song-") {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		urlStr := fmt.Sprintf("%s/info/?id=%s", s.getCurrentURL(), numericID)
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err2 := s.client.Do(req)
		if err2 != nil {
			return "", err2
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

		if result.Data.Album.Cover == "" {
			return "", fmt.Errorf("no cover art for song/album")
		}
		uuid := strings.ToLower(strings.ReplaceAll(result.Data.Album.Cover, "-", "/"))
		coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid)
	} else if strings.Contains(id, "-artist-") {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		urlStr := fmt.Sprintf("%s/artist/?id=%s", s.getCurrentURL(), numericID)
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err2 := s.client.Do(req)
		if err2 != nil {
			return "", err2
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

		if result.Artist.Picture == "" {
			return "", fmt.Errorf("no picture for artist")
		}
		uuid := strings.ToLower(strings.ReplaceAll(result.Artist.Picture, "-", "/"))
		coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid)
	} else if strings.Contains(id, "-playlist-") {
		_, _, _, uuid := subsonic.ParseID(id)
		urlStr := fmt.Sprintf("%s/playlist/?id=%s", s.getCurrentURL(), uuid)
		req, _ := http.NewRequest("GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err2 := s.client.Do(req)
		if err2 != nil {
			return "", err2
		}
		defer resp.Body.Close()

		var result struct {
			Playlist struct {
				SquareImage string `json:"squareImage"`
			} `json:"playlist"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}

		if result.Playlist.SquareImage == "" {
			return "", fmt.Errorf("no cover art for playlist")
		}
		imgUuid := strings.ToLower(strings.ReplaceAll(result.Playlist.SquareImage, "-", "/"))
		coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", imgUuid)
	} else {
		return "", fmt.Errorf("unsupported type for cover")
	}

	if coverURL != "" {
		s.redis.Set(ctx, cacheKey, coverURL, 7*24*time.Hour) // Cache covers for 1 week
	}

	return coverURL, err
}
