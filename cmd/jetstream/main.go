package main

import (
	"log"
	"jetstream/internal/config"
	"jetstream/internal/handlers"
	"jetstream/internal/service"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Load Config
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: Could not load config: %v", err)
	}

	// DEBUG: Audit /music directory on startup
	files, err := os.ReadDir("/music")
	if err != nil {
		log.Printf("ERROR: Reading /music: %v", err)
	} else {
		log.Printf("Checking /music content (%d items found):", len(files))
		for _, f := range files {
			log.Printf(" - %s", f.Name())
		}
	}

	// 2. Initialize Services
	squidService := service.NewSquidService(cfg)
	proxyHandler := handlers.NewProxyHandler(cfg)
	searchHandler := handlers.NewSearchHandler(squidService, cfg)
	syncService := service.NewSyncService(squidService, cfg)
	metadataHandler := handlers.NewMetadataHandler(squidService, syncService, proxyHandler)
	handler := handlers.NewHandler(squidService, syncService, proxyHandler)

	// 3. Setup Router
	r := gin.Default()

	// Trust all proxies (User is running in Docker/Reverse Proxy setup likely)
	// Or specifically trust the docker network. For now, trusting all to suppress warning/work.
	// In production, this should be specific.
	r.SetTrustedProxies(nil)

	// 4. Subsonic API Routes
	// Explicitly map common Subsonic endpoints to Proxy if they don't have custom logic yet.
	// This ensures they appear in the route list and are handled correctly.

	subsonic := r.Group("/rest")
	{
		// System
		subsonic.Any("/ping.view", proxyHandler.Handle)
		subsonic.Any("/ping", proxyHandler.Handle)
		subsonic.Any("/getLicense.view", proxyHandler.Handle)
		subsonic.Any("/getLicense", proxyHandler.Handle)

		// Browsing
		subsonic.Any("/getMusicFolders.view", proxyHandler.Handle)
		subsonic.Any("/getMusicFolders", proxyHandler.Handle)
		subsonic.Any("/getIndexes.view", proxyHandler.Handle)
		subsonic.Any("/getIndexes", proxyHandler.Handle)
		subsonic.Any("/getMusicDirectory.view", proxyHandler.Handle)
		subsonic.Any("/getMusicDirectory", proxyHandler.Handle)
		subsonic.Any("/getGenres.view", proxyHandler.Handle)
		subsonic.Any("/getGenres", proxyHandler.Handle)
		subsonic.Any("/getArtists.view", proxyHandler.Handle)
		subsonic.Any("/getArtists", proxyHandler.Handle)
		subsonic.Any("/getArtist.view", metadataHandler.GetArtist)
		subsonic.Any("/getArtist", metadataHandler.GetArtist)
		subsonic.Any("/getAlbum.view", metadataHandler.GetAlbum)
		subsonic.Any("/getAlbum", metadataHandler.GetAlbum)
		subsonic.Any("/getSong.view", metadataHandler.GetSong)
		subsonic.Any("/getSong", metadataHandler.GetSong)
		subsonic.Any("/getVideos.view", proxyHandler.Handle)
		subsonic.Any("/getVideos", proxyHandler.Handle)

		// Search
		subsonic.Any("/search2.view", proxyHandler.Handle)
		subsonic.Any("/search2", proxyHandler.Handle)
		subsonic.GET("/search3.view", searchHandler.Search3) // Injected
		subsonic.GET("/search3", searchHandler.Search3)

		// Playlists
		subsonic.Any("/createPlaylist.view", proxyHandler.Handle)
		subsonic.Any("/createPlaylist", proxyHandler.Handle)
		subsonic.Any("/updatePlaylist.view", proxyHandler.Handle)
		subsonic.Any("/updatePlaylist", proxyHandler.Handle)
		subsonic.Any("/deletePlaylist.view", proxyHandler.Handle)
		subsonic.Any("/deletePlaylist", proxyHandler.Handle)

		// Custom Playlist Handlers
		subsonic.Any("/getPlaylists.view", metadataHandler.GetPlaylists) // Intercepted
		subsonic.Any("/getPlaylists", metadataHandler.GetPlaylists)
		subsonic.Any("/getPlaylist.view", metadataHandler.GetPlaylist) // Intercepted
		subsonic.Any("/getPlaylist", metadataHandler.GetPlaylist)

		// Media Retrieval
		subsonic.Any("/stream.view", handler.Stream) // Custom Implementation
		subsonic.Any("/stream", handler.Stream)
		subsonic.Any("/download.view", handler.Stream)
		subsonic.Any("/download", handler.Stream)
		subsonic.Any("/getCoverArt.view", metadataHandler.GetCoverArt)
		subsonic.Any("/getCoverArt", metadataHandler.GetCoverArt)

		// Social
		subsonic.Any("/star.view", proxyHandler.Handle) // TODO: Implement Playlist Sync
		subsonic.Any("/star", proxyHandler.Handle)
		subsonic.Any("/unstar.view", proxyHandler.Handle)
		subsonic.Any("/unstar", proxyHandler.Handle)

		// Bookmarks
		subsonic.Any("/getBookmarks.view", proxyHandler.Handle)
		subsonic.Any("/getBookmarks", proxyHandler.Handle)
		subsonic.Any("/createBookmark.view", proxyHandler.Handle)
		subsonic.Any("/createBookmark", proxyHandler.Handle)
		subsonic.Any("/deleteBookmark.view", proxyHandler.Handle)
		subsonic.Any("/deleteBookmark", proxyHandler.Handle)
	}

	// Catch-all for any other Subsonic requests not explicitly listed
	r.NoRoute(proxyHandler.Handle)

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	r.GET("/sync", func(c *gin.Context) {
		id := c.Query("id")
		if id == "" {
			c.JSON(400, gin.H{"error": "id is required"})
			return
		}
		if err := syncService.SyncAlbum(id); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "synced", "id": id})
	})

	log.Printf("Starting JetStream on port %s", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatal(err)
	}
}
