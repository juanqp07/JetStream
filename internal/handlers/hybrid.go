package handlers

import (
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"strings"

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
			Version: "1.16.2",
			Album: &subsonic.AlbumWithSongs{
				Album: *album,
				Song:  songs,
			},
		}

		SendSubsonicResponse(c, resp)
		return
	}

	// Default: Navidrome
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetArtist(c *gin.Context) {
	id := c.Request.FormValue("id")
	if strings.HasPrefix(id, "ext-") {
		artist, albums, err := h.squidService.GetArtist(id)
		if err != nil {
			log.Printf("[Metadata] GetArtist error for %s: %v", id, err)
			SendSubsonicError(c, ErrArtistNotFound, err.Error())
			return
		}

		resp := subsonic.Response{
			Status:  "ok",
			Version: "1.16.2",
			Artist: &subsonic.ArtistWithAlbums{
				Artist: *artist,
				Album:  albums,
			},
		}

		SendSubsonicResponse(c, resp)
		return
	}
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
			Version: "1.16.2",
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
	// Squid doesn't have a "list all" for guest, so we return empty or proxy
	// Navidrome will show its own playlists.
	h.proxyHandler.Handle(c)
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

		// Fetch and Proxy
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("[Metadata] Failed to fetch cover from %s: %v", url, err)
			SendSubsonicError(c, ErrGeneric, "Failed to fetch cover")
			return
		}
		defer resp.Body.Close()

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
			Version: "1.16.2",
			Lyrics: &subsonic.Lyrics{
				Value: lyrics,
			},
		})
		return
	}
	h.proxyHandler.Handle(c)
}
