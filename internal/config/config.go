package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	defaultWorkers   = 4
	defaultWantEvery = 24 * time.Hour
)

type Config struct {
	Port       string
	DBPath     string        // DB_PATH
	MusicDir   string        // MUSIC_DIR, the library root
	SinglesDir string        // DREDGE_SINGLES_DIR, relative to MusicDir; "-" files singles straight under it
	Workers    int           // WORKERS
	APIKey     string        // DREDGE_API_KEY; when set, /api needs it in X-Api-Key
	WantEvery  time.Duration // DREDGE_WANT_INTERVAL; how often wanted tracks are looked for again, 0 for never

	PyroscopeServerAddress string // PYROSCOPE_SERVER_ADDRESS; profiling is off when empty
}

func Load() *Config {
	c := &Config{
		Port:       getEnv("PORT", "8080"),
		DBPath:     getEnv("DB_PATH", defaultDBPath()),
		MusicDir:   getEnv("MUSIC_DIR", "/music"),
		SinglesDir: getEnv("DREDGE_SINGLES_DIR", "Singles"),
		Workers:    defaultWorkers,
		APIKey:     os.Getenv("DREDGE_API_KEY"),
		WantEvery:  defaultWantEvery,

		PyroscopeServerAddress: os.Getenv("PYROSCOPE_SERVER_ADDRESS"),
	}
	if v := os.Getenv("DREDGE_WANT_INTERVAL"); v == "0" {
		c.WantEvery = 0
	} else if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		c.WantEvery = d
	}
	c.SinglesDir = SinglesDir(c.SinglesDir)
	if n, err := strconv.Atoi(os.Getenv("WORKERS")); err == nil && n > 0 {
		c.Workers = n
	}
	return c
}

// SinglesDir reads the "-" given for no singles folder as empty.
func SinglesDir(s string) string {
	if s == "-" {
		return ""
	}
	return s
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
