package handlers

import (
	"fmt"
	"io"
	"jetstream/internal/service"
	"log"
	"net/http"
	"os"
	"path/filepath"

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
		SendSubsonicError(c, ErrRequiredParameter, "Missing id parameter")
		return
	}

	// 1. Resolve ID (Handles external IDs and Virtual indexed IDs)
	externalID, isVirtual, err := ResolveVirtualID(c, h.proxyHandler, h.squidService, id)
	if err != nil || !isVirtual {
		log.Printf("[Stream] Not an external or virtual song: %s", id)
		h.proxyHandler.Handle(c)
		return
	}

	log.Printf("[Stream] Handling virtual/external stream: %s (Resolved: %s)", id, externalID)

	// 2. Resolve Metadata (Check Local Library first for real or ghost files)
	song, err := h.squidService.GetSong(externalID)
	if err != nil {
		SendSubsonicError(c, ErrDataNotFound, "Failed to resolve song info: "+err.Error())
		return
	}

	// 3. Local Check (Real or Ghost)
	artistDir := h.syncService.SanitizePath(song.Artist)
	albumDir := h.syncService.SanitizePath(song.Album)

	// New Filename Format: {Track} - [{ID}] {Title}.{ext}
	fileName := fmt.Sprintf("%02d - [%s] %s.%s", song.Track, externalID, h.syncService.SanitizePath(song.Title), h.syncService.GetDownloadFormat())
	localPath := filepath.Join("/music", "raid", artistDir, albumDir, fileName)

	// Ghost file check
	ghostFileName := fmt.Sprintf("%02d - [%s] %s.mp3", song.Track, externalID, h.syncService.SanitizePath(song.Title))
	searchResultPath := filepath.Join(h.syncService.SearchFolder(), artistDir, albumDir, ghostFileName)

	if info, err := os.Stat(localPath); err == nil {
		if info.Size() > 50000 { // Large enough to be real
			log.Printf("[Stream] Serving local file from raid: %s", localPath)
			c.File(localPath)
			return
		}
		log.Printf("[Stream] Small file detected in raid at %s, treating as ghost", localPath)
	} else if info, err := os.Stat(searchResultPath); err == nil && info.Size() < 20000 {
		log.Printf("[Stream] Ghost file detected in search folder: %s", searchResultPath)
	}

	// 4. Fallback: Get Stream URL from Squid Service & Proxy
	trackInfo, err := h.squidService.GetStreamURL(externalID)
	if err != nil {
		SendSubsonicError(c, ErrGeneric, "Failed to resolve stream: "+err.Error())
		return
	}

	// SYNC-ON-PLAY: Trigger background sync for this song
	go func() {
		if err := h.syncService.SyncSong(externalID); err != nil {
			log.Printf("[Stream] Failed to sync song %s: %v", externalID, err)
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
