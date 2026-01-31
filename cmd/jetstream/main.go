package main

import (
	"context"
	"jetstream/internal/config"
	"jetstream/internal/handlers"
	"jetstream/internal/service"
	"log"
	"log/slog"
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
	searchHandler := handlers.NewSearchHandler(squidService, syncService, cfg, proxyHandler)
	metadataHandler := handlers.NewMetadataHandler(squidService, syncService, proxyHandler)
	handler := handlers.NewHandler(squidService, syncService, proxyHandler)
	maintenanceHandler := handlers.NewMaintenanceHandler(syncService)

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
		subsonicGroup.Any("/getMusicDirectory.view", metadataHandler.GetMusicDirectory)
		subsonicGroup.Any("/getMusicDirectory", metadataHandler.GetMusicDirectory)
		subsonicGroup.Any("/getGenres.view", proxyHandler.Handle)
		subsonicGroup.Any("/getGenres", proxyHandler.Handle)
		subsonicGroup.Any("/getArtists.view", proxyHandler.Handle)
		subsonicGroup.Any("/getArtists", proxyHandler.Handle)
		subsonicGroup.Any("/getArtist.view", metadataHandler.GetArtist)
		subsonicGroup.Any("/getArtist", metadataHandler.GetArtist)
		subsonicGroup.Any("/getAlbum.view", metadataHandler.GetAlbum)
		subsonicGroup.Any("/getAlbum", metadataHandler.GetAlbum)
		subsonicGroup.Any("/getAlbumInfo.view", metadataHandler.GetAlbumInfo)
		subsonicGroup.Any("/getAlbumInfo", metadataHandler.GetAlbumInfo)
		subsonicGroup.Any("/getAlbumInfo2.view", metadataHandler.GetAlbumInfo2)
		subsonicGroup.Any("/getAlbumInfo2", metadataHandler.GetAlbumInfo2)
		subsonicGroup.Any("/getSong.view", metadataHandler.GetSong)
		subsonicGroup.Any("/getSong", metadataHandler.GetSong)

		// Lists
		subsonicGroup.Any("/getAlbumList.view", searchHandler.GetAlbumList2)
		subsonicGroup.Any("/getAlbumList", searchHandler.GetAlbumList2)
		subsonicGroup.Any("/getAlbumList2.view", searchHandler.GetAlbumList2)
		subsonicGroup.Any("/getAlbumList2", searchHandler.GetAlbumList2)
		subsonicGroup.Any("/getRandomSongs.view", metadataHandler.GetRandomSongs)
		subsonicGroup.Any("/getRandomSongs", metadataHandler.GetRandomSongs)
		subsonicGroup.Any("/getSongsByGenre.view", metadataHandler.GetSongsByGenre)
		subsonicGroup.Any("/getSongsByGenre", metadataHandler.GetSongsByGenre)
		subsonicGroup.Any("/getNowPlaying.view", proxyHandler.Handle)
		subsonicGroup.Any("/getNowPlaying", proxyHandler.Handle)
		subsonicGroup.Any("/getStarred.view", metadataHandler.GetStarred)
		subsonicGroup.Any("/getStarred", metadataHandler.GetStarred)
		subsonicGroup.Any("/getStarred2.view", metadataHandler.GetStarred2)
		subsonicGroup.Any("/getStarred2", metadataHandler.GetStarred2)

		// Extra Metadata (legacy compatibility)
		subsonicGroup.Any("/getArtistInfo.view", metadataHandler.GetArtistInfo)
		subsonicGroup.Any("/getArtistInfo", metadataHandler.GetArtistInfo)
		subsonicGroup.Any("/getArtistInfo2.view", metadataHandler.GetArtistInfo2)
		subsonicGroup.Any("/getArtistInfo2", metadataHandler.GetArtistInfo2)
		subsonicGroup.Any("/getSimilarArtists.view", metadataHandler.GetSimilarArtists)
		subsonicGroup.Any("/getSimilarArtists", metadataHandler.GetSimilarArtists)
		subsonicGroup.Any("/getSimilarArtists2", metadataHandler.GetSimilarArtists2)
		subsonicGroup.Any("/getSimilarSongs.view", metadataHandler.GetSimilarSongs)
		subsonicGroup.Any("/getSimilarSongs", metadataHandler.GetSimilarSongs)
		subsonicGroup.Any("/getSimilarSongs2.view", metadataHandler.GetSimilarSongs2)
		subsonicGroup.Any("/getSimilarSongs2", metadataHandler.GetSimilarSongs2)
		subsonicGroup.Any("/getTopSongs.view", searchHandler.GetTopSongs)
		subsonicGroup.Any("/getTopSongs", searchHandler.GetTopSongs)

		// User Interaction
		subsonicGroup.Any("/scrobble.view", metadataHandler.Scrobble)
		subsonicGroup.Any("/scrobble", metadataHandler.Scrobble)
		subsonicGroup.Any("/star.view", metadataHandler.Star)
		subsonicGroup.Any("/star", metadataHandler.Star)
		subsonicGroup.Any("/unstar.view", metadataHandler.Unstar)
		subsonicGroup.Any("/unstar", metadataHandler.Unstar)
		subsonicGroup.Any("/getUser.view", proxyHandler.Handle)
		subsonicGroup.Any("/getUser", proxyHandler.Handle)

		// Search
		subsonicGroup.Any("/search.view", searchHandler.Search)
		subsonicGroup.Any("/search", searchHandler.Search)
		subsonicGroup.Any("/search2.view", searchHandler.Search2)
		subsonicGroup.Any("/search2", searchHandler.Search2)
		subsonicGroup.Any("/search3.view", searchHandler.Search3)
		subsonicGroup.Any("/search3", searchHandler.Search3)

		// OpenSubsonic Extensions (Lyrics, etc)
		subsonicGroup.Any("/getLyrics.view", metadataHandler.GetLyrics)
		subsonicGroup.Any("/getLyrics", metadataHandler.GetLyrics)
		subsonicGroup.Any("/getLyricsBySongId.view", metadataHandler.GetLyricsBySongId)
		subsonicGroup.Any("/getLyricsBySongId", metadataHandler.GetLyricsBySongId)
		subsonicGroup.Any("/getOpenSubsonicExtensions.view", metadataHandler.GetOpenSubsonicExtensions)
		subsonicGroup.Any("/getOpenSubsonicExtensions", metadataHandler.GetOpenSubsonicExtensions)

		// Playlists
		subsonicGroup.Any("/getPlaylists.view", metadataHandler.GetPlaylists)
		subsonicGroup.Any("/getPlaylists", metadataHandler.GetPlaylists)
		subsonicGroup.Any("/getPlaylist.view", metadataHandler.GetPlaylist)
		subsonicGroup.Any("/getPlaylist", metadataHandler.GetPlaylist)
		subsonicGroup.Any("/createPlaylist.view", proxyHandler.Handle)
		subsonicGroup.Any("/createPlaylist", proxyHandler.Handle)
		subsonicGroup.Any("/deletePlaylist.view", proxyHandler.Handle)
		subsonicGroup.Any("/deletePlaylist", proxyHandler.Handle)
		subsonicGroup.Any("/updatePlaylist.view", proxyHandler.Handle)
		subsonicGroup.Any("/updatePlaylist", proxyHandler.Handle)

		// Media Retrieval
		subsonicGroup.Any("/stream.view", handler.Stream)
		subsonicGroup.Any("/stream", handler.Stream)
		subsonicGroup.Any("/download.view", handler.Stream)
		subsonicGroup.Any("/download", handler.Stream)
		subsonicGroup.Any("/getCoverArt.view", metadataHandler.GetCoverArt)
		subsonicGroup.Any("/getCoverArt", metadataHandler.GetCoverArt)
	}

	r.NoRoute(proxyHandler.Handle)

	// Health & Maintenance
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/maintenance/scan", maintenanceHandler.Scan)
	r.GET("/sync", func(c *gin.Context) {
		id := c.Query("id")
		if id == "" {
			c.JSON(400, gin.H{"error": "id is required"})
			return
		}
		album, songs, err := squidService.GetAlbum(context.Background(), id)
		if err != nil {
			c.JSON(500, gin.H{"error": "Failed to fetch album info: " + err.Error()})
			return
		}
		if err := syncService.SyncAlbum(context.Background(), album, songs); err != nil {
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
		slog.Info("Starting JetStream", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Listen error", "error", err)
			os.Exit(1)
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
