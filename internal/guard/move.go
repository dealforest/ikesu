package guard

import (
	"os"
	"path/filepath"
	"strings"
)

func safeMove(i Input, args []string) bool {
	if i.UnresolvedArg {
		return false
	}
	sources, destination, explicitTarget, noFollow, ok := moveOperands(args)
	if !ok || strings.Contains(destination, "..") {
		return false
	}
	destinations, ok := expandRemovalArg(i, destination)
	if !ok || len(destinations) != 1 {
		return false
	}
	destination = destinations[0]
	destPath := destination
	if !filepath.IsAbs(destPath) {
		destPath = filepath.Join(i.Cwd, destPath)
	}
	// The worktree root can be a container (mv a .). HOME and .git are
	// protected even when used as containers.
	resolved := physical(destPath)
	if resolved == physical(i.Env["HOME"]) || filepath.Base(destPath) == ".git" || filepath.Base(resolved) == ".git" {
		return false
	}
	stat := os.Stat
	if noFollow && !strings.HasSuffix(destination, "/") {
		stat = os.Lstat
	}
	info, err := stat(destPath)
	intoDirectory := err == nil && info.IsDir()
	if !intoDirectory && (explicitTarget || err != nil && !os.IsNotExist(err)) {
		return false
	}

	probes := map[string]string{}
	sourceCount := 0
	for _, source := range sources {
		targets, ok := expandRemovalArg(i, source)
		if !ok {
			return false
		}
		for _, target := range targets {
			sourceCount++
			if !safeRemovalWithProbes(i, target, probes) {
				return false
			}
			if intoDirectory && !safeRemovalWithProbes(i, filepath.Join(destination, filepath.Base(target)), probes) {
				return false
			}
		}
	}
	return intoDirectory || sourceCount == 1 && safeRemovalWithProbes(i, destination, probes)
}

func moveOperands(args []string) (sources []string, destination string, explicitTarget, noFollow, ok bool) {
nextArg:
	for len(args) > 0 {
		token := args[0]
		args = args[1:]
		// BSD mv stops parsing flags at the first operand. When a later
		// argument looks like a flag, the affected paths depend on mv.
		if len(sources) > 0 && token != "-" && strings.HasPrefix(token, "-") {
			return nil, "", false, false, false
		}
		switch {
		case token == "--":
			sources = append(sources, args...)
			args = nil
		case token == "-" || !strings.HasPrefix(token, "-"):
			sources = append(sources, token)
		case strings.HasPrefix(token, "--"):
			name, value, attached := strings.Cut(token, "=")
			switch name {
			case "--force", "--interactive", "--no-clobber", "--verbose":
				if attached {
					return nil, "", false, false, false
				}
			case "--target-directory", "--suffix":
				if !attached {
					if len(args) == 0 {
						return nil, "", false, false, false
					}
					value, args = args[0], args[1:]
				}
				if name == "--target-directory" {
					if explicitTarget {
						return nil, "", false, false, false
					}
					destination, explicitTarget = value, true
				}
			default:
				return nil, "", false, false, false
			}
		default:
			for flags := token[1:]; flags != ""; flags = flags[1:] {
				switch flags[0] {
				case 'f', 'i', 'n', 'v':
				case 'h':
					noFollow = true
				case 't', 'S':
					value := flags[1:]
					if value == "" {
						if len(args) == 0 {
							return nil, "", false, false, false
						}
						value, args = args[0], args[1:]
					}
					if flags[0] == 't' {
						if explicitTarget {
							return nil, "", false, false, false
						}
						destination, explicitTarget = value, true
					}
					continue nextArg
				default:
					return nil, "", false, false, false
				}
			}
		}
	}
	if !explicitTarget {
		if len(sources) < 2 {
			return nil, "", false, false, false
		}
		destination, sources = sources[len(sources)-1], sources[:len(sources)-1]
	}
	for _, source := range sources {
		if source == "" {
			return nil, "", false, false, false
		}
	}
	return sources, destination, explicitTarget, noFollow, len(sources) > 0 && destination != ""
}
