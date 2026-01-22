package handlers

import (
	"encoding/xml"
	"jetstream/internal/config"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type SearchHandler struct {
	squidService *service.SquidService
	syncService  *service.SyncService
	cfg          *config.Config
	client       *http.Client
}

func NewSearchHandler(squidService *service.SquidService, syncService *service.SyncService, cfg *config.Config) *SearchHandler {
	return &SearchHandler{
		squidService: squidService,
		syncService:  syncService,
		cfg:          cfg,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (h *SearchHandler) Search3(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		// Fallback to proxy if no query (though usually search has query)
		// Or return empty
	}

	// 1. Parallel Requests
	var navidromeResult *subsonic.Response
	var squidResult *subsonic.SearchResult3
	var wg sync.WaitGroup

	wg.Add(2)

	// A. Navidrome (Upstream)
	go func() {
		defer wg.Done()
		// We act as a client to Navidrome here to parse the response
		// Construct URL
		u := h.cfg.NavidromeURL + c.Request.RequestURI
		req, _ := http.NewRequest("GET", u, nil)

		// Forward Auth params from original request (they are in query)
		// Forward Headers (Auth)
		req.Header = c.Request.Header.Clone()

		resp, err := h.client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		// Parse XML
		navidromeResult = &subsonic.Response{}
		if err := xml.NewDecoder(resp.Body).Decode(navidromeResult); err != nil {
			// Log error
		}
	}()

	// B. Squid (External)
	go func() {
		defer wg.Done()
		res, err := h.squidService.Search(query)
		if err == nil {
			squidResult = res
		}
	}()

	wg.Wait()

	// 2. Merge Results
	if navidromeResult == nil {
		navidromeResult = &subsonic.Response{
			Status:        "ok",
			Version:       "1.16.1",
			SearchResult3: &subsonic.SearchResult3{},
		}
	}

	if navidromeResult.SearchResult3 == nil {
		navidromeResult.SearchResult3 = &subsonic.SearchResult3{}
	}

	if squidResult != nil {
		log.Printf("[Search] Squid returned %d songs, %d albums, %d artists for query '%s'",
			len(squidResult.Song), len(squidResult.Album), len(squidResult.Artist), query)

		// Append Songs
		navidromeResult.SearchResult3.Song = append(navidromeResult.SearchResult3.Song, squidResult.Song...)
		// Append Albums
		navidromeResult.SearchResult3.Album = append(navidromeResult.SearchResult3.Album, squidResult.Album...)
		// Append Artists
		navidromeResult.SearchResult3.Artist = append(navidromeResult.SearchResult3.Artist, squidResult.Artist...)

		// POPULATE GHOST FILES
		go func() {
			if err := h.syncService.ClearSearchCache(); err != nil {
				log.Printf("[Search] Warning: Failed to clear search cache: %v", err)
			}
			for _, song := range squidResult.Song {
				if err := h.syncService.CreateGhostFile(&song); err != nil {
					log.Printf("[Search] Warning: Failed to create ghost file for %s: %v", song.ID, err)
				}
			}
		}()
	} else {
		log.Printf("[Search] Squid returned 0 results (or error) for query '%s'", query)
	}

	// 3. Return Response
	// Force XML for now as it's the default Subsonic format
	c.XML(http.StatusOK, navidromeResult)
}
