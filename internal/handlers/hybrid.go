package handlers

import (
	"encoding/xml"
	"log"
	"net/http"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
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
	id := c.Query("id")
	log.Printf("[Metadata] GetAlbum request for ID: %s", id)
	if strings.HasPrefix(id, "ext-") {
		// Virtual Album from Squid
		log.Printf("[Metadata] Fetching external album info from Squid: %s", id)

		album, songs, err := h.squidService.GetAlbum(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// We need a specific struct for GetAlbum response if simple re-use isn't easy
		// Let's manually construct/wrap for now or assume Response struct has what we need
		// subsonic.Response defined earlier might need an Album field

		// Let's implement dynamic XML temporarily to match Subsonic protocol
		// <subsonic-response status="ok" version="1.16.1"><album id="..." ...><song .../></album></subsonic-response>

		type AlbumWithSongs struct {
			subsonic.Album
			Song []subsonic.Song `xml:"song"`
		}

		fullAlbum := AlbumWithSongs{
			Album: *album,
			Song:  songs,
		}

		type AlbumResponse struct {
			XMLName xml.Name       `xml:"subsonic-response"`
			Status  string         `xml:"status,attr"`
			Version string         `xml:"version,attr"`
			Album   AlbumWithSongs `xml:"album"`
		}

		finalResp := AlbumResponse{
			Status:  "ok",
			Version: "1.16.1",
			Album:   fullAlbum,
		}

		c.XML(http.StatusOK, finalResp)
		return
	}

	// Default: Navidrome
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetArtist(c *gin.Context) {
	id := c.Query("id")
	if strings.HasPrefix(id, "ext-") {
		artist, albums, err := h.squidService.GetArtist(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		type ArtistWithAlbums struct {
			subsonic.Artist
			Album []subsonic.Album `xml:"album"`
		}

		fullArtist := ArtistWithAlbums{
			Artist: *artist,
			Album:  albums,
		}

		type ArtistResponse struct {
			XMLName xml.Name         `xml:"subsonic-response"`
			Status  string           `xml:"status,attr"`
			Version string           `xml:"version,attr"`
			Artist  ArtistWithAlbums `xml:"artist"`
		}

		c.XML(http.StatusOK, ArtistResponse{
			Status:  "ok",
			Version: "1.16.1",
			Artist:  fullArtist,
		})
		return
	}
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetSong(c *gin.Context) {
	id := c.Query("id")
	if strings.HasPrefix(id, "ext-") {
		song, err := h.squidService.GetSong(id)
		if err != nil {
			// Handle error
		}

		type SongResponse struct {
			XMLName xml.Name      `xml:"subsonic-response"`
			Status  string        `xml:"status,attr"`
			Version string        `xml:"version,attr"`
			Song    subsonic.Song `xml:"song"`
		}

		c.XML(http.StatusOK, SongResponse{
			Status:  "ok",
			Version: "1.16.1",
			Song:    *song,
		})
		return
	}
	h.proxyHandler.Handle(c)
}

func (h *MetadataHandler) GetPlaylist(c *gin.Context) {
	id := c.Query("id")
	if strings.HasPrefix(id, "ext-") {
		playlist, songs, err := h.squidService.GetPlaylist(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		type PlaylistResponse struct {
			XMLName  xml.Name          `xml:"subsonic-response"`
			Status   string            `xml:"status,attr"`
			Version  string            `xml:"version,attr"`
			Playlist subsonic.Playlist `xml:"playlist"`
		}

		// Map songs to entries
		playlist.Entry = songs

		c.XML(http.StatusOK, PlaylistResponse{
			Status:   "ok",
			Version:  "1.16.1",
			Playlist: *playlist,
		})
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
	id := c.Query("id")
	if strings.HasPrefix(id, "ext-") {
		url, err := h.squidService.GetCoverURL(id)
		if err != nil {
			log.Printf("[Metadata] Cover not found for %s: %v", id, err)
			c.JSON(http.StatusNotFound, gin.H{"error": "Cover not found"})
			return
		}

		// Fetch and Proxy
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("[Metadata] Failed to fetch cover from %s: %v", url, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to fetch cover"})
			return
		}
		defer resp.Body.Close()

		log.Printf("[Metadata] Proxying cover from %s (Size: %d, Type: %s)", url, resp.ContentLength, resp.Header.Get("Content-Type"))
		c.DataFromReader(http.StatusOK, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
		return
	}
	h.proxyHandler.Handle(c)
}
