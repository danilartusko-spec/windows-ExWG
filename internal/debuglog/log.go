// Package debuglog stores original local diagnostics independently of the UI.
package debuglog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const FileBytes = 2 << 20
const RetainedFiles = 8
// Version is injected from /VERSION by build-flutter-windows.ps1:
//   -ldflags "-X nexusclient/internal/debuglog.Version=<x.y.z>"
// Plain `go build` / `go test` report "dev".
var Version = "dev"

var journalFileName = regexp.MustCompile(`^nexus-[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9]+-[0-9]{6}\.txt$`)

type Journal struct {
	mu       sync.Mutex
	dir      string
	file     *os.File
	size     int
	maxBytes int
	keep     int
	sequence uint64
	lastErr  error
}

func Open(dir string, maxBytes, keep int) (*Journal, error) {
	if maxBytes < 256 || keep < 2 {
		return nil, errors.New("invalid journal limits")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	j := &Journal{dir: dir, maxBytes: maxBytes, keep: keep}
	if err := j.rotate(); err != nil {
		if j.file != nil {
			_ = j.file.Close()
		}
		return nil, err
	}
	return j, nil
}

func logNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && journalFileName.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (j *Journal) rotate() error {
	if j.file != nil {
		if err := j.file.Close(); err != nil {
			return err
		}
		j.file = nil
	}
	var f *os.File
	for {
		name := fmt.Sprintf(
			"nexus-%s-%d-%06d.txt",
			time.Now().UTC().Format("20060102T150405.000000000Z"),
			os.Getpid(),
			j.sequence,
		)
		j.sequence++
		var err error
		f, err = os.OpenFile(filepath.Join(j.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	j.file, j.size = f, 0
	names, err := logNames(j.dir)
	if err != nil {
		return err
	}
	for len(names) > j.keep {
		if err := os.Remove(filepath.Join(j.dir, names[0])); err != nil {
			// Another live process/reader may hold a Windows file handle.
			// Report retention trouble but do not discard the current event.
			j.lastErr = fmt.Errorf("journal retention: %w", err)
		}
		names = names[1:]
	}
	return nil
}

// Write preserves the supplied message; only a timestamp/PID is added.
// Very large entries are split across files, not silently truncated.
func (j *Journal) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return 0, os.ErrClosed
	}
	data := fmt.Sprintf("%s pid=%d %s\n", time.Now().Format(time.RFC3339Nano), os.Getpid(), strings.TrimRight(string(p), "\r\n"))
	for len(data) > 0 {
		if j.size >= j.maxBytes {
			if err := j.rotate(); err != nil {
				j.lastErr = err
				return 0, err
			}
		}
		n := min(len(data), j.maxBytes-j.size)
		// Do not split a UTF-8 codepoint at a rotation boundary.
		for n > 0 && n < len(data) && !utf8.RuneStart(data[n]) {
			n--
		}
		if n == 0 {
			j.size = j.maxBytes
			continue
		}
		written, err := io.WriteString(j.file, data[:n])
		j.size += written
		if err == nil && written != n {
			err = io.ErrShortWrite
		}
		if err != nil {
			j.lastErr = err
			return 0, err
		}
		data = data[n:]
	}
	return len(p), nil
}

func (j *Journal) Sync() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return os.ErrClosed
	}
	err := j.file.Sync()
	if err != nil {
		j.lastErr = err
	}
	return err
}

func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := errors.Join(j.file.Sync(), j.file.Close())
	j.file = nil
	return err
}

func (j *Journal) LastError() error { j.mu.Lock(); defer j.mu.Unlock(); return j.lastErr }

// Snapshot returns chronological retained files. tailBytes=0 exports all files;
// a bounded tail is used only for the on-screen preview.
func Snapshot(dir string, tailBytes int) (string, error) {
	names, err := logNames(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "[journal not created yet]\n", nil
	}
	if err != nil {
		return "", err
	}
	var parts []string
	remaining := tailBytes
	for i := len(names) - 1; i >= 0; i-- {
		f, err := os.Open(filepath.Join(dir, names[i]))
		if errors.Is(err, os.ErrNotExist) {
			continue
		} // rotated while reading
		if err != nil {
			return "", err
		}
		stat, err := f.Stat()
		if err != nil {
			f.Close()
			return "", err
		}
		start := int64(0)
		if tailBytes > 0 && stat.Size() > int64(remaining) {
			start = stat.Size() - int64(remaining)
		}
		_, seekErr := f.Seek(start, io.SeekStart)
		data, readErr := io.ReadAll(io.LimitReader(f, FileBytes+1))
		closeErr := f.Close()
		if err := errors.Join(seekErr, readErr, closeErr); err != nil {
			return "", err
		}
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			data = data[1:]
		}
		parts = append(parts, "\n--- "+names[i]+" ---\n"+string(data))
		if tailBytes > 0 {
			remaining -= len(data)
			if remaining <= 0 {
				break
			}
		}
	}
	var out strings.Builder
	if tailBytes > 0 {
		out.WriteString("[preview: recent log tail; TXT export includes all retained files]\n")
	}
	for i := len(parts) - 1; i >= 0; i-- {
		out.WriteString(parts[i])
	}
	return out.String(), nil
}

// ErrorDetail keeps original text, concrete types, numeric OS codes and all
// branches of errors.Join. Go errors do not inherently contain origin stacks.
func ErrorDetail(err error) string {
	if err == nil {
		return "<nil>"
	}
	var out strings.Builder
	var visit func(error, int)
	visit = func(e error, depth int) {
		if e == nil {
			return
		}
		if depth > 24 {
			out.WriteString("[error unwrap depth limit]\n")
			return
		}
		fmt.Fprintf(&out, "%s%T: %+v\n", strings.Repeat("  ", depth), e, e)
		if code, ok := e.(syscall.Errno); ok {
			fmt.Fprintf(&out, "%scode=%d hex=0x%08X\n", strings.Repeat("  ", depth), uint64(code), uint64(code))
		}
		switch e := e.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range e.Unwrap() {
				visit(child, depth+1)
			}
		case interface{ Unwrap() error }:
			visit(e.Unwrap(), depth+1)
		}
	}
	visit(err, 0)
	return out.String()
}

var defaultMu sync.RWMutex
var defaultJournal *Journal

func SetDefault(j *Journal) { defaultMu.Lock(); defaultJournal = j; defaultMu.Unlock() }
func Default() *Journal     { defaultMu.RLock(); defer defaultMu.RUnlock(); return defaultJournal }
func Enabled() bool         { return Default() != nil }
func Event(stage, format string, args ...any) {
	if j := Default(); j != nil {
		_, _ = fmt.Fprintf(j, "[%s] %s", stage, fmt.Sprintf(format, args...))
	}
}
func Error(stage string, err error) {
	if err == nil {
		return
	}
	if j := Default(); j != nil {
		_, _ = fmt.Fprintf(j, "[ERROR %s]\n%s\ncapture stack (logging point, not error origin):\n%s", stage, ErrorDetail(err), debug.Stack())
		_ = j.Sync()
	}
}

// WriteTXT commits a UTF-8/BOM text export only once it is fully written.
func WriteTXT(path, text string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".nexus-export-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	_, writeErr := io.WriteString(f, "\ufeff"+strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n"))
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
