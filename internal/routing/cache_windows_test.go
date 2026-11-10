//go:build windows

package routing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoutingCacheRoundTripAndAtomicReplacement(t *testing.T) {
	cache := &Cache{path: filepath.Join(t.TempDir(), "profile.nroute")}
	source := "https://routing.example/profile"
	first := &Bundle{Profile: &Profile{Name: "first", GlobalProxy: true}}
	firstTime := time.Now().Add(-time.Hour).Round(time.Second)
	if err := cache.save(source, firstTime, first); err != nil {
		t.Fatal(err)
	}
	loaded, loadedAt, err := cache.load(source)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile.Name != "first" || !loadedAt.Equal(firstTime) {
		t.Fatalf("unexpected first cache value: %q, %v", loaded.Profile.Name, loadedAt)
	}

	second := &Bundle{Profile: &Profile{Name: "second", GlobalProxy: true}}
	secondTime := time.Now().Round(time.Second)
	if err := cache.save(source, secondTime, second); err != nil {
		t.Fatal(err)
	}
	loaded, loadedAt, err = cache.load(source)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile.Name != "second" || !loadedAt.Equal(secondTime) {
		t.Fatalf("unexpected replaced cache value: %q, %v", loaded.Profile.Name, loadedAt)
	}
	if _, _, err := cache.load("https://other.example/profile"); err == nil {
		t.Fatal("cache from another source was accepted")
	}
}

func TestRoutingCacheDeleteRemovesCurrentAndPrevious(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	cache, err := NewCache("delete-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache.path, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache.path+".previous", []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cache.Delete(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cache.path, cache.path + ".previous"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cache file still exists: %s", path)
		}
	}
}

func TestRoutingCacheStoreCopiesValidatedBundle(t *testing.T) {
	cache := &Cache{path: filepath.Join(t.TempDir(), "copied.nroute")}
	source := "https://routing.example/profile"
	updatedAt := time.Now().Add(-2 * time.Hour).Round(time.Second)
	bundle := &Bundle{Profile: &Profile{Name: "shared", GlobalProxy: true}}
	if err := cache.Store(source, updatedAt, bundle); err != nil {
		t.Fatal(err)
	}
	loaded, loadedAt, err := cache.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile.Name != "shared" || !loadedAt.Equal(updatedAt) {
		t.Fatalf("loaded=%q at %v", loaded.Profile.Name, loadedAt)
	}
}
