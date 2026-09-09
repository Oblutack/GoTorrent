// Command gottrentd is the headless daemon half of Phase 4: it runs the
// same engine.Engine every gottrent invocation does, but as a long-lived
// background process configured from a JSON file rather than a one-shot
// -torrent list. The control-API address is reserved at startup both as
// this process's single-instance lock and as where 4.3's security chain
// (bearer token, Host-header allowlist, brute-force lockout, optional TLS
// - see internal/api) already applies to the one route that exists today,
// a bare health check. The real REST/WebSocket routes (4.2) attach to the
// same mux, behind the same chain, once they exist.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Oblutack/GoTorrent/internal/api"
	"github.com/Oblutack/GoTorrent/internal/bootstrap"
	"github.com/Oblutack/GoTorrent/internal/engine"
	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/picker"
	"github.com/Oblutack/GoTorrent/internal/ratelimit"
	"github.com/Oblutack/GoTorrent/internal/storage"
	"github.com/Oblutack/GoTorrent/internal/version"
)

// categoryPaths mirrors cmd/gottrent's own flag.Value for -category-path.
type categoryPaths map[string]string

func (p categoryPaths) String() string {
	parts := make([]string, 0, len(p))
	for k, v := range p {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (p categoryPaths) Set(v string) error {
	name, path, ok := strings.Cut(v, "=")
	if !ok || name == "" || path == "" {
		return fmt.Errorf(`invalid -category-path %q, want "name=path"`, v)
	}
	p[name] = path
	return nil
}

func main() {
	if err := run(); err != nil {
		logger.Error.Fatalf("%v\n", err)
	}
}

func run() error {
	configPath := flag.String("config", "", "Path to the JSON config file (default: a config.json under the OS config dir)")
	downloadDir := flag.String("dir", "", "Default directory to save downloaded files")
	stateDir := flag.String("state-dir", "", "Directory for the fleet manifest (default: a directory under the OS config dir)")
	listenPort := flag.Uint("port", 0, "Port to listen on for inbound peer connections and advertise to trackers")
	randomPort := flag.Bool("random-port", false, "Listen on a random OS-assigned port instead of -port")
	bindAddress := flag.String("bind-address", "", "Local address to listen on for inbound peer connections (default: all interfaces)")
	noPortMap := flag.Bool("no-portmap", false, "Disable automatic UPnP/NAT-PMP port mapping")
	downLimitKB := flag.Uint("down-limit", 0, "Download rate cap in KiB/s across the whole fleet (0 = unlimited)")
	upLimitKB := flag.Uint("up-limit", 0, "Upload rate cap in KiB/s across the whole fleet (0 = unlimited)")
	ratioLimit := flag.Float64("ratio-limit", 0, "Pause a torrent once its upload/download ratio reaches this (0 = unlimited)")
	seedTimeLimit := flag.Duration("seed-time-limit", 0, "Pause a torrent once it has spent this long seeding, e.g. 2h30m (0 = unlimited)")
	seedLimitAction := flag.String("seed-limit-action", "", `What to do beyond pausing when -ratio-limit/-seed-time-limit is reached: "pause", "remove", or "remove-delete-data"`)
	sequential := flag.Bool("sequential", false, "Download pieces in order instead of rarest-first (useful for streaming)")
	firstLastPiece := flag.Bool("first-last-piece-first", false, "Fetch each file's first and last piece early, so a partially-downloaded file can be previewed")
	superSeeding := flag.Bool("super-seeding", false, "Advertise pieces one at a time while seeding (BEP 16), to spread a brand-new torrent's first copies across the swarm faster")
	maxActiveDownloads := flag.Int("max-active-downloads", 0, "Maximum torrents actively downloading at once across the fleet (0 = unlimited)")
	maxActiveSeeds := flag.Int("max-active-seeds", 0, "Maximum torrents actively seeding at once across the fleet (0 = unlimited)")
	maxActiveTotal := flag.Int("max-active", 0, "Maximum torrents active (downloading or seeding) at once across the fleet (0 = unlimited)")
	uploadSlots := flag.Int("upload-slots", 0, "Peers unchoked for upload at once, per torrent (0 = choker default)")
	excludeLAN := flag.Bool("exclude-lan-limits", false, "Don't apply -down-limit/-up-limit to peers on a private or loopback address")
	altDownLimitKB := flag.Uint("alt-down-limit", 0, "Download rate cap in KiB/s while -alt-schedule is active (0 = unlimited)")
	altUpLimitKB := flag.Uint("alt-up-limit", 0, "Upload rate cap in KiB/s while -alt-schedule is active (0 = unlimited)")
	altSchedule := flag.String("alt-schedule", "", `Weekly window to apply -alt-down-limit/-alt-up-limit instead of -down-limit/-up-limit, e.g. "22:00-06:00" or "Mon,Tue,Wed,Thu,Fri 09:00-17:00" (empty = disabled)`)
	contentLayoutFlag := flag.String("content-layout", "", `Directory layout for downloaded content: "original", "subfolder" (always wrap in a <name>/ directory), or "no-subfolder" (never wrap, even a multi-file torrent)`)
	catPaths := make(categoryPaths)
	flag.Var(catPaths, "category-path", `Default save path for a category, as "name=path" (repeat for multiple categories)`)
	watchDir := flag.String("watch-dir", "", "Directory to poll for .torrent files and auto-add (empty = disabled)")
	onComplete := flag.String("on-complete", "", `Shell command to run the first time a torrent finishes seeding, with %N/%F/%D substituted for its name/content path/download directory (empty = disabled)`)
	ipFilterPath := flag.String("ip-filter", "", "Path to an eMule ipfilter.dat or PeerGuardian .p2p blocklist file (empty = disabled)")
	ipFilterURL := flag.String("ip-filter-url", "", "URL to auto-update the IP filter from, in addition to -ip-filter (empty = disabled)")
	ipFilterFormat := flag.String("ip-filter-format", "", `Blocklist format: "dat" or "p2p" (empty = guess from -ip-filter/-ip-filter-url's extension)`)
	ipFilterUpdateInterval := flag.Duration("ip-filter-update-interval", 0, "How often to re-fetch -ip-filter-url, e.g. 12h (0 = 24h default)")
	proxyType := flag.String("proxy-type", "", `Outbound proxy for peer connections and HTTP(S) tracker announces: "socks5" or "http" (empty = disabled; UDP trackers and DHT are never proxied)`)
	proxyAddress := flag.String("proxy-address", "", `Proxy address, as "host:port"`)
	proxyUsername := flag.String("proxy-username", "", "Proxy username, if it requires authentication")
	proxyPassword := flag.String("proxy-password", "", "Proxy password, if it requires authentication")
	proxyDNS := flag.Bool("proxy-dns", false, "Resolve hostnames through the SOCKS5 proxy itself instead of locally (meaningless for -proxy-type=http)")
	anonymousMode := flag.Bool("anonymous-mode", false, "Strip the client fingerprint from the peer ID and disable LSD; requires -proxy-type to also be set")
	apiAddress := flag.String("api-address", "", `Address the control API listens on, as "host:port" (default 127.0.0.1:6880); also this process's single-instance lock`)
	tlsCertFile := flag.String("tls-cert", "", "TLS certificate file for the API listener (requires -tls-key too; empty = plain HTTP)")
	tlsKeyFile := flag.String("tls-key", "", "TLS private key file for the API listener (requires -tls-cert too)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	path := *configPath
	if path == "" {
		p, err := defaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolving default config path: %w", err)
		}
		path = p
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	mergeFlags(&cfg, explicit, flagValues{
		downloadDir: downloadDir, stateDir: stateDir, listenPort: listenPort,
		randomPort: randomPort, bindAddress: bindAddress, noPortMap: noPortMap,
		downLimitKB: downLimitKB, upLimitKB: upLimitKB, ratioLimit: ratioLimit,
		seedTimeLimit: seedTimeLimit, seedLimitAction: seedLimitAction,
		sequential: sequential, firstLastPiece: firstLastPiece, superSeeding: superSeeding,
		maxActiveDownloads: maxActiveDownloads, maxActiveSeeds: maxActiveSeeds, maxActiveTotal: maxActiveTotal,
		uploadSlots: uploadSlots, excludeLAN: excludeLAN,
		altDownLimitKB: altDownLimitKB, altUpLimitKB: altUpLimitKB, altSchedule: altSchedule,
		contentLayout: contentLayoutFlag, watchDir: watchDir, onComplete: onComplete,
		ipFilterPath: ipFilterPath, ipFilterURL: ipFilterURL, ipFilterFormat: ipFilterFormat,
		ipFilterUpdateInterval: ipFilterUpdateInterval,
		proxyType:              proxyType, proxyAddress: proxyAddress, proxyUsername: proxyUsername, proxyPassword: proxyPassword,
		proxyDNS: proxyDNS, anonymousMode: anonymousMode, apiAddress: apiAddress, verbose: verbose,
		tlsCertFile: tlsCertFile, tlsKeyFile: tlsKeyFile,
		catPaths: catPaths,
	})

	logger.Init(cfg.Verbose)

	defaults, err := buildDefaults(cfg)
	if err != nil {
		return fmt.Errorf("building engine defaults: %w", err)
	}

	// The single-instance lock: bind cfg.APIAddress before doing anything
	// else. A second gottrentd pointed at the same address gets a plain
	// "address already in use" here and refuses to start, rather than
	// silently running two engines against the same manifest and download
	// directories.
	apiListener, err := net.Listen("tcp", cfg.APIAddress)
	if err != nil {
		return fmt.Errorf("binding API address %s (is gottrentd already running?): %w", cfg.APIAddress, err)
	}
	defer apiListener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resolvedStateDir := cfg.StateDir
	if resolvedStateDir == "" {
		resolvedStateDir, err = engine.DefaultStateDir()
		if err != nil {
			return fmt.Errorf("resolving default state directory: %w", err)
		}
	}

	e, actualPort, err := bootstrap.Engine(ctx, bootstrap.Options{
		StateDir:   resolvedStateDir,
		ListenPort: cfg.ListenPort,
		RandomPort: cfg.RandomPort,
		NoPortMap:  cfg.NoPortMap,
		WatchDir:   cfg.WatchDir,
		Defaults:   defaults,
	})
	if err != nil {
		return fmt.Errorf("starting engine: %w", err)
	}
	logger.Logf("gottrentd %s: engine started, peer port %d, API listening on %s\n", version.UserAgent, actualPort, apiListener.Addr())

	token, err := api.LoadOrCreateToken(filepath.Join(filepath.Dir(path), "api-token"))
	if err != nil {
		return fmt.Errorf("loading API token: %w", err)
	}

	handler := api.NewHandler(api.Config{
		Token:              token,
		AllowedHosts:       api.DefaultAllowedHosts(cfg.APIAddress),
		MaxAuthFailures:    api.DefaultMaxAuthFailures,
		AuthFailureWindow:  api.DefaultAuthFailureWindow,
		AuthFailureLockout: api.DefaultAuthFailureLockout,
	}, api.Routes(e, version.UserAgent, filepath.Join(resolvedStateDir, "torrents")))

	apiConn := net.Listener(apiListener)
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		tlsConfig, err := api.LoadTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("loading TLS config: %w", err)
		}
		apiConn = tls.NewListener(apiListener, tlsConfig)
	}

	server := &http.Server{Handler: handler}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(apiConn) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
		logger.Logf("gottrentd: shutdown signal received, saving state and disconnecting...\n")
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			logger.Error.Printf("gottrentd: API server: %v\n", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
	cancel() // stops DHT/LSD/watch-folder/alt-speed background goroutines started via bootstrap.Engine
	e.Shutdown()

	logger.Logf("gottrentd: stopped.\n")
	return nil
}

// flagValues bundles every flag pointer mergeFlags needs, since Go has no
// way to iterate a struct's own fields generically by the matching flag
// name - explicit is the alternative to either a large reflect-based
// helper (overkill for ~30 fields with mixed types) or duplicating this
// list a second time.
type flagValues struct {
	downloadDir, stateDir, bindAddress                                          *string
	listenPort                                                                  *uint
	randomPort, noPortMap, sequential, firstLastPiece, superSeeding, excludeLAN *bool
	downLimitKB, upLimitKB, altDownLimitKB, altUpLimitKB                        *uint
	ratioLimit                                                                  *float64
	seedTimeLimit, ipFilterUpdateInterval                                       *time.Duration
	seedLimitAction, altSchedule, contentLayout, watchDir, onComplete           *string
	ipFilterPath, ipFilterURL, ipFilterFormat                                   *string
	proxyType, proxyAddress, proxyUsername, proxyPassword                       *string
	proxyDNS, anonymousMode, verbose                                            *bool
	maxActiveDownloads, maxActiveSeeds, maxActiveTotal, uploadSlots             *int
	apiAddress, tlsCertFile, tlsKeyFile                                         *string
	catPaths                                                                    categoryPaths
}

// mergeFlags overlays onto cfg only the flags actually present in explicit
// (populated via flag.Visit) - a flag never mentioned on the command line
// leaves whatever loadConfig already put in cfg (the file's value, or
// defaultConfig()'s if the file didn't set it either) untouched, which is
// what makes "config file, flags override" actually hold: a flag's own
// zero-value default must never stomp a real config-file setting just
// because the flag package always gives it *some* value.
func mergeFlags(cfg *Config, explicit map[string]bool, f flagValues) {
	if explicit["dir"] {
		cfg.DownloadDir = *f.downloadDir
	}
	if explicit["state-dir"] {
		cfg.StateDir = *f.stateDir
	}
	if explicit["port"] {
		cfg.ListenPort = uint16(*f.listenPort)
	}
	if explicit["random-port"] {
		cfg.RandomPort = *f.randomPort
	}
	if explicit["bind-address"] {
		cfg.BindAddress = *f.bindAddress
	}
	if explicit["no-portmap"] {
		cfg.NoPortMap = *f.noPortMap
	}
	if explicit["down-limit"] {
		cfg.DownLimitKB = *f.downLimitKB
	}
	if explicit["up-limit"] {
		cfg.UpLimitKB = *f.upLimitKB
	}
	if explicit["ratio-limit"] {
		cfg.RatioLimit = *f.ratioLimit
	}
	if explicit["seed-time-limit"] {
		cfg.SeedTimeLimit = f.seedTimeLimit.String()
	}
	if explicit["seed-limit-action"] {
		cfg.SeedLimitAction = *f.seedLimitAction
	}
	if explicit["sequential"] {
		cfg.Sequential = *f.sequential
	}
	if explicit["first-last-piece-first"] {
		cfg.FirstLastPieceFirst = *f.firstLastPiece
	}
	if explicit["super-seeding"] {
		cfg.SuperSeeding = *f.superSeeding
	}
	if explicit["max-active-downloads"] {
		cfg.MaxActiveDownloads = *f.maxActiveDownloads
	}
	if explicit["max-active-seeds"] {
		cfg.MaxActiveSeeds = *f.maxActiveSeeds
	}
	if explicit["max-active"] {
		cfg.MaxActiveTotal = *f.maxActiveTotal
	}
	if explicit["upload-slots"] {
		cfg.UploadSlots = *f.uploadSlots
	}
	if explicit["exclude-lan-limits"] {
		cfg.ExcludeLANFromLimits = *f.excludeLAN
	}
	if explicit["alt-down-limit"] {
		cfg.AltDownLimitKB = *f.altDownLimitKB
	}
	if explicit["alt-up-limit"] {
		cfg.AltUpLimitKB = *f.altUpLimitKB
	}
	if explicit["alt-schedule"] {
		cfg.AltSchedule = *f.altSchedule
	}
	if explicit["content-layout"] {
		cfg.ContentLayout = *f.contentLayout
	}
	if len(f.catPaths) > 0 {
		if cfg.CategoryPaths == nil {
			cfg.CategoryPaths = make(map[string]string, len(f.catPaths))
		}
		for k, v := range f.catPaths {
			cfg.CategoryPaths[k] = v
		}
	}
	if explicit["watch-dir"] {
		cfg.WatchDir = *f.watchDir
	}
	if explicit["on-complete"] {
		cfg.OnComplete = *f.onComplete
	}
	if explicit["ip-filter"] {
		cfg.IPFilterPath = *f.ipFilterPath
	}
	if explicit["ip-filter-url"] {
		cfg.IPFilterURL = *f.ipFilterURL
	}
	if explicit["ip-filter-format"] {
		cfg.IPFilterFormat = *f.ipFilterFormat
	}
	if explicit["ip-filter-update-interval"] {
		cfg.IPFilterUpdateInterval = f.ipFilterUpdateInterval.String()
	}
	if explicit["proxy-type"] {
		cfg.ProxyType = *f.proxyType
	}
	if explicit["proxy-address"] {
		cfg.ProxyAddress = *f.proxyAddress
	}
	if explicit["proxy-username"] {
		cfg.ProxyUsername = *f.proxyUsername
	}
	if explicit["proxy-password"] {
		cfg.ProxyPassword = *f.proxyPassword
	}
	if explicit["proxy-dns"] {
		cfg.ProxyDNS = *f.proxyDNS
	}
	if explicit["anonymous-mode"] {
		cfg.AnonymousMode = *f.anonymousMode
	}
	if explicit["api-address"] {
		cfg.APIAddress = *f.apiAddress
	}
	if explicit["tls-cert"] {
		cfg.TLSCertFile = *f.tlsCertFile
	}
	if explicit["tls-key"] {
		cfg.TLSKeyFile = *f.tlsKeyFile
	}
	if explicit["verbose"] {
		cfg.Verbose = *f.verbose
	}
}

// buildDefaults turns a fully-merged Config into engine.Defaults, the same
// translation cmd/gottrent's runFleet does from its own flags - parsing
// string fields (durations, the content-layout/seed-limit-action enums,
// the alt-schedule grammar) and erroring on anything malformed rather than
// silently falling back to a zero value a user would never notice was
// wrong.
func buildDefaults(cfg Config) (engine.Defaults, error) {
	contentLayout, err := parseContentLayout(cfg.ContentLayout)
	if err != nil {
		return engine.Defaults{}, fmt.Errorf("contentLayout: %w", err)
	}
	seedLimitAction, err := parseSeedLimitAction(cfg.SeedLimitAction)
	if err != nil {
		return engine.Defaults{}, fmt.Errorf("seedLimitAction: %w", err)
	}
	seedTimeLimit, err := parseOptionalDuration(cfg.SeedTimeLimit)
	if err != nil {
		return engine.Defaults{}, fmt.Errorf("seedTimeLimit: %w", err)
	}
	ipFilterUpdateInterval, err := parseOptionalDuration(cfg.IPFilterUpdateInterval)
	if err != nil {
		return engine.Defaults{}, fmt.Errorf("ipFilterUpdateInterval: %w", err)
	}

	defaults := engine.Defaults{
		DownloadDir:            cfg.DownloadDir,
		ListenPort:             cfg.ListenPort,
		BindAddress:            cfg.BindAddress,
		SeedRatioLimit:         cfg.RatioLimit,
		SeedTimeLimit:          seedTimeLimit,
		SeedLimitAction:        seedLimitAction,
		FirstLastPieceFirst:    cfg.FirstLastPieceFirst,
		SuperSeeding:           cfg.SuperSeeding,
		MaxActiveDownloads:     cfg.MaxActiveDownloads,
		MaxActiveSeeds:         cfg.MaxActiveSeeds,
		MaxActiveTotal:         cfg.MaxActiveTotal,
		UploadSlots:            cfg.UploadSlots,
		ExcludeLANFromLimits:   cfg.ExcludeLANFromLimits,
		AltDownLimit:           int64(cfg.AltDownLimitKB) * 1024,
		AltUpLimit:             int64(cfg.AltUpLimitKB) * 1024,
		ContentLayout:          contentLayout,
		CategoryPaths:          cfg.CategoryPaths,
		OnComplete:             cfg.OnComplete,
		IPFilterPath:           cfg.IPFilterPath,
		IPFilterURL:            cfg.IPFilterURL,
		IPFilterFormat:         cfg.IPFilterFormat,
		IPFilterUpdateInterval: ipFilterUpdateInterval,
		ProxyType:              cfg.ProxyType,
		ProxyAddress:           cfg.ProxyAddress,
		ProxyUsername:          cfg.ProxyUsername,
		ProxyPassword:          cfg.ProxyPassword,
		ProxyDNS:               cfg.ProxyDNS,
		AnonymousMode:          cfg.AnonymousMode,
	}
	if cfg.Sequential {
		defaults.PickerStrategy = picker.Sequential
	}
	if cfg.DownLimitKB > 0 {
		defaults.DownLimit = ratelimit.New(int64(cfg.DownLimitKB) * 1024)
	}
	if cfg.UpLimitKB > 0 {
		defaults.UpLimit = ratelimit.New(int64(cfg.UpLimitKB) * 1024)
	}
	if cfg.AltSchedule != "" {
		sched, err := engine.ParseSchedule(cfg.AltSchedule)
		if err != nil {
			return engine.Defaults{}, fmt.Errorf("altSchedule: %w", err)
		}
		defaults.AltSchedule = &sched
	}
	return defaults, nil
}

func parseOptionalDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func parseContentLayout(s string) (storage.ContentLayout, error) {
	switch s {
	case "", "original":
		return storage.LayoutOriginal, nil
	case "subfolder":
		return storage.LayoutSubfolder, nil
	case "no-subfolder":
		return storage.LayoutNoSubfolder, nil
	default:
		return 0, fmt.Errorf(`%q is not one of "original", "subfolder", "no-subfolder"`, s)
	}
}

func parseSeedLimitAction(s string) (engine.SeedLimitAction, error) {
	switch s {
	case "pause", "":
		return engine.SeedLimitActionPause, nil
	case "remove":
		return engine.SeedLimitActionRemove, nil
	case "remove-delete-data":
		return engine.SeedLimitActionRemoveDeleteData, nil
	default:
		return "", fmt.Errorf(`%q is not one of "pause", "remove", "remove-delete-data"`, s)
	}
}
