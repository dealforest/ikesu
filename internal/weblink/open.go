package weblink

import (
	"fmt"
	"os/exec"
	"runtime"
)

func Open(url string) error {
	if !Valid(url) {
		return fmt.Errorf("invalid HTTP(S) link")
	}
	var path string
	switch runtime.GOOS {
	case "darwin":
		path = "/usr/bin/open"
	case "linux":
		path = "xdg-open"
	default:
		return fmt.Errorf("opening links is unsupported on %s", runtime.GOOS)
	}
	cmd := exec.Command(path, url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
