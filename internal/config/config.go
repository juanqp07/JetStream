package config

import (
	"encoding/base64"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port           string
	NavidromeURL   string
	SquidURL       string   // Primary URL for backward compatibility
	SquidURLs      []string // All URLs including fallbacks
	MusicFolder    string
	DownloadFormat string
	SearchLimit    int
	RedisAddr      string
}

func Load() (*Config, error) {
	_ = godotenv.Load() // Ignore error if .env doesn't exist

	musicFolder := getEnv("MUSIC_FOLDER", "/music")
	primarySquidURL := getEnv("SQUID_URL", "https://triton.squid.wtf")

	// Decode fallback URLs (same as allstarr)
	encodedURLs := []string{
		"aHR0cHM6Ly90cml0b24uc3F1aWQud3Rm",             // squid wtf
		"aHR0cHM6Ly90aWRhbC5raW5vcGx1cy5vbmxpbmU=",     // Kinoplus
		"aHR0cHM6Ly90aWRhbC1hcGkuYmluaW11bS5vcmc=",     // Binimum
		"aHR0cHM6Ly9tb25vY2hyb21lLWFwaS5zYW1pZHkuY29t", // Monochrome
		"aHR0cHM6Ly9oaWZpLW9uZS5zcG90aXNhdmVyLm5ldA==", // Spotisaver 1
		"aHR0cHM6Ly9oaWZpLXR3by5zcG90aXNhdmVyLm5ldA==", // Spotisaver 2
		"aHR0cHM6Ly93b2xmLnFxZGwuc2l0ZQ==",             // Lucida wolf
		"aHR0cDovL2h1bmQucXFkbC5zaXRl",                 // Lucida hund
		"aHR0cHM6Ly9tYXVzLnFxZGwuc2l0ZQ==",             // Lucida maus
		"aHR0cHM6Ly92b2dlbC5xcWRsLnNpdGU=",             // Lucida vogel
		"aHR0cHM6Ly9rYXR6ZS5xcWRsLnNpdGU=",             // Lucida katze
	}

	// Decode URLs
	squidURLs := make([]string, 0, len(encodedURLs))
	for _, encoded := range encodedURLs {
		if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			squidURLs = append(squidURLs, string(decoded))
		}
	}

	// If custom SQUID_URL is set and not in the list, prepend it
	if primarySquidURL != "" && primarySquidURL != "https://triton.squid.wtf" {
		squidURLs = append([]string{primarySquidURL}, squidURLs...)
	}

	cfg := &Config{
		Port:           getEnv("PORT", "8080"),
		NavidromeURL:   getEnv("NAVIDROME_URL", getEnv("UPSTREAM_URL", getEnv("SUBSONIC_URL", "http://navidrome:4533"))),
		SquidURL:       primarySquidURL,
		SquidURLs:      squidURLs,
		MusicFolder:    musicFolder,
		DownloadFormat: getEnv("DOWNLOAD_FORMAT", "opus"),
		SearchLimit:    getEnvInt("SEARCH_LIMIT", 50),
		RedisAddr:      getEnv("REDIS_ADDR", "localhost:6379"),
	}

	log.Printf("[Config] Loaded RedisAddr: %s", cfg.RedisAddr)
	log.Printf("[Config] Loaded %d Squid fallback URLs", len(cfg.SquidURLs))
	return cfg, nil
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
