package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"jetstream/pkg/subsonic"
)

// Search performs a search on triton.squid.wtf and maps to Subsonic models
func (s *SquidService) Search(query string) (*subsonic.SearchResult3, error) {
	// Squid Search URL (mimicking C# logic)
	// C#: $"{BaseUrl}/search/?s={Uri.EscapeDataString(query)}" for songs
	// We will do a combined search or just songs/albums for now to demonstrate.
	// Let's implement Song search first as it's most critical.

	// 1. Search Songs
	// 1. Search Songs
	songURL := fmt.Sprintf("%s/search/?s=%s", s.cfg.SquidURL, url.QueryEscape(query))
	songs, err := s.fetchSongs(songURL)
	if err != nil {
		log.Printf("[Search] Error fetching songs: %v", err)
	}

	// 2. Search Albums
	albumURL := fmt.Sprintf("%s/search/?al=%s", s.cfg.SquidURL, url.QueryEscape(query))
	albums, err := s.fetchAlbums(albumURL)
	if err != nil {
		log.Printf("[Search] Error fetching albums: %v", err)
	}

	// 3. Search Artists
	artistURL := fmt.Sprintf("%s/search/?a=%s", s.cfg.SquidURL, url.QueryEscape(query))
	artists, err := s.fetchArtists(artistURL)
	if err != nil {
		log.Printf("[Search] Error fetching artists: %v", err)
	}

	// 4. Search Playlists
	playlistURL := fmt.Sprintf("%s/search/?p=%s", s.cfg.SquidURL, url.QueryEscape(query))
	playlists, err := s.fetchPlaylists(playlistURL)
	if err != nil {
		log.Printf("[Search] Error fetching playlists: %v", err)
	}

	return &subsonic.SearchResult3{
		Song:     songs,
		Album:    albums,
		Artist:   artists,
		Playlist: playlists,
	}, nil
}

func (s *SquidService) fetchSongs(urlStr string) ([]subsonic.Song, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	// ... (rest of fetchSongs is unchanged, just ensuring context)
	// Headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
					Artist      struct {
						ID   int64  `json:"id"`
						Name string `json:"name"`
					} `json:"artist"`
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

		albums = append(albums, subsonic.Album{
			ID:       subsonic.BuildID("squidwtf", "album", fmt.Sprintf("%d", item.ID)),
			Title:    item.Title,
			Name:     item.Title,
			Artist:   item.Artist.Name,
			ArtistID: subsonic.BuildID("squidwtf", "artist", fmt.Sprintf("%d", item.Artist.ID)),
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
		})
	}
	return playlists, nil
}
