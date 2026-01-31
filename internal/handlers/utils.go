package handlers

import (
	"encoding/xml"
	"fmt"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bogem/id3v2/v2"
	"github.com/gin-gonic/gin"
)

// SendSubsonicResponse sends a response in either XML or JSON format based on the 'f' query parameter.
func SendSubsonicResponse(c *gin.Context, resp subsonic.Response) {
	format := c.Query("f")
	if format == "json" {
		c.JSON(http.StatusOK, gin.H{"subsonic-response": resp})
	} else {
		// Default to XML, which is the standard for Subsonic
		c.XML(http.StatusOK, resp)
	}
}

// SendSubsonicError sends a standardized Subsonic error response.
func SendSubsonicError(c *gin.Context, code int, message string) {
	resp := subsonic.Response{
		Status:  subsonic.StatusFailed,
		Version: subsonic.Version,
		Error: &subsonic.Error{
			Code:    code,
			Message: message,
		},
	}
	SendSubsonicResponse(c, resp)
}

var idInPathRegex = regexp.MustCompile(`\[(ext-[^\]]+)\]`)

// ResolveVirtualID attempts to find an external ID (ext-...) for a given Navidrome ID.
func ResolveVirtualID(c *gin.Context, proxy *ProxyHandler, squid *service.SquidService, navidromeID string) (string, bool, error) {
	if strings.HasPrefix(navidromeID, "ext-") {
		return navidromeID, true, nil
	}

	slog.Debug("Attempting to resolve Navidrome ID", "navidromeID", navidromeID)

	// Force XML and let http.Client handle decompression
	parsedURL, _ := url.Parse(proxy.GetTargetURL() + "/rest/getSong.view")
	q := c.Request.URL.Query()
	q.Set("id", navidromeID)
	q.Set("f", "xml")
	parsedURL.RawQuery = q.Encode()

	req, _ := http.NewRequest("GET", parsedURL.String(), nil)
	req.Header = c.Request.Header.Clone()
	req.Header.Del("Accept-Encoding")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("Querying Navidrome", "error", err)
		return "", false, err
	}
	defer resp.Body.Close()

	var result struct {
		XMLName xml.Name `xml:"subsonic-response"`
		Song    struct {
			Path   string `xml:"path,attr"`
			Artist string `xml:"artist,attr"`
			Title  string `xml:"title,attr"`
		} `xml:"song"`
	}

	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Error("Decoding Navidrome response", "error", err)
		return "", false, err
	}

	slog.Debug("Navidrome reported path", "path", result.Song.Path)
	slog.Debug("Navidrome reported metadata", "metadata", fmt.Sprintf("%s - %s", result.Song.Artist, result.Song.Title))

	if result.Song.Path == "" {
		return navidromeID, false, nil
	}

	// 1. Try Path-based Resolution first (Fastest)
	match := idInPathRegex.FindStringSubmatch(result.Song.Path)
	if len(match) > 1 {
		slog.Info("Resolved from path", "id", navidromeID, "resolved", match[1])
		return match[1], true, nil
	}

	// 2. Try ID3 Tag Resolution & Ghost Detection
	fullPath := result.Song.Path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join("/music", result.Song.Path)
	}

	var isGhost bool
	if info, err := os.Stat(fullPath); err == nil {
		if info.Mode().IsRegular() {
			// Ghost check: Dummy files with covers can still be up to 500KB-1MB
			if info.Size() < 1024*1024 { // 1MB threshold
				isGhost = true
				slog.Debug("File is small, treating as virtual/ghost", "path", fullPath, "size", info.Size())
			}

			// Check tags regardless of size if it's a regular file
			tag, err := id3v2.Open(fullPath, id3v2.Options{Parse: true})
			if err == nil {
				defer tag.Close()
				frames := tag.GetFrames(tag.CommonID("User defined text information"))
				for _, f := range frames {
					utcf, ok := f.(id3v2.UserDefinedTextFrame)
					if ok && utcf.Description == "TIDAL_ID" {
						slog.Info("Resolved from ID3 tag", "id", navidromeID, "resolved", utcf.Value)
						return utcf.Value, true, nil
					}

				}
			} else {
				slog.Debug("Could not read ID3 tags", "path", fullPath, "error", err)
			}

		}
	} else if os.IsNotExist(err) {
		slog.Debug("File not found on disk, treating as virtual", "path", fullPath)
		isGhost = true
	}

	// 3. Robust Metadata Search Fallback (Self-Healing)
	if isGhost && result.Song.Artist != "" && result.Song.Title != "" {
		slog.Warn("Performing search lookup", "artist", result.Song.Artist, "title", result.Song.Title)
		resolvedID, err := squid.SearchOne(c.Request.Context(), result.Song.Artist, result.Song.Title)
		if err == nil {
			slog.Info("Self-healed via metadata search", "id", navidromeID, "resolved", resolvedID)
			return resolvedID, true, nil
		}
		slog.Error("Fallback search failed", "artist", result.Song.Artist, "title", result.Song.Title, "error", err)
	}

	log.Printf("[Resolver] [FAIL] Could not resolve %s to external ID", navidromeID)
	return navidromeID, false, nil
}

// ResolveVirtualArtistID attempts to find an external artist ID for a given Navidrome Artist ID.
func ResolveVirtualArtistID(c *gin.Context, proxy *ProxyHandler, squid *service.SquidService, navidromeID string) (string, bool, error) {
	if strings.HasPrefix(navidromeID, "ext-") {
		return navidromeID, true, nil
	}

	slog.Debug("Resolving Artist ID", "id", navidromeID)

	parsedURL, _ := url.Parse(proxy.GetTargetURL() + "/rest/getArtist.view")
	q := c.Request.URL.Query()
	q.Set("id", navidromeID)
	q.Set("f", "xml")
	parsedURL.RawQuery = q.Encode()

	req, _ := http.NewRequest("GET", parsedURL.String(), nil)
	req.Header = c.Request.Header.Clone()
	req.Header.Del("Accept-Encoding")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	var result struct {
		XMLName xml.Name `xml:"subsonic-response"`
		Artist  struct {
			Name string `xml:"name,attr"`
		} `xml:"artist"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false, err
	}

	if result.Artist.Name == "" {
		return navidromeID, false, nil
	}

	resolvedID, err := squid.SearchOneArtist(c.Request.Context(), result.Artist.Name)
	if err == nil {
		slog.Info("Resolved Artist", "id", navidromeID, "resolved", resolvedID, "name", result.Artist.Name)
		return resolvedID, true, nil
	}

	return navidromeID, false, nil
}

// ResolveVirtualAlbumID attempts to find an external album ID for a given Navidrome Album ID.
func ResolveVirtualAlbumID(c *gin.Context, proxy *ProxyHandler, squid *service.SquidService, navidromeID string) (string, bool, error) {
	if strings.HasPrefix(navidromeID, "ext-") {
		return navidromeID, true, nil
	}

	slog.Debug("Resolving Album ID", "id", navidromeID)

	parsedURL, _ := url.Parse(proxy.GetTargetURL() + "/rest/getAlbum.view")
	q := c.Request.URL.Query()
	q.Set("id", navidromeID)
	q.Set("f", "xml")
	parsedURL.RawQuery = q.Encode()

	req, _ := http.NewRequest("GET", parsedURL.String(), nil)
	req.Header = c.Request.Header.Clone()
	req.Header.Del("Accept-Encoding")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	var result struct {
		XMLName xml.Name `xml:"subsonic-response"`
		Album   struct {
			Title  string `xml:"title,attr"`
			Artist string `xml:"artist,attr"`
		} `xml:"album"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false, err
	}

	if result.Album.Title == "" {
		return navidromeID, false, nil
	}

	resolvedID, err := squid.SearchOneAlbum(c.Request.Context(), result.Album.Artist, result.Album.Title)
	if err == nil {
		slog.Info("Resolved Album", "id", navidromeID, "resolved", resolvedID, "artist", result.Album.Artist, "title", result.Album.Title)
		return resolvedID, true, nil
	}

	return navidromeID, false, nil
}
