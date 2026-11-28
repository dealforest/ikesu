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
	if filepath.Base(destPath) == ".git" {
		return false
	}
	stat := os.Stat
	if noFollow && !strings.HasSuffix(destination, "/") {
		stat = os.Lstat
	}
	info, err := stat(destPath)
	intoDirectory := err == nil && info.IsDir()
	if intoDirectory {
		resolved := physical(destPath)
		if resolved == physical(i.Env["HOME"]) || filepath.Base(resolved) == ".git" {
			return false
		}
	}
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
			if !safeMoveEntry(i, target, probes) {
				return false
			}
			if intoDirectory && !safeMoveEntry(i, filepath.Join(destination, filepath.Base(target)), probes) {
				return false
			}
		}
	}
	return intoDirectory || sourceCount == 1 && safeMoveEntry(i, destination, probes)
}

// mv changes a directory entry. Resolve the containing directory, even when
// the entry being moved or replaced is itself a symlink.
func safeMoveEntry(i Input, operand string, probes map[string]string) bool {
	if strings.Contains(operand, "..") {
		return false
	}
	path := operand
	if !filepath.IsAbs(path) {
		path = filepath.Join(i.Cwd, path)
	}
	path = filepath.Clean(path)
	parent := guardPhysical(filepath.Dir(path))
	path = filepath.Join(parent, filepath.Base(path))
	if path == physical(i.LaunchCwd) || path == physical(i.Env["HOME"]) || filepath.Base(path) == ".git" {
		return false
	}

	entry, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return false
	}
	link := err == nil && entry.Mode()&os.ModeSymlink != 0
	temporary := strings.Contains(path, "/.artifacts/") || strings.HasSuffix(path, "/.artifacts")
	for _, root := range []string{"/tmp", "/private/tmp", "/var/folders", "/private/var/folders", i.Env["TMPDIR"]} {
		if root != "" {
			root = guardPhysical(root)
			temporary = temporary || path == root || strings.HasPrefix(path, root+"/")
		}
	}
	if temporary {
		if !link {
			return !containsRegisteredWorktree(i, path, probes) && safeTemporaryRemoval(i, path, probes)
		}
		return safeTemporaryParent(i, parent, probes)
	}
	if !link {
		return safeRemovalWithProbes(i, path, probes)
	}

	// A symlink cannot contain a worktree. Check the repository containing
	// its parent using the same probes and private-worktree boundary as rm.
	if i.LaunchCwd == "" || physical(i.LaunchCwd) == physical(i.Env["HOME"]) {
		return false
	}
	input := removalProbeInput(i)
	input.probePrivate = false
	if i.probePrivate && parent != guardPhysical(i.Cwd) {
		var ok bool
		parent, ok = privateRemovalWorktree(input, parent, probes)
		if !ok {
			return false
		}
	}
	common := func(dir string) string {
		key := dir + "\x00--git-common-dir"
		if value, ok := probes[key]; ok {
			return value
		}
		value := probe(input, "git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
		probes[key] = value
		return value
	}
	launch, target := common(physical(i.LaunchCwd)), common(parent)
	if launch == "" || target == "" || physical(launch) != physical(target) {
		return false
	}
	top := probe(input, "git", "-C", parent, "rev-parse", "--show-toplevel")
	return top != "" && strings.HasPrefix(path, physical(top)+"/") && path != filepath.Join(physical(top), ".git")
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
