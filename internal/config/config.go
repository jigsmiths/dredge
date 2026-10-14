package config

import "os"

type Config struct {
	Port   string
	DBPath string // DB_PATH — only used if internal/db is wired in
}

func Load() *Config {
	return &Config{
		Port:   getEnv("PORT", "8080"),
		DBPath: getEnv("DB_PATH", "/data/APP_NAME.db"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
