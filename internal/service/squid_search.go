package service

import (
	"context"
	"encoding/json"
	"fmt"
	"jetstream/pkg/subsonic"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Search performs a search on triton.squid.wtf and maps to Subsonic models
func (s *SquidService) Search(ctx context.Context, query string) (*subsonic.SearchResult3, error) {
	cacheKey := CachePrefix + fmt.Sprintf("search:%s", query)

	// Check Cache
	if val, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
		var res subsonic.SearchResult3
		if err := json.Unmarshal([]byte(val), &res); err == nil {
			return &res, nil
		}
	}

	var (
		songs     []subsonic.Song
		albums    []subsonic.Album
		artists   []subsonic.Artist
		playlists []subsonic.Playlist
		wg        sync.WaitGroup
	)

	wg.Add(4)

	// 1. Search Songs
	go func() {
		defer wg.Done()
		var err error
		songs, err = s.fetchSongs(ctx, query)
		if err != nil {
			slog.Error("Error fetching songs", "error", err, "query", query)
		}
	}()

	// 2. Search Albums
	go func() {
		defer wg.Done()
		var err error
		albums, err = s.fetchAlbums(ctx, query)
		if err != nil {
			slog.Error("Error fetching albums", "error", err, "query", query)
		}
	}()

	// 3. Search Artists
	go func() {
		defer wg.Done()
		var err error
		artists, err = s.fetchArtists(ctx, query)
		if err != nil {
			slog.Error("Error fetching artists", "error", err, "query", query)
		}
	}()

	// 4. Search Playlists
	go func() {
		defer wg.Done()
		var err error
		playlists, err = s.fetchPlaylists(ctx, query)
		if err != nil {
			slog.Error("Error fetching playlists", "error", err, "query", query)
		}
	}()

	wg.Wait()

	res := &subsonic.SearchResult3{
		Song:     songs,
		Album:    albums,
		Artist:   artists,
		Playlist: playlists,
	}

	if data, err := json.Marshal(res); err == nil {
		s.redis.Set(ctx, cacheKey, data, 48*time.Hour)
	}

	return res, nil
}

// SearchOne attempts to find a single song ID matching the artist and title.
func (s *SquidService) SearchOne(ctx context.Context, artist, title string) (string, error) {
	query := fmt.Sprintf("%s %s", artist, title)
	res, err := s.Search(ctx, query)
	if err != nil {
		return "", err
	}

	if len(res.Song) == 0 {
		return "", fmt.Errorf("no matches found")
	}

	return res.Song[0].ID, nil
}

// SearchOneArtist attempts to find a single artist ID matching the name.
func (s *SquidService) SearchOneArtist(ctx context.Context, name string) (string, error) {
	res, err := s.Search(ctx, name)
	if err != nil {
		return "", err
	}

	if len(res.Artist) == 0 {
		return "", fmt.Errorf("no matches found")
	}

	return res.Artist[0].ID, nil
}

// SearchOneAlbum attempts to find a single album ID matching the artist and title.
func (s *SquidService) SearchOneAlbum(ctx context.Context, artist, title string) (string, error) {
	query := fmt.Sprintf("%s %s", artist, title)
	res, err := s.Search(ctx, query)
	if err != nil {
		return "", err
	}

	if len(res.Album) == 0 {
		return "", fmt.Errorf("no matches found")
	}

	return res.Album[0].ID, nil
}

func (s *SquidService) fetchSongs(ctx context.Context, query string) ([]subsonic.Song, error) {
	var songs []subsonic.Song
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/search/?s=%s", baseURL, url.QueryEscape(query))
		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", UserAgent)

		resp, err := s.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("HTTP 429")
			}
			return fmt.Errorf("failed to fetch songs")
		}
		defer resp.Body.Close()

		var result struct {
			Data struct {
				Items []struct {
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
				} `json:"items"`
				Tracks struct {
					Items []struct {
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
					} `json:"items"`
				} `json:"tracks"`
				Songs struct {
					Items []struct {
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
					} `json:"items"`
				} `json:"songs"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}

		items := result.Data.Items
		if len(items) == 0 && len(result.Data.Tracks.Items) > 0 {
			items = result.Data.Tracks.Items
		}
		if len(items) == 0 && len(result.Data.Songs.Items) > 0 {
			items = result.Data.Songs.Items
		}

		songs = []subsonic.Song{}
		for i, item := range items {
			if s.cfg.SearchLimit > 0 && i >= s.cfg.SearchLimit {
				break
			}
			songs = append(songs, subsonic.Song{
				ID:          subsonic.BuildID("squidwtf", "song", fmt.Sprintf("%d", item.ID)),
				Parent:      subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
				Title:       item.Title,
				Album:       item.Album.Title,
				AlbumID:     subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
				Artist:      item.Artist.Name,
				ArtistID:    subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.Artist.ID)),
				CoverArt:    subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.Album.ID)),
				Duration:    item.Duration,
				Track:       item.TrackNumber,
				BitRate:     320,
				Suffix:      "mp3",
				ContentType: "audio/mpeg",
				IsDir:       false,
				IsVideo:     false,
				Path:        fmt.Sprintf("squidwtf/%s/%s/%d.mp3", item.Artist.Name, item.Album.Title, item.ID),
			})
		}
		return nil
	})

	return songs, err
}

func (s *SquidService) fetchAlbums(ctx context.Context, query string) ([]subsonic.Album, error) {
	var albums []subsonic.Album
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/search/?al=%s", baseURL, url.QueryEscape(query))
		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", UserAgent)

		resp, err := s.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("HTTP 429")
			}
			return fmt.Errorf("failed to fetch albums")
		}
		defer resp.Body.Close()

		var result struct {
			Data struct {
				Albums struct {
					Items []struct {
						ID          int64  `json:"id"`
						Title       string `json:"title"`
						ReleaseDate string `json:"releaseDate"`
						Artists     []struct {
							ID   int64  `json:"id"`
							Name string `json:"name"`
						} `json:"artists"`
						Cover string `json:"cover"`
					} `json:"items"`
				} `json:"albums"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}

		albums = []subsonic.Album{}
		for i, item := range result.Data.Albums.Items {
			if s.cfg.SearchLimit > 0 && i >= s.cfg.SearchLimit {
				break
			}
			year := 0
			if len(item.ReleaseDate) >= 4 {
				fmt.Sscanf(item.ReleaseDate, "%d", &year)
			}

			artistName := ""
			artistID := int64(0)
			if len(item.Artists) > 0 {
				artistName = item.Artists[0].Name
				artistID = item.Artists[0].ID
			}

			albums = append(albums, subsonic.Album{
				ID:       subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.ID)),
				Title:    item.Title,
				Name:     item.Title,
				Artist:   artistName,
				ArtistID: subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", artistID)),
				Year:     year,
				CoverArt: subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.ID)),
				IsDir:    true,
			})
		}
		return nil
	})
	return albums, err
}

func (s *SquidService) fetchArtists(ctx context.Context, query string) ([]subsonic.Artist, error) {
	var artists []subsonic.Artist
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/search/?a=%s", baseURL, url.QueryEscape(query))
		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", UserAgent)

		resp, err := s.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("HTTP 429")
			}
			return fmt.Errorf("failed to fetch artists")
		}
		defer resp.Body.Close()

		var result struct {
			Data struct {
				Artists struct {
					Items []struct {
						ID      int64  `json:"id"`
						Name    string `json:"name"`
						Picture string `json:"picture"`
					} `json:"items"`
				} `json:"artists"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}

		artists = []subsonic.Artist{}
		for i, item := range result.Data.Artists.Items {
			if s.cfg.SearchLimit > 0 && i >= s.cfg.SearchLimit {
				break
			}
			artists = append(artists, subsonic.Artist{
				ID:       subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.ID)),
				Name:     item.Name,
				CoverArt: subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.ID)),
			})
		}
		return nil
	})
	return artists, err
}

func (s *SquidService) fetchPlaylists(ctx context.Context, query string) ([]subsonic.Playlist, error) {
	var playlists []subsonic.Playlist
	err := s.tryWithFallback(ctx, func(baseURL string) error {
		urlStr := fmt.Sprintf("%s/search/?p=%s", baseURL, url.QueryEscape(query))
		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", UserAgent)

		resp, err := s.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("HTTP 429")
			}
			return fmt.Errorf("failed to fetch playlists")
		}
		defer resp.Body.Close()

		var result struct {
			Data struct {
				Playlists struct {
					Items []struct {
						UUID           string `json:"uuid"`
						Title          string `json:"title"`
						SquareImage    string `json:"squareImage"`
						NumberOfTracks int    `json:"numberOfTracks"`
						Duration       int    `json:"duration"`
						Created        string `json:"created"`
					} `json:"items"`
				} `json:"playlists"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}

		playlists = []subsonic.Playlist{}
		for i, item := range result.Data.Playlists.Items {
			if s.cfg.SearchLimit > 0 && i >= s.cfg.SearchLimit {
				break
			}
			playlists = append(playlists, subsonic.Playlist{
				ID:        subsonic.BuildID("squidwtf", "playlist", item.UUID),
				Name:      item.Title,
				SongCount: item.NumberOfTracks,
				Duration:  item.Duration,
				Created:   item.Created,
				CoverArt:  subsonic.BuildID("squidwtf", "playlist", item.UUID),
				Owner:     "Tidal",
				Public:    true,
			})
		}
		return nil
	})
	return playlists, err
}
