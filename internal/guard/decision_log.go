package guard

import (
	"bufio"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The directory inode stays in place across rotations. Locking the log inode
// would allow a writer to wait on the archive while another writes the new log.
func lockDecisionLogDirectory(dir string, mode int) (*os.File, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), mode); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Open both generations under the same lock so rotation cannot change their
// pairing. Once opened, descriptors keep reading the same files across rename.
func openDecisionLogFiles(path string) ([]*os.File, error) {
	lock, err := lockDecisionLogDirectory(filepath.Dir(path), unix.LOCK_SH)
	switch {
	case err == nil:
		defer lock.Close()
	case os.IsNotExist(err):
		return nil, nil
	case os.IsPermission(err):
		// An explicitly supplied log may be readable in a search-only directory.
	default:
		return nil, err
	}
	var files []*os.File
	for _, name := range []string{path + ".1", path} {
		f, err := os.Open(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			closeLogFiles(files)
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func closeLogFiles(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}

func scanLogLines(files []*os.File, visit func(string)) error {
	for _, f := range files {
		s := bufio.NewScanner(f)
		for s.Scan() {
			visit(s.Text())
		}
		if err := s.Err(); err != nil {
			return err
		}
	}
	return nil
}
