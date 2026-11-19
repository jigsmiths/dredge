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

	// Soulseek through slskd, for wanted tracks the catalogue lacks; off unless all three are set
	SlskdURL    string // DREDGE_SLSKD_URL
	SlskdAPIKey string // DREDGE_SLSKD_API_KEY
	SlskdDir    string // DREDGE_SLSKD_DIR, slskd's downloads folder as dredge sees it

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

		SlskdURL:    os.Getenv("DREDGE_SLSKD_URL"),
		SlskdAPIKey: os.Getenv("DREDGE_SLSKD_API_KEY"),
		SlskdDir:    os.Getenv("DREDGE_SLSKD_DIR"),

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

// Soulseek reports whether slskd is fully set up, and whether any of it is.
func (c *Config) Soulseek() (on, partly bool) {
	set := 0
	for _, v := range []string{c.SlskdURL, c.SlskdAPIKey, c.SlskdDir} {
		if v != "" {
			set++
		}
	}
	return set == 3, set > 0 && set < 3
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
