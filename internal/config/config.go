package config

import (
	"os"
	"path/filepath"
	"strconv"
)

const defaultWorkers = 4

type Config struct {
	Port       string
	DBPath     string // DB_PATH
	MusicDir   string // MUSIC_DIR, the library root
	SinglesDir string // DREDGE_SINGLES_DIR, relative to MusicDir; "-" files singles straight under it
	Workers    int    // WORKERS
}

func Load() *Config {
	c := &Config{
		Port:       getEnv("PORT", "8080"),
		DBPath:     getEnv("DB_PATH", defaultDBPath()),
		MusicDir:   getEnv("MUSIC_DIR", "/music"),
		SinglesDir: getEnv("DREDGE_SINGLES_DIR", "Singles"),
		Workers:    defaultWorkers,
	}
	if c.SinglesDir == "-" {
		c.SinglesDir = ""
	}
	if n, err := strconv.Atoi(os.Getenv("WORKERS")); err == nil && n > 0 {
		c.Workers = n
	}
	return c
}

// defaultDBPath suits running the CLI on a workstation; the container sets
// DB_PATH.
func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "dredge.db"
	}
	return filepath.Join(dir, "dredge", "dredge.db")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
