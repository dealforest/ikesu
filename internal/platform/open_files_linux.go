//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"strconv"
)

// OpenRegularFiles returns paths held by numeric file descriptors only.
func OpenRegularFiles(pid int) ([]string, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		fd := filepath.Join(dir, entry.Name())
		info, err := os.Stat(fd)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		path, err := os.Readlink(fd)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// OpenJSONLFiles keeps the original JSONL discovery semantics for Codex.
func OpenJSONLFiles(pid int) []string {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		path, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if err == nil && filepath.Ext(path) == ".jsonl" {
			paths = append(paths, path)
		}
	}
	return paths
}
