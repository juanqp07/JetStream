package handlers

import (
	"encoding/xml"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type MetadataHandler struct {
	squidService *service.SquidService
	syncService  *service.SyncService
	proxyHandler *ProxyHandler // Fallback
}

func NewMetadataHandler(squidService *service.SquidService, syncService *service.SyncService, proxyHandler *ProxyHandler) *MetadataHandler {
	return &MetadataHandler{
		squidService: squidService,
		syncService:  syncService,
		proxyHandler: proxyHandler,
	}
}

func (h *MetadataHandler) GetAlbum(c *gin.Context) {
	id := c.Request.FormValue("id")
	log.Printf("[Metadata] GetAlbum request for ID: %s", id)

	// 1. Check if it's already an external ID (from search results)
	if strings.HasPrefix(id, "ext-") {
		log.Printf("[Metadata] Fetching external album info from Squid: %s", id)
		album, songs, err := h.squidService.GetAlbum(id)
		if err != nil {
			log.Printf("[Metadata] GetAlbum error for %s: %v", id, err)
			SendSubsonicError(c, ErrGeneric, err.Error())
			return
		}
		resp := subsonic.Response{
			Status:  "ok",
			Version: "1.16.1",
			Album: &subsonic.AlbumWithSongs{
				Album: *album,
				Song:  songs,
			},
		}
		SendSubsonicResponse(c, resp)
		return
	}

	// 2. Try to resolve local ID to external ID (enrichment)
	resolvedID, _, err := ResolveVirtualAlbumID(c, h.proxyHandler, h.squidService, id)
	if err == nil && resolvedID != id {
		log.Printf("[Metadata] Resolved local Album ID %s to external ID: %s", id, resolvedID)
		album, songs, err := h.squidService.GetAlbum(resolvedID)
		if err == nil {
			resp := subsonic.Response{
				Status:  "ok",
				Version: "1.16.1",
				Album: &subsonic.AlbumWithSongs{
					Album: *album,
					Song:  songs,
				},
			}
			SendSubsonicResponse(c, resp)
			return
		}
	}

	// 3. Default: Navidrome
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetArtist(c *gin.Context) {
	id := c.Request.FormValue("id")

	// 1. Check if it's already an external ID (from search results)
	if strings.HasPrefix(id, "ext-") {
		log.Printf("[Metadata] Fetching external artist info from Squid: %s", id)
		artist, albums, err := h.squidService.GetArtist(id)
		if err != nil {
			log.Printf("[Metadata] GetArtist error for %s: %v", id, err)
			SendSubsonicError(c, ErrArtistNotFound, err.Error())
			return
		}
		resp := subsonic.Response{
			Status:  "ok",
			Version: "1.16.1",
			Artist: &subsonic.ArtistWithAlbums{
				Artist: *artist,
				Album:  albums,
			},
		}
		SendSubsonicResponse(c, resp)
		return
	}

	// 2. Try to resolve local ID to external ID (enrichment)
	resolvedID, _, err := ResolveVirtualArtistID(c, h.proxyHandler, h.squidService, id)
	if err == nil && resolvedID != id {
		log.Printf("[Metadata] Resolved local Artist ID %s to external ID: %s", id, resolvedID)
		artist, albums, err := h.squidService.GetArtist(resolvedID)
		if err == nil {
			resp := subsonic.Response{
				Status:  "ok",
				Version: "1.16.1",
				Artist: &subsonic.ArtistWithAlbums{
					Artist: *artist,
					Album:  albums,
				},
			}
			SendSubsonicResponse(c, resp)
			return
		}
	}

	// 3. Default: Navidrome
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetSong(c *gin.Context) {
	id := c.Request.FormValue("id")
	resolvedID, isVirtual, err := ResolveVirtualID(c, h.proxyHandler, h.squidService, id)
	if err == nil && isVirtual {
		log.Printf("[Metadata] Intercepted virtual song metadata request: %s (Resolved: %s)", id, resolvedID)
		song, err := h.squidService.GetSong(resolvedID)
		if err != nil {
			log.Printf("[Metadata] GetSong error for %s: %v", resolvedID, err)
			SendSubsonicError(c, ErrDataNotFound, "Song not found")
			return
		}

		resp := subsonic.Response{
			Status:  "ok",
			Version: "1.16.1",
			Song:    song,
		}

		SendSubsonicResponse(c, resp)
		return
	}

	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetPlaylist(c *gin.Context) {
	id := c.Request.FormValue("id")
	if strings.HasPrefix(id, "ext-") {
		playlist, songs, err := h.squidService.GetPlaylist(id)
		if err != nil {
			log.Printf("[Metadata] GetPlaylist error for %s: %v", id, err)
			SendSubsonicError(c, ErrGeneric, err.Error())
			return
		}

		// Map songs to entries
		playlist.Entry = songs

		resp := subsonic.Response{
			Status:   "ok",
			Version:  "1.16.1",
			Playlist: playlist,
		}

		SendSubsonicResponse(c, resp)
		return
	}
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetPlaylists(c *gin.Context) {
	// 1. Parallel Requests
	var navidromeResult *subsonic.Response
	var squidPlaylists []subsonic.Playlist
	var wg sync.WaitGroup

	wg.Add(2)

	// A. Navidrome (Upstream)
	go func() {
		defer wg.Done()
		u, _ := url.Parse(h.proxyHandler.GetTargetURL() + "/rest/getPlaylists.view")
		q := c.Request.URL.Query()
		q.Set("f", "xml")
		u.RawQuery = q.Encode()

		req, _ := http.NewRequest("GET", u.String(), nil)
		req.Header = c.Request.Header.Clone()
		req.Header.Del("Accept-Encoding")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		navidromeResult = &subsonic.Response{}
		if err := xml.NewDecoder(resp.Body).Decode(navidromeResult); err != nil {
			log.Printf("[Metadata] [ERROR] Decoding Upstream playlists: %v", err)
		}
	}()

	// B. Squid (External - Featured/Popular)
	go func() {
		defer wg.Done()
		// Since there's no "list all", we show a few featured ones or just leave it
		// For now, let's try a default search for "Featured" to populate some
		res, err := h.squidService.Search("Featured")
		if err == nil && res != nil {
			squidPlaylists = res.Playlist
		}
	}()

	wg.Wait()

	// 2. Merge Results
	if navidromeResult == nil {
		navidromeResult = &subsonic.Response{
			Status:    "ok",
			Version:   "1.16.1",
			Playlists: &subsonic.Playlists{},
		}
	}

	if navidromeResult.Playlists == nil {
		navidromeResult.Playlists = &subsonic.Playlists{}
	}

	// Append external playlists
	navidromeResult.Playlists.Playlist = append(navidromeResult.Playlists.Playlist, squidPlaylists...)

	// 3. Return Response
	SendSubsonicResponse(c, *navidromeResult)
}

func (h *MetadataHandler) GetCoverArt(c *gin.Context) {
	id := c.Request.FormValue("id")
	resolvedID, isVirtual, err := ResolveVirtualID(c, h.proxyHandler, h.squidService, id)

	if err == nil && isVirtual {
		log.Printf("[Metadata] Intercepted virtual cover request: %s (Resolved: %s)", id, resolvedID)
		url, err := h.squidService.GetCoverURL(resolvedID)
		if err != nil {
			log.Printf("[Metadata] Cover not found for %s: %v", resolvedID, err)
			SendSubsonicError(c, ErrDataNotFound, "Cover not found")
			return
		}

		// Fetch and Proxy with proper User-Agent to avoid 403
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", service.UserAgent)
		req.Header.Set("Accept", "image/*,*/*")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[Metadata] Failed to fetch cover from %s: %v", url, err)
			SendSubsonicError(c, ErrGeneric, "Failed to fetch cover")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("[Metadata] Cover server returned %d for %s", resp.StatusCode, url)
			SendSubsonicError(c, ErrDataNotFound, "Cover not found")
			return
		}

		log.Printf("[Metadata] Proxying cover from %s (Size: %d, Type: %s)", url, resp.ContentLength, resp.Header.Get("Content-Type"))
		c.DataFromReader(http.StatusOK, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
		return
	}
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetOpenSubsonicExtensions(c *gin.Context) {
	resp := subsonic.Response{
		Status:  "ok",
		Version: "1.16.1",
		OpenSubsonicExtensions: &subsonic.OpenSubsonicExtensions{
			Extension: []subsonic.OpenSubsonicExtension{
				{Name: "songLyrics", Versions: []string{"1"}},
				{Name: "formPost", Versions: []string{"1"}},
				{Name: "transcoding", Versions: []string{"1"}},
			},
		},
	}

	SendSubsonicResponse(c, resp)
}

func (h *MetadataHandler) GetLyrics(c *gin.Context) {
	// Legacy Subsonic getLyrics.view
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetLyricsBySongId(c *gin.Context) {
	id := c.Request.FormValue("id")
	resolvedID, isVirtual, _ := ResolveVirtualID(c, h.proxyHandler, h.squidService, id)

	if isVirtual {
		lyrics, err := h.squidService.GetLyrics(resolvedID)
		if err != nil {
			log.Printf("[Metadata] Lyrics not found for %s: %v", resolvedID, err)
			SendSubsonicResponse(c, subsonic.Response{
				Status:  "ok",
				Version: "1.16.1",
			})
			return
		}

		SendSubsonicResponse(c, subsonic.Response{
			Status:  "ok",
			Version: "1.16.1",
			Lyrics: &subsonic.Lyrics{
				Value: lyrics,
			},
		})
		return
	}
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetMusicDirectory(c *gin.Context) {
	id := c.Request.FormValue("id")
	if strings.HasPrefix(id, "ext-") {
		log.Printf("[Metadata] GetMusicDirectory for external ID: %s", id)

		if strings.Contains(id, "-artist-") {
			artist, albums, err := h.squidService.GetArtist(id)
			if err != nil {
				SendSubsonicError(c, ErrGeneric, err.Error())
				return
			}
			var children []subsonic.Song
			for _, alb := range albums {
				children = append(children, subsonic.Song{
					ID:       alb.ID,
					Parent:   id,
					Title:    alb.Title,
					IsDir:    true,
					Album:    alb.Title,
					Artist:   alb.Artist,
					CoverArt: alb.CoverArt,
				})
			}
			resp := subsonic.Response{
				Status:  "ok",
				Version: "1.16.1",
				Directory: &subsonic.Directory{
					ID:    id,
					Name:  artist.Name,
					Child: children,
				},
			}
			SendSubsonicResponse(c, resp)
			return
		} else if strings.Contains(id, "-album-") {
			album, songs, err := h.squidService.GetAlbum(id)
			if err != nil {
				SendSubsonicError(c, ErrGeneric, err.Error())
				return
			}
			resp := subsonic.Response{
				Status:  "ok",
				Version: "1.16.1",
				Directory: &subsonic.Directory{
					ID:    id,
					Name:  album.Title,
					Child: songs,
				},
			}
			SendSubsonicResponse(c, resp)
			return
		}
	}

	h.proxyHandler.Handle(c)
}
