package guard

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"

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

// walkLogLines delivers complete lines without imposing a line-size limit.
// A positive tail budget visits newer lines first and lets audit stop early.
func walkLogLines(files []*os.File, tailBudget int64, stop func([]byte) bool) (int64, error) {
	visit := func(line []byte) bool {
		line = bytes.TrimSuffix(line, []byte{'\n'})
		return stop(bytes.TrimSuffix(line, []byte{'\r'}))
	}
	if tailBudget > 0 {
		var read int64
		for index := len(files) - 1; index >= 0 && read < tailBudget; index-- {
			size, done, err := walkLogTail(files[index], tailBudget-read, visit)
			read += size
			if done || err != nil {
				return read, err
			}
		}
		return read, nil
	}
	for _, f := range files {
		reader := bufio.NewReader(f)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) != 0 && (err == nil || err == io.EOF) && visit(line) {
				return 0, nil
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return 0, err
			}
		}
	}
	return 0, nil
}

const tailChunk = 1 << 20

func walkLogTail(f *os.File, budget int64, visit func([]byte) bool) (int64, bool, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	pos := st.Size()
	var fragments [][]byte
	var read int64
	for pos > 0 && read < budget {
		size := min(tailChunk, pos, budget-read)
		pos -= size
		data := make([]byte, size)
		if _, err := f.ReadAt(data, pos); err != nil && err != io.EOF {
			return read, false, err
		}
		read += size
		for {
			separator := bytes.LastIndexByte(data, '\n')
			line := data[separator+1:]
			if separator < 0 && pos > 0 {
				fragments = append(fragments, line)
				break
			}
			if len(fragments) > 0 {
				fragments = append(fragments, line)
				slices.Reverse(fragments)
				line = bytes.Join(fragments, nil)
				fragments = nil
			}
			if len(line) != 0 && visit(line) {
				return read, true, nil
			}
			if separator < 0 {
				break
			}
			data = data[:separator]
		}
	}
	return read, false, nil
}
