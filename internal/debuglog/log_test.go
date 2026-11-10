package debuglog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unicode/utf8"
)

func TestPersistenceAcrossRestartAndDisconnect(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(dir, FileBytes, RetainedFiles)
	if err != nil {
		t.Fatal(err)
	}
	original := "connect failed\noriginal error: UDP server.example:44123 код=5\nstack: first\n  second"
	if _, err := j.Write([]byte(original)); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = Open(dir, FileBytes, RetainedFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, err := j.Write([]byte("disconnect; next launch")); err != nil {
		t.Fatal(err)
	}
	text, err := Snapshot(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, original) || !strings.Contains(text, "disconnect; next launch") {
		t.Fatal("history was replaced")
	}
}

func TestRotationRetentionAndUnicode(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(dir, 256, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for i := 0; i < 18; i++ {
		if _, err := j.Write([]byte(fmt.Sprintf("entry-%02d %s", i, strings.Repeat("Ошибка 🌍 ", 16)))); err != nil {
			t.Fatal(err)
		}
	}
	names, err := logNames(dir)
	if err != nil || len(names) != 3 {
		t.Fatalf("files=%v error=%v", names, err)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 256 || !utf8.Valid(data) {
			t.Fatalf("invalid rotated UTF-8 or size=%d", len(data))
		}
	}
	text, err := Snapshot(dir, 0)
	if err != nil || !strings.Contains(text, "entry-17") || strings.Contains(text, "entry-00") {
		t.Fatalf("rotation: %v\n%s", err, text)
	}
}

func TestOversizedEntryIsSplitNotLost(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(dir, 256, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	payload := strings.Repeat("line\n", 90) + "END_OF_STACK"
	if _, err := j.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	names, _ := logNames(dir)
	var raw strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		raw.Write(data)
	}
	if !strings.Contains(raw.String(), payload) {
		t.Fatal("entry was truncated")
	}
}

func TestConcurrentAppendAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(dir, FileBytes, RetainedFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			for n := 0; n < 40; n++ {
				if _, err := j.Write([]byte(fmt.Sprintf("unique-%d-%d", id, n))); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		if _, err := Snapshot(dir, 1024); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
	text, err := Snapshot(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, "unique-") != 320 {
		t.Fatal("lost/interleaved events")
	}
}

func TestOriginalErrorsAndJoinedCodes(t *testing.T) {
	err := errors.Join(fmt.Errorf("SCM.OpenService: %w", syscall.Errno(5)), fmt.Errorf("dial udp server.example:5533: %w", syscall.Errno(1231)))
	detail := ErrorDetail(err)
	for _, want := range []string{"SCM.OpenService", "syscall.Errno", "code=5 hex=0x00000005", "server.example:5533", "code=1231"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing %q: %s", want, detail)
		}
	}
}

func TestOriginalStackAndCaptureStackPersist(t *testing.T) {
	j, err := Open(t.TempDir(), FileBytes, RetainedFiles)
	if err != nil {
		t.Fatal(err)
	}
	SetDefault(j)
	defer func() { SetDefault(nil); j.Close() }()
	Error("test.stage", errors.New("original\noriginal stack line\nnext line"))
	text, err := Snapshot(j.dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"original\noriginal stack line\nnext line", "capture stack (logging point, not error origin)", "TestOriginalStackAndCaptureStackPersist"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestUTF8TXTExportAndTail(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(dir, FileBytes, RetainedFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	_, _ = j.Write([]byte(strings.Repeat("старое\n", 500) + "LAST_ORIGINAL_ERROR"))
	tail, err := Snapshot(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	full, err := Snapshot(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tail, "LAST_ORIGINAL_ERROR") || !utf8.ValidString(tail) || len(full) <= len(tail) {
		t.Fatal("incorrect preview/export")
	}
	path := filepath.Join(dir, "отчёт.txt")
	if err := WriteTXT(path, "ошибка\r\nline2\nline3"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "\ufeffошибка\r\nline2\r\nline3" {
		t.Fatalf("invalid TXT: %q", data)
	}
	if err := WriteTXT(path, "new"); err != nil {
		t.Fatal(err)
	}
	if err := WriteTXT(filepath.Join(dir, "missing", "test.txt"), "not saved"); err == nil {
		t.Fatal("export failure hidden")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".nexus-export-*"))
	if len(leftovers) > 0 {
		t.Fatal("temporary exports leaked")
	}
}

func TestRotationLeavesUnrelatedFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	keepPath := filepath.Join(dir, "nexus-user-saved-report.txt")
	if err := os.WriteFile(keepPath, []byte("user export"), 0600); err != nil {
		t.Fatal(err)
	}
	j, err := Open(dir, 256, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for i := 0; i < 10; i++ {
		if _, err := j.Write([]byte(strings.Repeat("x", 200))); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(keepPath)
	if err != nil || string(data) != "user export" {
		t.Fatal("rotation touched unrelated file")
	}
}

func TestDisabledByDefaultUntilEnabled(t *testing.T) {
	SetDefault(nil)
	if Enabled() {
		t.Fatal("debuglog must be disabled when default journal is nil")
	}
	Event("stage", "dropped")
	Error("stage", errors.New("dropped"))

	dir := t.TempDir()
	j, err := Open(dir, FileBytes, RetainedFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		SetDefault(nil)
		_ = j.Close()
	}()
	SetDefault(j)
	if !Enabled() {
		t.Fatal("debuglog must be enabled when default journal is set")
	}
	Event("stage", "recorded")

	SetDefault(nil)
	if Enabled() {
		t.Fatal("debuglog must be disabled after SetDefault(nil)")
	}
	Event("stage", "dropped-after")

	text, err := Snapshot(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "[stage] recorded") {
		t.Fatalf("missing recorded event in snapshot: %s", text)
	}
	if strings.Contains(text, "dropped") {
		t.Fatalf("events while disabled must not be recorded: %s", text)
	}
}
