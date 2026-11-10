//go:build windows

package profiles

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDPAPIRoundTrip(t *testing.T) {
	source := []byte("private profile material")
	protected, err := protect(source)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, source) {
		t.Fatal("protected output contains plaintext")
	}
	actual, err := unprotect(protected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, source) {
		t.Fatalf("got %q, want %q", actual, source)
	}
}

func TestStoreKeepsNewestAddedOrderAndSelectedSeparately(t *testing.T) {
	store := &Store{directory: t.TempDir()}
	older := Profile{
		Name:      "Older",
		Config:    "[Interface]\nPrivateKey = older",
		CreatedAt: time.Now().Add(-time.Hour),
	}
	newer := Profile{
		Name:      "Newer",
		Config:    "[Interface]\nPrivateKey = newer",
		CreatedAt: time.Now(),
	}
	if err := store.Save(&older); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&newer); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSelected(older.ID); err != nil {
		t.Fatal(err)
	}
	selected, err := store.LoadSelected()
	if err != nil || selected != older.ID {
		t.Fatalf("selected=%q err=%v", selected, err)
	}
	items, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != newer.ID || items[1].ID != older.ID {
		t.Fatalf("unexpected order: %#v", items)
	}
	older.Name = "Edited"
	if err := store.Save(&older); err != nil {
		t.Fatal(err)
	}
	items, err = store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ID != newer.ID || items[1].ID != older.ID {
		t.Fatalf("editing changed insertion order: %#v", items)
	}
}

func TestRoutingSettingsRoundTripAndStayOutOfProfileList(t *testing.T) {
	store := &Store{directory: t.TempDir()}
	wanted := RoutingSettings{
		Source:    "https://routing.help/",
		Overrides: "proxy domain:updates.cdn-apple.com",
	}
	if err := store.SaveRoutingSettings(wanted); err != nil {
		t.Fatal(err)
	}
	actual, exists, err := store.LoadRoutingSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !exists || actual != wanted {
		t.Fatalf("settings=%#v exists=%t, want %#v", actual, exists, wanted)
	}
	items, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("routing settings were treated as profiles: %#v", items)
	}
}

func TestStoreSaveLoadDelete(t *testing.T) {
	store := &Store{directory: t.TempDir()}
	profile := Profile{
		Name:             "Home",
		Config:           "[Interface]\nPrivateKey = secret",
		Routing:          "https://routing.help/",
		RoutingOverrides: "direct domain:example.com",
	}
	if err := store.Save(&profile); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(store.directory, profile.ID+".nexus"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("PrivateKey")) || bytes.Contains(raw, []byte("secret")) {
		t.Fatal("profile was written in plaintext")
	}
	items, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != profile.Name || items[0].Config != profile.Config || items[0].RoutingOverrides != profile.RoutingOverrides {
		t.Fatalf("unexpected profiles: %#v", items)
	}
	if err := store.Delete(profile); err != nil {
		t.Fatal(err)
	}
	items, err = store.LoadAll()
	if err != nil || len(items) != 0 {
		t.Fatalf("profile was not deleted: %#v, %v", items, err)
	}
}
