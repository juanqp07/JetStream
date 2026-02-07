package handlers

import (
	"encoding/xml"
	"fmt"
	"jetstream/internal/config"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log/slog"
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
		ls := fmt.Sprintf("%d", h.cfg.SearchLimit)
		if h.cfg.SearchLimit <= 0 {
			ls = "50"
		}
		q.Set("songCount", ls)
		q.Set("albumCount", ls)
		q.Set("artistCount", ls)
		fURL.RawQuery = q.Encode()

		req, _ := http.NewRequest("GET", fURL.String(), nil)
		req.Header = c.Request.Header.Clone()
		req.Header.Del("Accept-Encoding") // Let Go's http.Client handle decompression

		resp, err := h.client.Do(req)
		if err != nil {
			slog.Error("Upstream search request failed", "error", err)
			return
		}

		defer resp.Body.Close()

		navidromeResult = &subsonic.Response{}
		if err := xml.NewDecoder(resp.Body).Decode(navidromeResult); err != nil {
			slog.Error("Decoding Upstream search response", "error", err)
		}

	}()

	// B. Squid (External)
	go func() {
		defer wg.Done()
		res, err := h.squidService.Search(c.Request.Context(), query)
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
		navidromeResult.SearchResult3 = &subsonic.SearchResult3{
			Artist:   []subsonic.Artist{},
			Album:    []subsonic.Album{},
			Song:     []subsonic.Song{},
			Playlist: []subsonic.Playlist{},
		}
	} else {
		if navidromeResult.SearchResult3.Artist == nil {
			navidromeResult.SearchResult3.Artist = []subsonic.Artist{}
		}
		if navidromeResult.SearchResult3.Album == nil {
			navidromeResult.SearchResult3.Album = []subsonic.Album{}
		}
		if navidromeResult.SearchResult3.Song == nil {
			navidromeResult.SearchResult3.Song = []subsonic.Song{}
		}
		if navidromeResult.SearchResult3.Playlist == nil {
			navidromeResult.SearchResult3.Playlist = []subsonic.Playlist{}
		}
	}

	if squidResult != nil {
		slog.Info("Squid search results",
			"songs", len(squidResult.Song),
			"albums", len(squidResult.Album),
			"artists", len(squidResult.Artist),
			"playlists", len(squidResult.Playlist),
			"query", query)

		// Append Songs
		navidromeResult.SearchResult3.Song = append(navidromeResult.SearchResult3.Song, squidResult.Song...)
		// Append Albums
		navidromeResult.SearchResult3.Album = append(navidromeResult.SearchResult3.Album, squidResult.Album...)
		// Append Artists
		navidromeResult.SearchResult3.Artist = append(navidromeResult.SearchResult3.Artist, squidResult.Artist...)
		// Append Playlists
		navidromeResult.SearchResult3.Playlist = append(navidromeResult.SearchResult3.Playlist, squidResult.Playlist...)

	} else {
		slog.Debug("Squid returned 0 results (or error)", "query", query)
	}

	// 3. Return Response & Limit
	limit := h.cfg.SearchLimit
	if limit <= 0 {
		limit = 50
	}
	if navidromeResult.SearchResult3 != nil {
		if len(navidromeResult.SearchResult3.Song) > limit {
			navidromeResult.SearchResult3.Song = navidromeResult.SearchResult3.Song[:limit]
		}
		if len(navidromeResult.SearchResult3.Album) > limit {
			navidromeResult.SearchResult3.Album = navidromeResult.SearchResult3.Album[:limit]
		}
		if len(navidromeResult.SearchResult3.Artist) > limit {
			navidromeResult.SearchResult3.Artist = navidromeResult.SearchResult3.Artist[:limit]
		}
	}

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
		ls := fmt.Sprintf("%d", h.cfg.SearchLimit)
		if h.cfg.SearchLimit <= 0 {
			ls = "50"
		}
		q.Set("songCount", ls)
		q.Set("albumCount", ls)
		q.Set("artistCount", ls)
		fURL.RawQuery = q.Encode()

		req, _ := http.NewRequest("GET", fURL.String(), nil)
		req.Header = c.Request.Header.Clone()
		req.Header.Del("Accept-Encoding")

		resp, err := h.client.Do(req)
		if err != nil {
			slog.Error("Upstream search2 request failed", "error", err)
			return
		}

		defer resp.Body.Close()

		navidromeResult = &subsonic.Response{}
		if err := xml.NewDecoder(resp.Body).Decode(navidromeResult); err != nil {
			slog.Error("Decoding Upstream search2 response", "error", err)
		}

	}()

	// B. Squid (External)
	go func() {
		defer wg.Done()
		res, err := h.squidService.Search(c.Request.Context(), query)
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
		navidromeResult.SearchResult2 = &subsonic.SearchResult2{
			Artist: []subsonic.Artist{},
			Album:  []subsonic.Album{},
			Song:   []subsonic.Song{},
		}
	} else {
		if navidromeResult.SearchResult2.Artist == nil {
			navidromeResult.SearchResult2.Artist = []subsonic.Artist{}
		}
		if navidromeResult.SearchResult2.Album == nil {
			navidromeResult.SearchResult2.Album = []subsonic.Album{}
		}
		if navidromeResult.SearchResult2.Song == nil {
			navidromeResult.SearchResult2.Song = []subsonic.Song{}
		}
	}

	if squidResult != nil {
		// Append Songs
		navidromeResult.SearchResult2.Song = append(navidromeResult.SearchResult2.Song, squidResult.Song...)
		// Append Albums
		navidromeResult.SearchResult2.Album = append(navidromeResult.SearchResult2.Album, squidResult.Album...)
		// Append Artists
		navidromeResult.SearchResult2.Artist = append(navidromeResult.SearchResult2.Artist, squidResult.Artist...)
	}

	// 3. Return Response & Limit
	limit := h.cfg.SearchLimit
	if limit <= 0 {
		limit = 50
	}
	if navidromeResult.SearchResult2 != nil {
		if len(navidromeResult.SearchResult2.Song) > limit {
			navidromeResult.SearchResult2.Song = navidromeResult.SearchResult2.Song[:limit]
		}
		if len(navidromeResult.SearchResult2.Album) > limit {
			navidromeResult.SearchResult2.Album = navidromeResult.SearchResult2.Album[:limit]
		}
		if len(navidromeResult.SearchResult2.Artist) > limit {
			navidromeResult.SearchResult2.Artist = navidromeResult.SearchResult2.Artist[:limit]
		}
	}

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
		ls := fmt.Sprintf("%d", h.cfg.SearchLimit)
		if h.cfg.SearchLimit <= 0 {
			ls = "50"
		}
		q.Set("songCount", ls)
		fURL.RawQuery = q.Encode()

		req, _ := http.NewRequest("GET", fURL.String(), nil)
		req.Header = c.Request.Header.Clone()
		req.Header.Del("Accept-Encoding")

		resp, err := h.client.Do(req)
		if err != nil {
			slog.Error("Upstream search1 request failed", "error", err)
			return
		}

		defer resp.Body.Close()

		navidromeResult = &subsonic.Response{}
		xml.NewDecoder(resp.Body).Decode(navidromeResult)
	}()

	// B. Squid (External)
	go func() {
		defer wg.Done()
		res, err := h.squidService.Search(c.Request.Context(), query)
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
		navidromeResult.SearchResult = &subsonic.SearchResult{
			Match: []subsonic.Song{},
		}
	} else {
		if navidromeResult.SearchResult.Match == nil {
			navidromeResult.SearchResult.Match = []subsonic.Song{}
		}
	}

	if squidResult != nil {
		// Search1 only has "Match" (songs)
		for _, s := range squidResult.Song {
			navidromeResult.SearchResult.Match = append(navidromeResult.SearchResult.Match, s)
		}
	}

	// 3. Return Response & Limit
	limit := h.cfg.SearchLimit
	if limit <= 0 {
		limit = 50
	}
	if navidromeResult.SearchResult != nil {
		if len(navidromeResult.SearchResult.Match) > limit {
			navidromeResult.SearchResult.Match = navidromeResult.SearchResult.Match[:limit]
		}
	}

	SendSubsonicResponse(c, *navidromeResult)
}

func (h *SearchHandler) GetTopSongs(c *gin.Context) {
	artist := c.Request.FormValue("artist")
	countStr := c.Request.FormValue("count")
	count := 20
	if countStr != "" {
		fmt.Sscanf(countStr, "%d", &count)
	}

	if artist != "" {
		slog.Info("Fetching top songs", "artist", artist)
		songs, err := h.squidService.GetTopSongsByArtist(c.Request.Context(), artist, count)

		if err == nil && len(songs) > 0 {
			resp := subsonic.Response{
				Status:  "ok",
				Version: "1.16.1",
				TopSongs: &subsonic.TopSongs{
					Song: songs,
				},
			}
			SendSubsonicResponse(c, resp)
			return
		}
	}

	h.proxyHandler.Handle(c)
}

func (h *SearchHandler) GetAlbumList2(c *gin.Context) {
	listType := c.Request.FormValue("type")

	if listType == "random" {
		// 1. Parallel Requests
		var navidromeResult *subsonic.Response
		var squidAlbums []subsonic.Album
		var wg sync.WaitGroup

		wg.Add(2)

		// A. Navidrome
		go func() {
			defer wg.Done()
			u, _ := url.Parse(h.proxyHandler.GetTargetURL() + "/rest/getAlbumList2.view")
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
			xml.NewDecoder(resp.Body).Decode(navidromeResult)
		}()

		// B. Squid - Search for "Hits" to get some "random" albums
		go func() {
			defer wg.Done()
			res, err := h.squidService.Search(c.Request.Context(), "Hits")
			if err == nil && res != nil {
				squidAlbums = res.Album
			}

		}()

		wg.Wait()

		// 2. Merge
		if navidromeResult == nil {
			navidromeResult = &subsonic.Response{
				Status:     "ok",
				Version:    "1.16.1",
				AlbumList2: &subsonic.AlbumList2{},
			}
		}

		if navidromeResult.AlbumList2 == nil {
			navidromeResult.AlbumList2 = &subsonic.AlbumList2{}
		}

		// Inject external albums
		limit := 10
		if len(squidAlbums) < limit {
			limit = len(squidAlbums)
		}
		navidromeResult.AlbumList2.Album = append(navidromeResult.AlbumList2.Album, squidAlbums[:limit]...)

		SendSubsonicResponse(c, *navidromeResult)
		return
	}

	h.proxyHandler.Handle(c)
}
