//go:build darwin

package platform

import (
	"strconv"
	"strings"

	"github.com/kazuph/ikesu/internal/childenv"
)

// OpenRegularFiles returns paths held by numeric file descriptors only.
func OpenRegularFiles(pid int) ([]string, error) {
	output, err := childenv.Command("/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-Fftn").Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	numeric, regular := false, false
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case 'f':
			_, err := strconv.Atoi(line[1:])
			numeric = err == nil && line[1:] != ""
			regular = false
		case 't':
			regular = line[1:] == "REG"
		case 'n':
			if numeric && regular {
				paths = append(paths, line[1:])
			}
		}
	}
	return paths, nil
}

// OpenJSONLFiles keeps the original JSONL discovery semantics for Codex.
func OpenJSONLFiles(pid int) []string {
	output, err := childenv.Command("/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "n") && strings.HasSuffix(line, ".jsonl") {
			paths = append(paths, line[1:])
		}
	}
	return paths
}
