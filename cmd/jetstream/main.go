package main

import (
	"context"
	"jetstream/internal/config"
	"jetstream/internal/handlers"
	"jetstream/internal/service"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Load Config
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: Could not load config: %v", err)
	}

	// 2. Initialize Services
	squidService := service.NewSquidService(cfg)
	proxyHandler := handlers.NewProxyHandler(cfg)
	syncService := service.NewSyncService(squidService, cfg)
	searchHandler := handlers.NewSearchHandler(squidService, syncService, cfg)
	metadataHandler := handlers.NewMetadataHandler(squidService, syncService, proxyHandler)
	handler := handlers.NewHandler(squidService, syncService, proxyHandler)

	// 3. Setup Router
	r := gin.Default()
	r.SetTrustedProxies(nil)

	// 4. Subsonic API Routes
	subsonicGroup := r.Group("/rest")
	{
		// System
		subsonicGroup.Any("/ping.view", proxyHandler.Handle)
		subsonicGroup.Any("/ping", proxyHandler.Handle)
		subsonicGroup.Any("/getLicense.view", proxyHandler.Handle)
		subsonicGroup.Any("/getLicense", proxyHandler.Handle)

		// Browsing
		subsonicGroup.Any("/getMusicFolders.view", proxyHandler.Handle)
		subsonicGroup.Any("/getMusicFolders", proxyHandler.Handle)
		subsonicGroup.Any("/getIndexes.view", proxyHandler.Handle)
		subsonicGroup.Any("/getIndexes", proxyHandler.Handle)
		subsonicGroup.Any("/getMusicDirectory.view", proxyHandler.Handle)
		subsonicGroup.Any("/getMusicDirectory", proxyHandler.Handle)
		subsonicGroup.Any("/getGenres.view", proxyHandler.Handle)
		subsonicGroup.Any("/getGenres", proxyHandler.Handle)
		subsonicGroup.Any("/getArtists.view", proxyHandler.Handle)
		subsonicGroup.Any("/getArtists", proxyHandler.Handle)
		subsonicGroup.Any("/getArtist.view", metadataHandler.GetArtist)
		subsonicGroup.Any("/getArtist", metadataHandler.GetArtist)
		subsonicGroup.Any("/getAlbum.view", metadataHandler.GetAlbum)
		subsonicGroup.Any("/getAlbum", metadataHandler.GetAlbum)
		subsonicGroup.Any("/getSong.view", metadataHandler.GetSong)
		subsonicGroup.Any("/getSong", metadataHandler.GetSong)

		// Search
		subsonicGroup.Any("/search.view", searchHandler.Search)
		subsonicGroup.Any("/search", searchHandler.Search)
		subsonicGroup.Any("/search2.view", searchHandler.Search2)
		subsonicGroup.Any("/search3.view", searchHandler.Search3)
		subsonicGroup.Any("/search3", searchHandler.Search3)

		// OpenSubsonic Extensions (Lyrics, etc)
		subsonicGroup.Any("/getLyrics.view", metadataHandler.GetLyrics)
		subsonicGroup.Any("/getLyricsBySongId.view", metadataHandler.GetLyricsBySongId)
		subsonicGroup.Any("/getOpenSubsonicExtensions.view", metadataHandler.GetOpenSubsonicExtensions)

		// Playlists
		subsonicGroup.Any("/getPlaylists.view", metadataHandler.GetPlaylists)
		subsonicGroup.Any("/getPlaylist.view", metadataHandler.GetPlaylist)

		// Media Retrieval
		subsonicGroup.Any("/stream.view", handler.Stream)
		subsonicGroup.Any("/stream", handler.Stream)
		subsonicGroup.Any("/download.view", handler.Stream)
		subsonicGroup.Any("/download", handler.Stream)
		subsonicGroup.Any("/getCoverArt.view", metadataHandler.GetCoverArt)
		subsonicGroup.Any("/getCoverArt", metadataHandler.GetCoverArt)
	}

	r.NoRoute(proxyHandler.Handle)

	// Health & Sync endpoints
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/sync", func(c *gin.Context) {
		id := c.Query("id")
		if id == "" {
			c.JSON(400, gin.H{"error": "id is required"})
			return
		}
		album, songs, err := squidService.GetAlbum(id)
		if err != nil {
			c.JSON(500, gin.H{"error": "Failed to fetch album info: " + err.Error()})
			return
		}
		if err := syncService.SyncAlbum(album, songs); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "synced", "id": id})
	})

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	// 5. Graceful Shutdown
	go func() {
		log.Printf("Starting JetStream on port %s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down JetStream...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("JetStream exited")
}
