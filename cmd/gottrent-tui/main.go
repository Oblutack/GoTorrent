// Command gottrent-tui is a terminal UI for a running gottrentd — for a
// headless box or an SSH session where a GUI isn't an option, sharing the
// exact same REST+WS control API (internal/api) Desktop already talks to.
// All the real logic lives in internal/tui/internal/tuiclient; this is
// just flag parsing and a call to tui.Run, the same thin-main.go shape
// cmd/gottrent-sim already established for a small standalone binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Oblutack/GoTorrent/internal/tui"
)

func main() {
	var (
		apiAddress = flag.String("api-address", "127.0.0.1:6880", "gottrentd's control API address")
		token      = flag.String("token", "", "bearer token (default: read from the same api-token file gottrentd itself writes)")
		theme      = flag.String("theme", "", "colour theme (default: the one used last time; ctrl+t or t cycles themes inside the program)")
		listThemes = flag.Bool("list-themes", false, "print the available colour themes and exit")
	)
	flag.Parse()

	if *listThemes {
		fmt.Println(strings.Join(tui.ThemeNames(), "\n"))
		return
	}
	if *theme != "" && !tui.KnownTheme(*theme) {
		fmt.Fprintf(os.Stderr, "gottrent-tui: unknown theme %q; available: %s\n", *theme, strings.Join(tui.ThemeNames(), ", "))
		os.Exit(2)
	}

	if *token == "" {
		*token = readDefaultToken()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := tui.Run(ctx, *apiAddress, *token, *theme); err != nil {
		fmt.Fprintln(os.Stderr, "gottrent-tui:", err)
		os.Exit(1)
	}
}

// readDefaultToken reads (never creates) gottrentd's own default bearer-
// token file — os.UserConfigDir()/GoTorrent/api-token, the exact path
// internal/api.LoadOrCreateToken writes to when cmd/gottrentd is given no
// -config/-state-dir override, the same path Desktop's own
// IDaemonLauncher.TryReadExistingToken reads for the identical reason: a
// client has no business minting a token gottrentd itself doesn't know
// about, only reading whatever real one is already there. A missing file
// just means -token was needed and wasn't given — reported once the
// connect screen actually tries to use the empty token, not here.
func readDefaultToken() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(base, "GoTorrent", "api-token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
