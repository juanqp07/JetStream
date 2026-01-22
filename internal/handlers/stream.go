package handlers

import (
	"encoding/xml"
	"fmt"
	"io"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bogem/id3v2/v2"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	squidService *service.SquidService
	syncService  *service.SyncService
	proxyHandler *ProxyHandler
}

func NewHandler(squidService *service.SquidService, syncService *service.SyncService, proxyHandler *ProxyHandler) *Handler {
	return &Handler{
		squidService: squidService,
		syncService:  syncService,
		proxyHandler: proxyHandler,
	}
}

// Stream handles /rest/stream and /rest/stream.view
func (h *Handler) Stream(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing id parameter"})
		return
	}

	var err error

	// 1. Parse ID
	isExternal, provider, _, externalID := subsonic.ParseID(id)

	// Fallback to Proxy if not external or not supported provider
	if !isExternal || (provider != "deezer" && provider != "squid" && provider != "squidwtf") {
		// Check if it's a virtual song in Navidrome
		tidalID, isVirtual := h.checkVirtualSong(c, id)
		if isVirtual {
			log.Printf("[Stream] Virtual song detected (Navidrome ID: %s), redirecting to Tidal (Tidal ID: %s)", id, tidalID)
			externalID = tidalID
		} else {
			h.proxyHandler.Handle(c)
			return
		}
	}

	// 2. Resolve Metadata (Check Local Library first for real or ghost files)
	var song *subsonic.Song
	// Note: We don't have artist/album info yet, just ID.
	// If it's a proxy ID (ext-squidwtf-song-...), we might find it in .search still,
	// but we really want library-wide detection.
	// For library-wide detection, Navidrome will have passed us the ID.
	// If it's a Navidrome ID, checkVirtualSong already handles it and gives us the externalID.

	// Fallback to Squid API for full metadata (required for local path construction)
	song, err = h.squidService.GetSong(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve song info: " + err.Error()})
		return
	}

	// 3. Local Check (Real or Ghost)
	artistDir := h.syncService.SanitizePath(song.Artist)
	albumDir := h.syncService.SanitizePath(song.Album)
	fileName := fmt.Sprintf("%02d - %s.%s", song.Track, h.syncService.SanitizePath(song.Title), h.syncService.GetDownloadFormat())
	localPath := filepath.Join("/music", artistDir, albumDir, fileName)

	// Check Search Results specifically for ghosts
	searchResultPath := filepath.Join("/music", "Search Results", artistDir, albumDir, fmt.Sprintf("%02d - %s.mp3", song.Track, h.syncService.SanitizePath(song.Title)))

	if info, err := os.Stat(localPath); err == nil {
		if info.Size() > 50000 { // Large enough to be real
			log.Printf("[Stream] Serving local file: %s", localPath)
			c.File(localPath)
			return
		}
		log.Printf("[Stream] Small file detected at %s, treating as ghost", localPath)
	} else if info, err := os.Stat(searchResultPath); err == nil && info.Size() < 20000 {
		log.Printf("[Stream] Ghost file detected in Search Results: %s", searchResultPath)
	}

	// 4. Fallback: Get Stream URL from Squid Service & Proxy
	trackInfo, err := h.squidService.GetStreamURL(externalID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve stream: " + err.Error()})
		return
	}

	// SYNC-ON-PLAY: Trigger background sync for this song
	go func() {
		// Use 'id' (full format) instead of 'externalID' (numeric) because GetSong expects full ID
		if err := h.syncService.SyncSong(id); err != nil {
			log.Printf("[Stream] Failed to sync song %s: %v", id, err)
		}
	}()

	// 3. Proxy the Stream
	// We need to request the actual file from the CDN
	req, err := http.NewRequest("GET", trackInfo.DownloadURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upstream request"})
		return
	}

	// Pass range header if present for seeking support
	if rangeHeader := c.GetHeader("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	client := &http.Client{} // Use default client or one with timeouts
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to connect to upstream CDN"})
		return
	}
	defer resp.Body.Close()

	// 4. Copy Headers
	c.Header("Content-Type", trackInfo.MimeType)
	if resp.ContentLength > 0 {
		c.Header("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
	}
	c.Header("Accept-Ranges", "bytes") // Critical for scrubbing

	// Copy other relevant headers like Content-Range if it exists (for Partial Content)
	if contentRange := resp.Header.Get("Content-Range"); contentRange != "" {
		c.Header("Content-Range", contentRange)
		c.Status(http.StatusPartialContent)
	} else {
		c.Status(http.StatusOK)
	}

	// Support Download
	if filepath.Base(c.Request.URL.Path) == "download.view" || filepath.Base(c.Request.URL.Path) == "download" {
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.%s\"", externalID, "mp3")) // Simplified filename
	}

	// 5. Zero-Copy Streaming
	log.Printf("[Stream] Streaming external content: %s (Mime: %s)", externalID, trackInfo.MimeType)
	// io.Copy efficiently copies from Reader to Writer
	_, err = io.Copy(c.Writer, resp.Body)
	if err != nil {
		// Connection might be broken, log it but can't really change status now
		log.Printf("[Stream] Error streaming content: %v", err)
	}
}

func (h *Handler) checkVirtualSong(c *gin.Context, navidromeID string) (string, bool) {
	// 1. Get Song Info from Navidrome
	u := h.proxyHandler.GetTargetURL() + "/rest/getSong.view?" + c.Request.URL.RawQuery

	resp, err := http.Get(u)
	if err != nil {
		log.Printf("[checkVirtualSong] Error getting song info from Navidrome: %v", err)
		return "", false
	}
	defer resp.Body.Close()

	var result struct {
		XMLName xml.Name `xml:"subsonic-response"`
		Song    struct {
			Path string `xml:"path,attr"`
		} `xml:"song"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[checkVirtualSong] Error decoding XML: %v", err)
		return "", false
	}
	log.Printf("[checkVirtualSong] Navidrome Path: %s", result.Song.Path)

	// 2. Check if Path is local and read tags
	fullPath := result.Song.Path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join("/music", result.Song.Path)
	}

	if !h.isVirtualFile(fullPath) {
		log.Printf("[checkVirtualSong] Not a virtual file (size check failed): %s", fullPath)
		return "", false
	}

	// 3. Read Tidal ID from ID3 tag
	tag, err := id3v2.Open(fullPath, id3v2.Options{Parse: true})
	if err != nil {
		log.Printf("[checkVirtualSong] Error opening ID3: %v", err)
		return "", false
	}
	defer tag.Close()

	// Look for TIDAL_ID in User-defined text frames
	frames := tag.GetFrames(tag.CommonID("User defined text information"))
	for _, f := range frames {
		utcf, ok := f.(id3v2.UserDefinedTextFrame)
		if ok && utcf.Description == "TIDAL_ID" {
			return utcf.Value, true
		}
	}
	log.Printf("[checkVirtualSong] TIDAL_ID tag not found in %s", fullPath)

	return "", false
}

func (h *Handler) isVirtualFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	// Virtual files are approx 1KB to 2KB
	return info.Size() < 10000
}
