package service

import (
	"context"
	"encoding/json"
	"fmt"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Search performs a search on triton.squid.wtf and maps to Subsonic models
func (s *SquidService) Search(query string) (*subsonic.SearchResult3, error) {
	ctx := context.Background()
	cacheKey := fmt.Sprintf("search:%s", query)

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
		songURL := fmt.Sprintf("%s/search/?s=%s", s.getCurrentURL(), url.QueryEscape(query))
		var err error
		songs, err = s.fetchSongs(songURL)
		if err != nil {
			log.Printf("[Search] Error fetching songs: %v", err)
		}
	}()

	// 2. Search Albums
	go func() {
		defer wg.Done()
		albumURL := fmt.Sprintf("%s/search/?al=%s", s.getCurrentURL(), url.QueryEscape(query))
		var err error
		albums, err = s.fetchAlbums(albumURL)
		if err != nil {
			log.Printf("[Search] Error fetching albums: %v", err)
		}
	}()

	// 3. Search Artists
	go func() {
		defer wg.Done()
		artistURL := fmt.Sprintf("%s/search/?a=%s", s.getCurrentURL(), url.QueryEscape(query))
		var err error
		artists, err = s.fetchArtists(artistURL)
		if err != nil {
			log.Printf("[Search] Error fetching artists: %v", err)
		}
	}()

	// 4. Search Playlists
	go func() {
		defer wg.Done()
		playlistURL := fmt.Sprintf("%s/search/?p=%s", s.getCurrentURL(), url.QueryEscape(query))
		var err error
		playlists, err = s.fetchPlaylists(playlistURL)
		if err != nil {
			log.Printf("[Search] Error fetching playlists: %v", err)
		}
	}()

	wg.Wait()

	res := &subsonic.SearchResult3{
		Song:     songs,
		Album:    albums,
		Artist:   artists,
		Playlist: playlists,
	}

	// Cache Result
	if data, err := json.Marshal(res); err == nil {
		s.redis.Set(ctx, cacheKey, data, 24*time.Hour)
	}

	return res, nil
}

// SearchOne attempts to find a single song ID matching the artist and title.
func (s *SquidService) SearchOne(artist, title string) (string, error) {
	query := fmt.Sprintf("%s %s", artist, title)
	res, err := s.Search(query)
	if err != nil {
		return "", err
	}

	if len(res.Song) == 0 {
		return "", fmt.Errorf("no matches found")
	}

	// Try to find a good match based on title/artist similarity
	// For now, just take the first one
	return res.Song[0].ID, nil
}

// SearchOneArtist attempts to find a single artist ID matching the name.
func (s *SquidService) SearchOneArtist(name string) (string, error) {
	res, err := s.Search(name)
	if err != nil {
		return "", err
	}

	if len(res.Artist) == 0 {
		return "", fmt.Errorf("no matches found")
	}

	// For now, just take the first one
	return res.Artist[0].ID, nil
}

// SearchOneAlbum attempts to find a single album ID matching the artist and title.
func (s *SquidService) SearchOneAlbum(artist, title string) (string, error) {
	query := fmt.Sprintf("%s %s", artist, title)
	res, err := s.Search(query)
	if err != nil {
		return "", err
	}

	if len(res.Album) == 0 {
		return "", fmt.Errorf("no matches found")
	}

	// For now, just take the first one
	return res.Album[0].ID, nil
}

func (s *SquidService) fetchSongs(urlStr string) ([]subsonic.Song, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// ... rest of the logic remains same

	if resp.StatusCode != http.StatusOK {
		return []subsonic.Song{}, nil
	}

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
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var songs []subsonic.Song
	for i, item := range result.Data.Items {
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
		})
	}
	return songs, nil
}

func (s *SquidService) fetchAlbums(urlStr string) ([]subsonic.Album, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return []subsonic.Album{}, nil
	}

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
		return nil, err
	}

	var albums []subsonic.Album
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
		})
	}
	return albums, nil
}

func (s *SquidService) fetchArtists(urlStr string) ([]subsonic.Artist, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return []subsonic.Artist{}, nil
	}

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
		return nil, err
	}

	var artists []subsonic.Artist
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
	return artists, nil
}
func (s *SquidService) fetchPlaylists(urlStr string) ([]subsonic.Playlist, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return []subsonic.Playlist{}, nil
	}

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
		return nil, err
	}

	var playlists []subsonic.Playlist
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
	return playlists, nil
}
