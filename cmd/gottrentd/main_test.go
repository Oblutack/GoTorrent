package main

import (
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/storage"
)

// TestMergeFlagsOnlyAppliesExplicitFlags proves the core precedence rule:
// a flag not present in explicit (never passed on the command line) must
// leave whatever loadConfig already put in cfg untouched, even though the
// flag package always gives every flag *some* zero value.
func TestMergeFlagsOnlyAppliesExplicitFlags(t *testing.T) {
	cfg := Config{DownloadDir: "/from-config", ListenPort: 9999, Verbose: true}

	downloadDir := "/from-flag"
	listenPort := uint(0) // the flag's own zero default - must NOT stomp 9999
	verbose := false      // ditto - must NOT stomp true, since -verbose was never passed
	seedTimeLimit := 2 * time.Hour
	ipFilterUpdateInterval := time.Duration(0)

	// explicit only names "dir" - every other flag pointer below is present
	// in flagValues (mergeFlags needs the whole struct) but must be ignored
	// since it was never actually passed.
	explicit := map[string]bool{"dir": true}

	mergeFlags(&cfg, explicit, flagValues{
		downloadDir: &downloadDir, listenPort: &listenPort, verbose: &verbose,
		seedTimeLimit: &seedTimeLimit, ipFilterUpdateInterval: &ipFilterUpdateInterval,
	})

	if cfg.DownloadDir != "/from-flag" {
		t.Fatalf("DownloadDir = %q, want the explicitly-passed /from-flag", cfg.DownloadDir)
	}
	if cfg.ListenPort != 9999 {
		t.Fatalf("ListenPort = %d, want the config file's 9999 untouched (flag was never passed)", cfg.ListenPort)
	}
	if !cfg.Verbose {
		t.Fatal("Verbose was flipped false by an unpassed -verbose flag's zero default")
	}
}

// TestMergeFlagsAppliesEveryExplicitFlag is the complement: once a flag IS
// in explicit, its value must actually land in cfg, covering one field of
// each type mergeFlags handles (string, uint16 via uint, bool, duration
// stored as a string, float64, map).
func TestMergeFlagsAppliesEveryExplicitFlag(t *testing.T) {
	cfg := Config{}

	downloadDir := "/explicit"
	listenPort := uint(6882)
	randomPort := true
	ratioLimit := 1.5
	seedTimeLimit := 90 * time.Minute
	catPaths := categoryPaths{"tv": "/tv"}

	explicit := map[string]bool{
		"dir": true, "port": true, "random-port": true, "ratio-limit": true, "seed-time-limit": true,
	}
	mergeFlags(&cfg, explicit, flagValues{
		downloadDir: &downloadDir, listenPort: &listenPort, randomPort: &randomPort,
		ratioLimit: &ratioLimit, seedTimeLimit: &seedTimeLimit, catPaths: catPaths,
	})

	if cfg.DownloadDir != "/explicit" {
		t.Fatalf("DownloadDir = %q, want /explicit", cfg.DownloadDir)
	}
	if cfg.ListenPort != 6882 {
		t.Fatalf("ListenPort = %d, want 6882", cfg.ListenPort)
	}
	if !cfg.RandomPort {
		t.Fatal("RandomPort not applied")
	}
	if cfg.RatioLimit != 1.5 {
		t.Fatalf("RatioLimit = %v, want 1.5", cfg.RatioLimit)
	}
	if cfg.SeedTimeLimit != "1h30m0s" {
		t.Fatalf("SeedTimeLimit = %q, want 1h30m0s", cfg.SeedTimeLimit)
	}
	if cfg.CategoryPaths["tv"] != "/tv" {
		t.Fatalf("CategoryPaths[tv] = %q, want /tv", cfg.CategoryPaths["tv"])
	}
}

func TestBuildDefaultsTranslatesConfig(t *testing.T) {
	cfg := defaultConfig()
	cfg.DownloadDir = "/downloads"
	cfg.ListenPort = 7000
	cfg.ContentLayout = "subfolder"
	cfg.SeedLimitAction = "remove"
	cfg.SeedTimeLimit = "2h30m"
	cfg.DownLimitKB = 100
	cfg.UpLimitKB = 50
	cfg.Sequential = true

	defaults, err := buildDefaults(cfg)
	if err != nil {
		t.Fatalf("buildDefaults: %v", err)
	}
	if defaults.DownloadDir != "/downloads" {
		t.Fatalf("DownloadDir = %q, want /downloads", defaults.DownloadDir)
	}
	if defaults.ListenPort != 7000 {
		t.Fatalf("ListenPort = %d, want 7000", defaults.ListenPort)
	}
	if defaults.ContentLayout != storage.LayoutSubfolder {
		t.Fatalf("ContentLayout = %v, want LayoutSubfolder", defaults.ContentLayout)
	}
	if defaults.SeedTimeLimit != 2*time.Hour+30*time.Minute {
		t.Fatalf("SeedTimeLimit = %v, want 2h30m", defaults.SeedTimeLimit)
	}
	if defaults.DownLimit == nil || defaults.DownLimit.Limit() != 100*1024 {
		t.Fatalf("DownLimit not built from DownLimitKB=100")
	}
	if defaults.UpLimit == nil || defaults.UpLimit.Limit() != 50*1024 {
		t.Fatalf("UpLimit not built from UpLimitKB=50")
	}
	if defaults.PickerStrategy != picker.Sequential {
		t.Fatalf("PickerStrategy = %v, want picker.Sequential", defaults.PickerStrategy)
	}
}

func TestBuildDefaultsRejectsInvalidContentLayout(t *testing.T) {
	cfg := defaultConfig()
	cfg.ContentLayout = "bogus"
	if _, err := buildDefaults(cfg); err == nil {
		t.Fatal("buildDefaults succeeded with an invalid contentLayout, want an error")
	}
}

func TestBuildDefaultsRejectsInvalidSeedLimitAction(t *testing.T) {
	cfg := defaultConfig()
	cfg.SeedLimitAction = "bogus"
	if _, err := buildDefaults(cfg); err == nil {
		t.Fatal("buildDefaults succeeded with an invalid seedLimitAction, want an error")
	}
}

func TestBuildDefaultsRejectsInvalidDuration(t *testing.T) {
	cfg := defaultConfig()
	cfg.SeedTimeLimit = "not-a-duration"
	if _, err := buildDefaults(cfg); err == nil {
		t.Fatal("buildDefaults succeeded with an invalid seedTimeLimit, want an error")
	}
}

func TestBuildDefaultsRejectsInvalidAltSchedule(t *testing.T) {
	cfg := defaultConfig()
	cfg.AltSchedule = "not a real schedule"
	if _, err := buildDefaults(cfg); err == nil {
		t.Fatal("buildDefaults succeeded with an invalid altSchedule, want an error")
	}
}
