package config

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port           string
	NavidromeURL   string
	SquidURL       string
	MusicFolder    string
	SearchFolder   string
	DownloadFormat string
	SearchLimit    int
}

func Load() (*Config, error) {
	_ = godotenv.Load() // Ignore error if .env doesn't exist

	musicFolder := getEnv("MUSIC_FOLDER", "/music")
	return &Config{
		Port:           getEnv("PORT", "8080"),
		NavidromeURL:   getEnv("NAVIDROME_URL", getEnv("UPSTREAM_URL", getEnv("SUBSONIC_URL", "http://navidrome:4533"))),
		SquidURL:       getEnv("SQUID_URL", "https://triton.squid.wtf"),
		MusicFolder:    musicFolder,
		SearchFolder:   getEnv("SEARCH_FOLDER", filepath.Join(musicFolder, "search")),
		DownloadFormat: getEnv("DOWNLOAD_FORMAT", "opus"),
		SearchLimit:    getEnvInt("SEARCH_LIMIT", 50),
	}, nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, exists := os.LookupEnv(key); exists {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return fallback
}
