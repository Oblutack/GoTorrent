package engine

import (
	"os/exec"
	"syscall"
)

// shellCommand builds a cmd.exe invocation using the raw command line
// directly (SysProcAttr.CmdLine) rather than Go's normal Args-based command
// line construction. The latter backslash-escapes embedded double quotes to
// satisfy the standard CommandLineToArgvW convention, which corrupts a
// string meant to be re-parsed by cmd.exe's own, different quoting rules —
// without this, any Defaults.OnComplete command containing a quoted path
// (the common case, since %F/%D expand to real filesystem paths) fails with
// "The filename, directory name, or volume label syntax is incorrect."
func shellCommand(command string) *exec.Cmd {
	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "/C " + command}
	return cmd
}
