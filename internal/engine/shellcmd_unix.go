//go:build !windows

package engine

import "os/exec"

// shellCommand runs command through /bin/sh -c, letting Go's normal POSIX
// argument passing do the right thing (no analogue of the Windows
// cmd.exe/CmdLine quoting mismatch — see shellcmd_windows.go).
//
// command is always local operator configuration (Defaults.OnComplete, set
// via -on-complete or gottrentd's own JSON config), never derived from
// network input — the same "let the local operator run whatever they
// configure" shape as cron, systemd's ExecStart, or any other download
// tool's "run a script on complete" hook. gosec's G204 flags any subprocess
// built from a variable regardless of where that variable came from, which
// is the right default but not applicable here.
func shellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command) // #nosec G204 -- see doc comment above
}
