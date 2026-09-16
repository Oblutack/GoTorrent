package engine

import (
	"strings"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// dispatchCompletionHook runs Defaults.OnComplete the first time hash
// reaches StateSeeding in this process run — never again for the same
// torrent, even across a later Pause/Resume or 3.4's ratio/time-limit
// auto-pause revisiting Seeding, since neither of those is "just finished"
// a second time. Called via a detached goroutine from the Add call site
// (see its own comment): os/exec can block for an arbitrary time, and this
// must never run on the torrent actor's own goroutine.
func (e *Engine) dispatchCompletionHook(hash metainfo.Hash, s torrent.State) {
	if s != torrent.StateSeeding || e.defaults.OnComplete == "" {
		return
	}

	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok || mt.completionHookFired {
		e.mu.Unlock()
		return
	}
	mt.completionHookFired = true
	command := e.defaults.OnComplete
	name := displayNameFor(mt)
	downloadDir := mt.downloadDir
	e.mu.Unlock()

	expanded := expandCompletionVars(command, name, mt.t.ContentPath(), downloadDir)
	if err := runShellCommand(expanded); err != nil {
		logger.Warning.Printf("engine: on-complete command for %s: %v\n", hash, err)
	}
}

// recordCompletedAt records the first time hash reaches StateSeeding as
// Summary.CompletedAt (Stage 5's "completedOn"), persisting it to the
// manifest immediately so it survives a restart — same "set once, guarded"
// shape as dispatchCompletionHook right above, called from the same
// detached goroutine for the same reason (must never run on the torrent
// actor's own goroutine). Guarded by mt.completedAt.IsZero() rather than a
// separate bool flag (unlike completionHookFired) — a torrent reloaded
// from the manifest with completedAt already set is naturally a no-op
// here, since the guard reads the same field Load already restored.
func (e *Engine) recordCompletedAt(hash metainfo.Hash, s torrent.State) {
	if s != torrent.StateSeeding {
		return
	}

	e.mu.Lock()
	mt, ok := e.torrents[hash]
	if !ok || !mt.completedAt.IsZero() {
		e.mu.Unlock()
		return
	}
	mt.completedAt = time.Now()
	err := e.saveManifestLocked()
	e.mu.Unlock()

	if err != nil {
		logger.Warning.Printf("engine: persisting completed-at for %s: %v\n", hash, err)
	}
}

// expandCompletionVars substitutes %N (name), %F (content path), %D
// (download directory) — the roadmap's own vocabulary for this feature.
// %% escapes a literal percent sign.
func expandCompletionVars(command, name, contentPath, downloadDir string) string {
	const escapedPercent = "\x00"
	r := strings.NewReplacer("%%", escapedPercent, "%N", name, "%F", contentPath, "%D", downloadDir)
	return strings.ReplaceAll(r.Replace(command), escapedPercent, "%")
}

// runShellCommand dispatches through the platform shell (shellCommand,
// OS-specific — see shellcmd_windows.go/shellcmd_unix.go), so a user can
// write an ordinary shell one-liner (pipes, quoting, chained commands)
// rather than a single bare argv. Waits for the command to finish — this
// already runs off the actor and engine goroutines (see the caller), so
// blocking here costs nothing but the caller's own detached goroutine.
func runShellCommand(command string) error {
	return shellCommand(command).Run()
}
