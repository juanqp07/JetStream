package service

import (
	"context"
	"encoding/json"
	"fmt"
	"jetstream/pkg/subsonic"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GetLyrics fetches lyrics for a track ID
func (s *SquidService) GetLyrics(ctx context.Context, id string) (string, error) {
	cacheKey := CachePrefix + fmt.Sprintf("lyrics:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil && val != "" {
		return val, nil
	}

	_, _, _, numericID := subsonic.ParseID(id)

	var lyrics string
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/lyrics/?id=%s", baseURL, numericID)
		req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		var result struct {
			Data string `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}

		lyrics = result.Data
		return nil
	})

	if err != nil {
		return "", err
	}

	// Cache Result
	if lyrics != "" {
		s.redis.Set(ctx, cacheKey, lyrics, 7*24*time.Hour)
	}

	return lyrics, nil
}

// GetSong fetches song details from Squid
func (s *SquidService) GetSong(ctx context.Context, id string) (*subsonic.Song, error) {
	cacheKey := CachePrefix + fmt.Sprintf("song:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var song subsonic.Song
		if err := json.Unmarshal([]byte(val), &song); err == nil {
			return &song, nil
		}
	}

	_, _, _, numericID := subsonic.ParseID(id)

	var song *subsonic.Song
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		// Try /info/ first for clean metadata
		urlStr := fmt.Sprintf("%s/info/?id=%s", baseURL, numericID)
		req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := s.client.Do(req)

		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("HTTP 429")
			}
			// Fallback to /track/ if /info/ fails
			slog.Warn("/info/ failed, trying /track/", "numericID", numericID)
			urlStr = fmt.Sprintf("%s/track/?id=%s", baseURL, numericID)
			req, _ = http.NewRequestWithContext(ctx, "GET", urlStr, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err = s.client.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
					return fmt.Errorf("HTTP 429")
				}
				return fmt.Errorf("failed to fetch song info from both /info/ and /track/")
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
			return err
		}

		item := result.Data
		song = &subsonic.Song{
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
			Path:        fmt.Sprintf("squidwtf/%s/%s/%d.mp3", item.Artist.Name, item.Album.Title, item.ID),
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Cache Result
	if data, err := json.Marshal(song); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return song, nil
}

func (s *SquidService) GetAlbum(ctx context.Context, id string) (*subsonic.Album, []subsonic.Song, error) {
	cacheKey := CachePrefix + fmt.Sprintf("album:%s", id)

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

	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/album/?id=%s", baseURL, numericID)

		req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
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
			IsDir:     true,
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
				Path:        fmt.Sprintf("squidwtf/%s/%s/%d.mp3", data.Artist.Name, data.Title, t.ID),
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

// GetArtist fetches artist details
func (s *SquidService) GetArtist(ctx context.Context, id string) (*subsonic.Artist, []subsonic.Album, error) {
	cacheKey := CachePrefix + fmt.Sprintf("artist:%s", id)

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
	var metaErr error
	go func() {
		defer wg.Done()
		metaErr = s.tryWithFallback(ctx, func(baseURL string) error {
			metaURL := fmt.Sprintf("%s/artist/?id=%s", baseURL, numericID)
			reqMeta, _ := http.NewRequestWithContext(ctx, "GET", metaURL, nil)
			reqMeta.Header.Set("User-Agent", UserAgent)
			respMeta, err := s.client.Do(reqMeta)

			if err != nil || respMeta.StatusCode != http.StatusOK {
				if respMeta != nil && respMeta.StatusCode == http.StatusTooManyRequests {
					return fmt.Errorf("HTTP 429")
				}
				return fmt.Errorf("failed to fetch artist metadata")
			}
			defer respMeta.Body.Close()

			var metaResult struct {
				Artist struct {
					Name    string `json:"name"`
					Picture string `json:"picture"`
				} `json:"artist"`
			}
			json.NewDecoder(respMeta.Body).Decode(&metaResult)
			artistName = metaResult.Artist.Name
			return nil
		})
	}()

	// 2. Fetch Artist Albums
	var errAlbums error
	go func() {
		defer wg.Done()
		errAlbums = s.tryWithFallback(ctx, func(baseURL string) error {
			urlStr := fmt.Sprintf("%s/artist/?f=%s", baseURL, numericID)
			req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err := s.client.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
					return fmt.Errorf("HTTP 429")
				}
				return fmt.Errorf("failed to fetch artist albums")
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
				return err
			}
			items = result.Albums.Items
			return nil
		})
	}()

	wg.Wait()

	if metaErr != nil || errAlbums != nil {
		slog.Error("Failed to fetch artist info", "metaErr", metaErr, "albumsErr", errAlbums)
		return nil, nil, fmt.Errorf("failed to fetch artist info")
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
			CoverArt: albumID,
			IsDir:    true,
		})
	}

	// Cache Result
	entry := artistCacheEntry{Artist: artist, Albums: albums}
	if data, err := json.Marshal(entry); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return artist, albums, nil
}

func (s *SquidService) GetPlaylist(ctx context.Context, id string) (*subsonic.Playlist, []subsonic.Song, error) {
	cacheKey := CachePrefix + fmt.Sprintf("playlist:%s", id)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var entry playlistCacheEntry
		if err := json.Unmarshal([]byte(val), &entry); err == nil {
			return entry.Playlist, entry.Songs, nil
		}
	}

	_, _, _, uuid := subsonic.ParseID(id)

	var playlist *subsonic.Playlist
	var songs []subsonic.Song
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/playlist/?id=%s", baseURL, uuid)
		slog.Debug("Squid Playlist Request", "url", urlStr)
		req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := s.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("HTTP 429")
			}
			return fmt.Errorf("playlist not found or api error")
		}
		defer resp.Body.Close()

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
			return err
		}

		data := result.Playlist
		if data.UUID == "" {
			return fmt.Errorf("playlist not found (empty uuid)")
		}

		playlist = &subsonic.Playlist{
			ID:        subsonic.BuildID("squidwtf", "playlist", data.UUID),
			Name:      data.Title,
			SongCount: data.NumberOfTracks,
			Duration:  data.Duration,
			CoverArt:  subsonic.BuildID("squidwtf", "playlist", data.UUID),
		}

		songs = []subsonic.Song{}
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
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	// Cache Result
	entry := playlistCacheEntry{Playlist: playlist, Songs: songs}
	if data, err := json.Marshal(entry); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return playlist, songs, nil
}

func (s *SquidService) GetCoverURL(ctx context.Context, id string) (string, error) {
	cacheKey := CachePrefix + fmt.Sprintf("cover:%s", id)

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

		err = s.tryWithFallback(ctx, func(baseURL string) error {
			urlStr := fmt.Sprintf("%s/album/?id=%s", baseURL, numericID)
			req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err2 := s.client.Do(req)
			if err2 != nil || resp.StatusCode != http.StatusOK {
				if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
					return fmt.Errorf("HTTP 429")
				}
				return fmt.Errorf("failed to fetch album cover info")
			}
			defer resp.Body.Close()

			var result struct {
				Data struct {
					Cover string `json:"cover"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return err
			}

			if result.Data.Cover == "" {
				return fmt.Errorf("no cover art for album")
			}
			uuid := strings.ToLower(strings.ReplaceAll(result.Data.Cover, "-", "/"))
			coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid)
			return nil
		})
	} else if strings.Contains(id, "-song-") {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		err = s.tryWithFallback(ctx, func(baseURL string) error {
			urlStr := fmt.Sprintf("%s/info/?id=%s", baseURL, numericID)
			req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err2 := s.client.Do(req)
			if err2 != nil || resp.StatusCode != http.StatusOK {
				if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
					return fmt.Errorf("HTTP 429")
				}
				return fmt.Errorf("failed to fetch song cover info")
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
				return err
			}

			if result.Data.Album.Cover == "" {
				return fmt.Errorf("no cover art for song/album")
			}
			uuid := strings.ToLower(strings.ReplaceAll(result.Data.Album.Cover, "-", "/"))
			coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid)
			return nil
		})
	} else if strings.Contains(id, "-artist-") {
		parts := strings.Split(id, "-")
		if len(parts) < 4 {
			return "", fmt.Errorf("invalid id")
		}
		numericID := parts[3]

		err = s.tryWithFallback(ctx, func(baseURL string) error {
			urlStr := fmt.Sprintf("%s/artist/?id=%s", baseURL, numericID)
			req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err2 := s.client.Do(req)
			if err2 != nil || resp.StatusCode != http.StatusOK {
				if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
					return fmt.Errorf("HTTP 429")
				}
				return fmt.Errorf("failed to fetch artist cover info")
			}
			defer resp.Body.Close()

			var result struct {
				Artist struct {
					Picture string `json:"picture"`
				} `json:"artist"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return err
			}

			if result.Artist.Picture == "" {
				return fmt.Errorf("no picture for artist")
			}
			uuid := strings.ToLower(strings.ReplaceAll(result.Artist.Picture, "-", "/"))
			coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", uuid)
			return nil
		})
	} else if strings.Contains(id, "-playlist-") {
		_, _, _, uuid := subsonic.ParseID(id)
		err = s.tryWithFallback(ctx, func(baseURL string) error {
			urlStr := fmt.Sprintf("%s/playlist/?id=%s", baseURL, uuid)
			req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
			req.Header.Set("User-Agent", UserAgent)
			resp, err2 := s.client.Do(req)
			if err2 != nil || resp.StatusCode != http.StatusOK {
				if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
					return fmt.Errorf("HTTP 429")
				}
				return fmt.Errorf("failed to fetch playlist cover info")
			}
			defer resp.Body.Close()

			var result struct {
				Playlist struct {
					SquareImage string `json:"squareImage"`
				} `json:"playlist"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return err
			}

			if result.Playlist.SquareImage == "" {
				return fmt.Errorf("no cover art for playlist")
			}
			imgUuid := strings.ToLower(strings.ReplaceAll(result.Playlist.SquareImage, "-", "/"))
			coverURL = fmt.Sprintf("https://resources.tidal.com/images/%s/320x320.jpg", imgUuid)
			return nil
		})
	} else {
		return "", fmt.Errorf("unsupported type for cover")
	}

	if coverURL != "" {
		s.redis.Set(ctx, cacheKey, coverURL, 7*24*time.Hour)
	}

	return coverURL, err
}

func (s *SquidService) GetSimilarArtists(ctx context.Context, id string) ([]subsonic.Artist, error) {
	_, _, _, numericID := subsonic.ParseID(id)
	var artists []subsonic.Artist
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/artist/similar/?id=%s", baseURL, numericID)

		req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := s.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("HTTP 429")
			}
			return fmt.Errorf("failed to fetch similar artists")
		}
		defer resp.Body.Close()

		var result struct {
			Artists []struct {
				ID      int64  `json:"id"`
				Name    string `json:"name"`
				Picture string `json:"picture"`
			} `json:"artists"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}

		artists = []subsonic.Artist{}
		for _, item := range result.Artists {
			artists = append(artists, subsonic.Artist{
				ID:       subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.ID)),
				Name:     item.Name,
				CoverArt: subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.ID)),
			})
		}
		return nil
	})

	if err != nil {
		return []subsonic.Artist{}, nil
	}
	return artists, nil
}

func (s *SquidService) GetTopSongsByArtist(ctx context.Context, artistName string, count int) ([]subsonic.Song, error) {
	// We use the search endpoint to get popular tracks for the artist
	res, err := s.Search(ctx, artistName)
	if err != nil {
		return nil, err
	}

	// Filter songs to match the artist exactly if possible, or just return first N
	var topSongs []subsonic.Song
	for _, song := range res.Song {
		if strings.EqualFold(song.Artist, artistName) {
			topSongs = append(topSongs, song)
		}
		if len(topSongs) >= count {
			break
		}
	}

	// Fallback: if no exact matches, just take the first few
	if len(topSongs) == 0 && len(res.Song) > 0 {
		limit := count
		if len(res.Song) < limit {
			limit = len(res.Song)
		}
		topSongs = res.Song[:limit]
	}

	return topSongs, nil
}
