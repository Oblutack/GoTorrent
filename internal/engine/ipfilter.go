package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/ipfilter"
	"github.com/Oblutack/GoTorrent/internal/logger"
)

// defaultIPFilterUpdateInterval is used when Defaults.IPFilterUpdateInterval
// is unset but Defaults.IPFilterURL is — a blocklist is not going to
// meaningfully change more often than daily.
const defaultIPFilterUpdateInterval = 24 * time.Hour

// StartIPFilter loads Defaults.IPFilterPath (if set) into the engine's
// shared filter (Engine.ipFilter — already handed to every torrent's
// Config.IPFilter and checked by handleIncoming regardless of whether this
// is ever called; StartIPFilter only ever populates it) and, if
// Defaults.IPFilterURL is also set, starts a background loop re-fetching
// it on IPFilterUpdateInterval. A local-path load failure is returned
// directly (the caller asked for a specific file — treat it like
// Listen/StartDHT's own "asked for something specific, report if it
// didn't work" contract); a URL fetch failure, since it can only ever
// happen well after startup, is logged instead.
func (e *Engine) StartIPFilter(ctx context.Context) error {
	if e.defaults.IPFilterPath != "" {
		format := e.defaults.IPFilterFormat
		if format == "" {
			format = detectIPFilterFormat(e.defaults.IPFilterPath)
		}
		if err := loadIPFilterSource(e.ipFilter, format, openFile(e.defaults.IPFilterPath)); err != nil {
			return fmt.Errorf("engine: loading IP filter %s: %w", e.defaults.IPFilterPath, err)
		}
		logger.Logf("engine: IP filter loaded %d ranges from %s\n", e.ipFilter.Count(), e.defaults.IPFilterPath)
	}
	if e.defaults.IPFilterURL != "" {
		go e.ipFilterUpdateLoop(ctx)
	}
	return nil
}

func (e *Engine) ipFilterUpdateLoop(ctx context.Context) {
	interval := e.defaults.IPFilterUpdateInterval
	if interval <= 0 {
		interval = defaultIPFilterUpdateInterval
	}

	format := e.defaults.IPFilterFormat
	if format == "" {
		format = detectIPFilterFormat(e.defaults.IPFilterURL)
	}
	fetch := func() {
		open := openHTTP(ctx, e.defaults.IPFilterURL)
		if err := loadIPFilterSource(e.ipFilter, format, open); err != nil {
			logger.Warning.Printf("engine: IP filter auto-update from %s: %v\n", e.defaults.IPFilterURL, err)
			return
		}
		logger.Logf("engine: IP filter auto-updated, %d ranges from %s\n", e.ipFilter.Count(), e.defaults.IPFilterURL)
	}

	fetch()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fetch()
		}
	}
}

// openFile and openHTTP adapt a local path / an HTTP GET to the same
// func() (io.ReadCloser, error) shape, so loadIPFilterSource loads a
// blocklist from either without knowing which it got.
func openFile(path string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return os.Open(path) }
}

func openHTTP(ctx context.Context, url string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %s", resp.Status)
		}
		return resp.Body, nil
	}
}

// loadIPFilterSource opens a blocklist via open and parses it as "p2p"
// (PeerGuardian) or, for anything else, "dat" (eMule) — see
// detectIPFilterFormat for how a caller resolves format when
// Defaults.IPFilterFormat itself is empty — then loads the result into
// filter.
func loadIPFilterSource(filter *ipfilter.Filter, format string, open func() (io.ReadCloser, error)) error {
	f, err := open()
	if err != nil {
		return err
	}
	defer f.Close()

	var ranges []ipfilter.Range
	if format == "p2p" {
		ranges = ipfilter.ParsePeerGuardianP2P(f)
	} else {
		ranges = ipfilter.ParseEmuleDAT(f)
	}
	filter.Load(ranges)
	return nil
}

// detectIPFilterFormat guesses a blocklist's format from its path or URL's
// extension, defaulting to eMule's ("dat") — by far the more common format
// in the wild — when the extension is anything else or missing.
func detectIPFilterFormat(source string) string {
	if strings.HasSuffix(strings.ToLower(source), ".p2p") {
		return "p2p"
	}
	return "dat"
}
