package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, k := range []string{"PORT", "DB_PATH", "MUSIC_DIR", "DREDGE_SINGLES_DIR", "WORKERS"} {
		t.Setenv(k, "")
	}
	c := Load()
	if c.Port != "8080" || c.MusicDir != "/music" || c.SinglesDir != "Singles" || c.Workers != 4 {
		t.Errorf("config = %+v", c)
	}
	if !strings.HasSuffix(filepath.ToSlash(c.DBPath), "dredge/dredge.db") {
		t.Errorf("DBPath = %q, want a per-user default ending in dredge/dredge.db", c.DBPath)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	t.Setenv("PORT", "9000")
	t.Setenv("DB_PATH", "/data/dredge.db")
	t.Setenv("MUSIC_DIR", "/mnt/music")
	t.Setenv("DREDGE_SINGLES_DIR", "Loose")
	t.Setenv("WORKERS", "7")
	c := Load()
	if c.Port != "9000" || c.DBPath != "/data/dredge.db" || c.MusicDir != "/mnt/music" || c.SinglesDir != "Loose" || c.Workers != 7 {
		t.Errorf("config = %+v", c)
	}
}

func TestAnEmptySinglesDirIsAllowedViaDash(t *testing.T) {
	t.Setenv("DREDGE_SINGLES_DIR", "-")
	if c := Load(); c.SinglesDir != "" {
		t.Errorf("SinglesDir = %q, want empty (singles straight under the music dir)", c.SinglesDir)
	}
}

func TestBadWorkerCountsFallBack(t *testing.T) {
	for _, v := range []string{"0", "-3", "many", "1.5"} {
		t.Setenv("WORKERS", v)
		if c := Load(); c.Workers != 4 {
			t.Errorf("WORKERS=%q gave %d, want the default 4", v, c.Workers)
		}
	}
}
