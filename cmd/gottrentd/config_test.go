package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// Config has a map field (CategoryPaths), so it is not comparable with
	// == at all - compare the fields that matter here individually.
	want := defaultConfig()
	if cfg.DownloadDir != want.DownloadDir || cfg.ListenPort != want.ListenPort ||
		cfg.SeedLimitAction != want.SeedLimitAction || cfg.ContentLayout != want.ContentLayout ||
		cfg.APIAddress != want.APIAddress {
		t.Fatalf("loadConfig on a missing file = %+v, want defaultConfig() = %+v", cfg, want)
	}
}

func TestLoadConfigMergesFileOverDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"downloadDir": "/downloads", "listenPort": 7000, "categoryPaths": {"movies": "/downloads/movies"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.DownloadDir != "/downloads" {
		t.Fatalf("DownloadDir = %q, want /downloads", cfg.DownloadDir)
	}
	if cfg.ListenPort != 7000 {
		t.Fatalf("ListenPort = %d, want 7000", cfg.ListenPort)
	}
	if cfg.CategoryPaths["movies"] != "/downloads/movies" {
		t.Fatalf("CategoryPaths[movies] = %q, want /downloads/movies", cfg.CategoryPaths["movies"])
	}
	// Fields the file never mentioned must still carry defaultConfig()'s
	// values, not the JSON zero value - this is what makes a config file
	// only having to state what it actually wants to override.
	want := defaultConfig()
	if cfg.SeedLimitAction != want.SeedLimitAction {
		t.Fatalf("SeedLimitAction = %q (file didn't set it), want default %q", cfg.SeedLimitAction, want.SeedLimitAction)
	}
	if cfg.APIAddress != want.APIAddress {
		t.Fatalf("APIAddress = %q (file didn't set it), want default %q", cfg.APIAddress, want.APIAddress)
	}
}

func TestLoadConfigMalformedFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig succeeded on malformed JSON, want an error")
	}
}

func TestDefaultConfigPathEndsUnderGoTorrent(t *testing.T) {
	path, err := defaultConfigPath()
	if err != nil {
		t.Skipf("os.UserConfigDir unavailable in this environment: %v", err)
	}
	if filepath.Base(path) != "config.json" {
		t.Fatalf("defaultConfigPath() = %q, want it to end in config.json", path)
	}
	if filepath.Base(filepath.Dir(path)) != "GoTorrent" {
		t.Fatalf("defaultConfigPath() = %q, want its parent directory to be GoTorrent", path)
	}
}
