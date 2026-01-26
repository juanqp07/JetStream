package handlers

import (
	"encoding/xml"
	"jetstream/internal/service"
	"jetstream/pkg/subsonic"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bogem/id3v2/v2"
	"github.com/gin-gonic/gin"
)

// SendSubsonicResponse sends a response in either XML or JSON format based on the 'f' query parameter.
func SendSubsonicResponse(c *gin.Context, resp subsonic.Response) {
	format := c.Query("f")
	if format == "json" {
		c.JSON(http.StatusOK, resp)
	} else {
		// Default to XML, which is the standard for Subsonic
		c.XML(http.StatusOK, resp)
	}
}

// SendSubsonicError sends a standardized Subsonic error response.
func SendSubsonicError(c *gin.Context, code int, message string) {
	resp := subsonic.Response{
		Status:  "failed",
		Version: "1.16.2",
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

	log.Printf("[Resolver] [DEBUG] Attempting to resolve Navidrome ID: %s", navidromeID)

	u := proxy.GetTargetURL() + "/rest/getSong.view?id=" + navidromeID + "&" + c.Request.URL.RawQuery
	resp, err := http.Get(u)
	if err != nil {
		log.Printf("[Resolver] [ERROR] Querying Navidrome: %v", err)
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
		log.Printf("[Resolver] [ERROR] Decoding Navidrome response: %v", err)
		return "", false, err
	}

	log.Printf("[Resolver] [DEBUG] Navidrome reported path: %s", result.Song.Path)
	log.Printf("[Resolver] [DEBUG] Navidrome reported metadata: %s - %s", result.Song.Artist, result.Song.Title)

	if result.Song.Path == "" {
		return navidromeID, false, nil
	}

	// 1. Try Path-based Resolution first (Fastest)
	match := idInPathRegex.FindStringSubmatch(result.Song.Path)
	if len(match) > 1 {
		log.Printf("[Resolver] [SUCCESS] Resolved %s -> %s (from path)", navidromeID, match[1])
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
				log.Printf("[Resolver] [DEBUG] File %s is small (%d bytes). Treating as virtual/ghost.", fullPath, info.Size())
			}

			// Check tags regardless of size if it's a regular file
			tag, err := id3v2.Open(fullPath, id3v2.Options{Parse: true})
			if err == nil {
				defer tag.Close()
				frames := tag.GetFrames(tag.CommonID("User defined text information"))
				for _, f := range frames {
					utcf, ok := f.(id3v2.UserDefinedTextFrame)
					if ok && utcf.Description == "TIDAL_ID" {
						log.Printf("[Resolver] [SUCCESS] Resolved %s -> %s (from ID3 tag)", navidromeID, utcf.Value)
						return utcf.Value, true, nil
					}
				}
			} else {
				log.Printf("[Resolver] [DEBUG] Could not read ID3 tags for %s: %v", fullPath, err)
			}
		}
	} else if os.IsNotExist(err) {
		log.Printf("[Resolver] [DEBUG] File not found on disk: %s. Treating as virtual.", fullPath)
		isGhost = true
	}

	// 3. Robust Metadata Search Fallback (Self-Healing)
	if isGhost && result.Song.Artist != "" && result.Song.Title != "" {
		log.Printf("[Resolver] [FALLBACK] Performing search lookup for: %s - %s", result.Song.Artist, result.Song.Title)
		resolvedID, err := squid.SearchOne(result.Song.Artist, result.Song.Title)
		if err == nil {
			log.Printf("[Resolver] [SUCCESS] Self-healed %s -> %s via metadata search", navidromeID, resolvedID)
			return resolvedID, true, nil
		}
		log.Printf("[Resolver] [ERROR] Fallback search failed for %s - %s: %v", result.Song.Artist, result.Song.Title, err)
	}

	log.Printf("[Resolver] [FAIL] Could not resolve %s to external ID", navidromeID)
	return navidromeID, false, nil
}

// Subsonic Error Codes
const (
	ErrGeneric           = 0
	ErrRequiredParameter = 10
	ErrClientVersionOld  = 20
	ErrServerVersionOld  = 30
	ErrWrongUserPass     = 40
	ErrNotAuthorized     = 50
	ErrTrialExpired      = 60
	ErrDataNotFound      = 70
	ErrUserNotAuthorized = 80
	ErrArtistNotFound    = 70 // Re-using 70
)
