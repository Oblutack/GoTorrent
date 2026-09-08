//go:build !windows

package engine

import "os/exec"

// shellCommand runs command through /bin/sh -c, letting Go's normal POSIX
// argument passing do the right thing (no analogue of the Windows
// cmd.exe/CmdLine quoting mismatch — see shellcmd_windows.go).
func shellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}
