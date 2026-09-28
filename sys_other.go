//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
)

func hide(*exec.Cmd) {}

// macOS and Linux have no Apps list to join, and delete a running program without fuss.
func addUninstallEntry(string, int64) error { return nil }
func removeUninstallEntry()                 {}
func deleteOnReboot(string)                 {}

// alert shows msg in a native dialog, for when the setup's own window can't open.
func alert(title, msg string) {
	var c *exec.Cmd
	switch _, err := exec.LookPath("zenity"); {
	case runtime.GOOS == "darwin": // the text goes in as arguments, so no AppleScript quoting
		c = exec.Command("osascript", "-e", "on run argv", "-e", "display alert (item 1 of argv) message (item 2 of argv) as critical", "-e", "end run", title, msg)
	case err == nil:
		c = exec.Command("zenity", "--error", "--no-markup", "--title", title, "--text", msg)
	default:
		c = exec.Command("kdialog", "--title", title, "--error", msg)
	}
	c.Stderr = os.Stderr
	c.Run()
}
