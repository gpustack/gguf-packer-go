package osx

import (
	"errors"
	"os/exec"
	"runtime"
)

// OpenBrowser opens the default web browser to the specified URL.
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
	}
	return errors.New("unsupported platform")
}
