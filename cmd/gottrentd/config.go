package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is gottrentd's persisted configuration, loaded from a JSON file
// and layered with CLI flag overrides (see mergeFlags) — the two together
// are 4.1's "config file + flag overrides" requirement. JSON rather than
// the roadmap's original "config.toml" wording: this project has stayed
// dependency-free throughout (see CLAUDE.md), and encoding/json is already
// stdlib, unlike TOML, which has no parser in the standard library and
// would mean either pulling in a dependency or hand-rolling a real chunk
// of the TOML spec for a config file that does not need it. Field names
// and defaults mirror cmd/gottrent's own flags one for one, minus
// -torrent (a daemon has no per-invocation torrent list — that's what the
// persisted manifest and, from 4.2 on, the control API are for) and plus
// the API-specific fields flags 4.1 doesn't have a use for yet.
type Config struct {
	DownloadDir            string            `json:"downloadDir"`
	StateDir               string            `json:"stateDir"`
	ListenPort             uint16            `json:"listenPort"`
	RandomPort             bool              `json:"randomPort"`
	BindAddress            string            `json:"bindAddress"`
	NoPortMap              bool              `json:"noPortMap"`
	DownLimitKB            uint              `json:"downLimitKB"`
	UpLimitKB              uint              `json:"upLimitKB"`
	RatioLimit             float64           `json:"ratioLimit"`
	SeedTimeLimit          string            `json:"seedTimeLimit"`
	SeedLimitAction        string            `json:"seedLimitAction"`
	Sequential             bool              `json:"sequential"`
	FirstLastPieceFirst    bool              `json:"firstLastPieceFirst"`
	SuperSeeding           bool              `json:"superSeeding"`
	MaxActiveDownloads     int               `json:"maxActiveDownloads"`
	MaxActiveSeeds         int               `json:"maxActiveSeeds"`
	MaxActiveTotal         int               `json:"maxActiveTotal"`
	UploadSlots            int               `json:"uploadSlots"`
	ExcludeLANFromLimits   bool              `json:"excludeLanFromLimits"`
	AltDownLimitKB         uint              `json:"altDownLimitKB"`
	AltUpLimitKB           uint              `json:"altUpLimitKB"`
	AltSchedule            string            `json:"altSchedule"`
	ContentLayout          string            `json:"contentLayout"`
	CategoryPaths          map[string]string `json:"categoryPaths"`
	WatchDir               string            `json:"watchDir"`
	OnComplete             string            `json:"onComplete"`
	IPFilterPath           string            `json:"ipFilterPath"`
	IPFilterURL            string            `json:"ipFilterURL"`
	IPFilterFormat         string            `json:"ipFilterFormat"`
	IPFilterUpdateInterval string            `json:"ipFilterUpdateInterval"`
	ProxyType              string            `json:"proxyType"`
	ProxyAddress           string            `json:"proxyAddress"`
	ProxyUsername          string            `json:"proxyUsername"`
	ProxyPassword          string            `json:"proxyPassword"`
	ProxyDNS               bool              `json:"proxyDNS"`
	AnonymousMode          bool              `json:"anonymousMode"`
	// APIAddress is where the control API (4.2/4.3) listens, and — already,
	// as of 4.1 — what a bare bind attempt at startup uses as this daemon's
	// single-instance lock: a second gottrentd pointed at the same address
	// fails to bind it and refuses to start, no separate PID-file mechanism
	// needed. 127.0.0.1 by default, deliberately, per 4.3's own reasoning:
	// a localhost-bound API is not yet a secure one, but it is at least not
	// reachable from the network by default.
	APIAddress string `json:"apiAddress"`
	Verbose    bool   `json:"verbose"`
}

// defaultConfig mirrors cmd/gottrent's own flag defaults field for field,
// so a freshly-installed gottrentd with no config file at all behaves the
// same way gottrent does out of the box.
func defaultConfig() Config {
	return Config{
		DownloadDir:     ".",
		ListenPort:      6881,
		SeedLimitAction: "pause",
		ContentLayout:   "original",
		APIAddress:      "127.0.0.1:6880",
	}
}

// defaultConfigPath is where gottrentd looks for its config file when
// -config is not given: alongside the resume/manifest state this project
// already keeps under os.UserConfigDir() (see torrent.ResumeDir,
// engine.DefaultStateDir), not inside DownloadDir - config should survive
// a download directory getting wiped, and shouldn't get bundled up if the
// download directory ever gets archived or shared.
func defaultConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not locate a config directory: %w", err)
	}
	return filepath.Join(base, "GoTorrent", "config.json"), nil
}

// loadConfig reads path as JSON into a Config seeded with defaultConfig()'s
// values, so a config file only has to mention the fields it actually wants
// to override. A missing file is not an error - it just means "use the
// defaults, same as gottrent would" - but a present, malformed one is:
// silently falling back to defaults there would hide a typo the user has
// no other way to discover.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}
