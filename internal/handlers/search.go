package handlers

import (
	"encoding/xml"
	"jetstream/internal/config"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type SearchHandler struct {
	squidService *service.SquidService
	syncService  *service.SyncService
	cfg          *config.Config
	client       *http.Client
	proxyHandler *ProxyHandler
}

func NewSearchHandler(squidService *service.SquidService, syncService *service.SyncService, cfg *config.Config, proxyHandler *ProxyHandler) *SearchHandler {
	return &SearchHandler{
		squidService: squidService,
		syncService:  syncService,
		cfg:          cfg,
		client:       &http.Client{Timeout: 10 * time.Second},
		proxyHandler: proxyHandler,
	}
}

func (h *SearchHandler) Search3(c *gin.Context) {
	query := c.Request.FormValue("query")
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

		// Force XML from Navidrome for parsing consistency
		fURL, _ := url.Parse(h.cfg.NavidromeURL + c.Request.RequestURI)
		q := fURL.Query()
		q.Set("f", "xml")
		fURL.RawQuery = q.Encode()

		req, _ := http.NewRequest("GET", fURL.String(), nil)
		req.Header = c.Request.Header.Clone()
		req.Header.Del("Accept-Encoding") // Let Go's http.Client handle decompression

		resp, err := h.client.Do(req)
		if err != nil {
			log.Printf("[Search] [ERROR] Upstream request failed: %v", err)
			return
		}
		defer resp.Body.Close()

		navidromeResult = &subsonic.Response{}
		if err := xml.NewDecoder(resp.Body).Decode(navidromeResult); err != nil {
			log.Printf("[Search] [ERROR] Decoding Upstream response: %v", err)
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
	} else {
		// Even if Navidrome failed, we might have Squid results, so force OK status
		navidromeResult.Status = "ok"
		navidromeResult.Error = nil
	}

	if navidromeResult.SearchResult3 == nil {
		navidromeResult.SearchResult3 = &subsonic.SearchResult3{}
	}

	if squidResult != nil {
		log.Printf("[Search] Squid returned %d songs, %d albums, %d artists, %d playlists for query '%s'",
			len(squidResult.Song), len(squidResult.Album), len(squidResult.Artist), len(squidResult.Playlist), query)

		// Append Songs
		navidromeResult.SearchResult3.Song = append(navidromeResult.SearchResult3.Song, squidResult.Song...)
		// Append Albums
		navidromeResult.SearchResult3.Album = append(navidromeResult.SearchResult3.Album, squidResult.Album...)
		// Append Artists
		navidromeResult.SearchResult3.Artist = append(navidromeResult.SearchResult3.Artist, squidResult.Artist...)
		// Append Playlists
		navidromeResult.SearchResult3.Playlist = append(navidromeResult.SearchResult3.Playlist, squidResult.Playlist...)

	} else {
		log.Printf("[Search] Squid returned 0 results (or error) for query '%s'", query)
	}

	// 3. Return Response
	SendSubsonicResponse(c, *navidromeResult)
}

func (h *SearchHandler) Search2(c *gin.Context) {
	query := c.Request.FormValue("query")

	// 1. Parallel Requests
	var navidromeResult *subsonic.Response
	var squidResult *subsonic.SearchResult3
	var wg sync.WaitGroup

	wg.Add(2)

	// A. Navidrome (Upstream)
	go func() {
		defer wg.Done()

		// Force XML from Navidrome for parsing consistency
		fURL, _ := url.Parse(h.cfg.NavidromeURL + c.Request.RequestURI)
		q := fURL.Query()
		q.Set("f", "xml")
		fURL.RawQuery = q.Encode()

		req, _ := http.NewRequest("GET", fURL.String(), nil)
		req.Header = c.Request.Header.Clone()
		req.Header.Del("Accept-Encoding")

		resp, err := h.client.Do(req)
		if err != nil {
			log.Printf("[Search2] [ERROR] Upstream request failed: %v", err)
			return
		}
		defer resp.Body.Close()

		navidromeResult = &subsonic.Response{}
		if err := xml.NewDecoder(resp.Body).Decode(navidromeResult); err != nil {
			log.Printf("[Search2] [ERROR] Decoding Upstream response: %v", err)
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
			SearchResult2: &subsonic.SearchResult2{},
		}
	} else {
		// Even if Navidrome failed, we might have Squid results, so force OK status
		navidromeResult.Status = "ok"
		navidromeResult.Error = nil
	}

	if navidromeResult.SearchResult2 == nil {
		navidromeResult.SearchResult2 = &subsonic.SearchResult2{}
	}

	if squidResult != nil {
		// Append Songs
		navidromeResult.SearchResult2.Song = append(navidromeResult.SearchResult2.Song, squidResult.Song...)
		// Append Albums
		navidromeResult.SearchResult2.Album = append(navidromeResult.SearchResult2.Album, squidResult.Album...)
		// Append Artists
		navidromeResult.SearchResult2.Artist = append(navidromeResult.SearchResult2.Artist, squidResult.Artist...)
	}

	// 3. Return Response
	SendSubsonicResponse(c, *navidromeResult)
}

func (h *SearchHandler) Search(c *gin.Context) {
	query := c.Request.FormValue("query")

	// 1. Parallel Requests
	var navidromeResult *subsonic.Response
	var squidResult *subsonic.SearchResult3
	var wg sync.WaitGroup

	wg.Add(2)

	// A. Navidrome (Upstream)
	go func() {
		defer wg.Done()
		fURL, _ := url.Parse(h.cfg.NavidromeURL + c.Request.RequestURI)
		q := fURL.Query()
		q.Set("f", "xml")
		fURL.RawQuery = q.Encode()

		req, _ := http.NewRequest("GET", fURL.String(), nil)
		req.Header = c.Request.Header.Clone()
		req.Header.Del("Accept-Encoding")

		resp, err := h.client.Do(req)
		if err != nil {
			log.Printf("[Search1] [ERROR] Upstream failure: %v", err)
			return
		}
		defer resp.Body.Close()

		navidromeResult = &subsonic.Response{}
		xml.NewDecoder(resp.Body).Decode(navidromeResult)
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
			Status:  "ok",
			Version: "1.16.1",
		}
	} else {
		navidromeResult.Status = "ok"
		navidromeResult.Error = nil
	}

	if navidromeResult.SearchResult == nil {
		navidromeResult.SearchResult = &subsonic.SearchResult{}
	}

	if squidResult != nil {
		// Search1 only has "Match" (songs)
		for _, s := range squidResult.Song {
			navidromeResult.SearchResult.Match = append(navidromeResult.SearchResult.Match, s)
		}
	}

	// 3. Return Response
	SendSubsonicResponse(c, *navidromeResult)
}

func (h *SearchHandler) GetTopSongs(c *gin.Context) {
	// For now, proxy to Navidrome.
	// Future: Fetch top tracks from Squid if artist is external.
	h.proxyHandler.Handle(c)
}

func (h *SearchHandler) GetAlbumList2(c *gin.Context) {
	// Proxy to Navidrome.
	// Future: Inject some "Trending" albums from Tidal.
	h.proxyHandler.Handle(c)
}
