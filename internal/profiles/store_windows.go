//go:build windows

package profiles

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const defaultRouting = "https://routing.help/"

const routingSettingsFile = "routing-settings.dat"

type Profile struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Config           string    `json:"config"`
	Routing          string    `json:"routing"`
	RoutingOverrides string    `json:"routingOverrides,omitempty"`
	CreatedAt        time.Time `json:"createdAt,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// RoutingSettings are shared by every VPN profile. Keeping them in their own
// DPAPI-protected record prevents selecting or importing a server from making
// the user's local routing rules appear to disappear.
type RoutingSettings struct {
	Source    string `json:"source"`
	Overrides string `json:"overrides,omitempty"`
}

type Store struct {
	directory string
}

func NewStore() (*Store, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		return nil, errors.New("Windows не сообщила папку LOCALAPPDATA")
	}
	directory := filepath.Join(base, "NexusClient", "Profiles")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	return &Store{directory: directory}, nil
}

func (store *Store) LoadAll() ([]Profile, error) {
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(entries))
	var joined error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".nexus") {
			continue
		}
		profile, err := store.load(filepath.Join(store.directory, entry.Name()))
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		profiles = append(profiles, profile)
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i].CreatedAt.Equal(profiles[j].CreatedAt) {
			return profiles[i].ID > profiles[j].ID
		}
		return profiles[i].CreatedAt.After(profiles[j].CreatedAt)
	})
	return profiles, joined
}

func (store *Store) Save(profile *Profile) error {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Config = strings.TrimSpace(profile.Config)
	profile.Routing = strings.TrimSpace(profile.Routing)
	profile.RoutingOverrides = strings.TrimSpace(profile.RoutingOverrides)
	if profile.Name == "" {
		return errors.New("задайте название профиля")
	}
	if profile.Config == "" {
		return errors.New("AWG-конфигурация пуста")
	}
	if profile.Routing == "" {
		profile.Routing = defaultRouting
	}
	if profile.ID == "" {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		profile.ID = hex.EncodeToString(id)
	}
	now := time.Now()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	plain, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	protected, err := protect(plain)
	if err != nil {
		return fmt.Errorf("не удалось зашифровать профиль: %w", err)
	}
	path := filepath.Join(store.directory, profile.ID+".nexus")
	temporary := path + ".new"
	if err := os.WriteFile(temporary, protected, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (store *Store) LoadSelected() (string, error) {
	data, err := os.ReadFile(filepath.Join(store.directory, "selected-profile.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (store *Store) SaveSelected(id string) error {
	path := filepath.Join(store.directory, "selected-profile.txt")
	temporary := path + ".new"
	if err := os.WriteFile(temporary, []byte(strings.TrimSpace(id)), 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (store *Store) LoadRoutingSettings() (RoutingSettings, bool, error) {
	protected, err := os.ReadFile(filepath.Join(store.directory, routingSettingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return RoutingSettings{}, false, nil
	}
	if err != nil {
		return RoutingSettings{}, false, err
	}
	plain, err := unprotect(protected)
	if err != nil {
		return RoutingSettings{}, false, fmt.Errorf("настройки маршрутизации принадлежат другому пользователю Windows или повреждены: %w", err)
	}
	var settings RoutingSettings
	if err := json.Unmarshal(plain, &settings); err != nil {
		return RoutingSettings{}, false, err
	}
	settings.Source = strings.TrimSpace(settings.Source)
	settings.Overrides = strings.TrimSpace(settings.Overrides)
	if settings.Source == "" {
		settings.Source = defaultRouting
	}
	return settings, true, nil
}

func (store *Store) SaveRoutingSettings(settings RoutingSettings) error {
	settings.Source = strings.TrimSpace(settings.Source)
	settings.Overrides = strings.TrimSpace(settings.Overrides)
	if settings.Source == "" {
		settings.Source = defaultRouting
	}
	plain, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	protected, err := protect(plain)
	if err != nil {
		return fmt.Errorf("не удалось зашифровать настройки маршрутизации: %w", err)
	}
	path := filepath.Join(store.directory, routingSettingsFile)
	temporary := path + ".new"
	if err := os.WriteFile(temporary, protected, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (store *Store) Delete(profile Profile) error {
	if profile.ID == "" {
		return nil
	}
	err := os.Remove(filepath.Join(store.directory, profile.ID+".nexus"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (store *Store) load(path string) (Profile, error) {
	protected, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	plain, err := unprotect(protected)
	if err != nil {
		return Profile{}, fmt.Errorf("профиль принадлежит другому пользователю Windows или повреждён: %w", err)
	}
	var profile Profile
	if err := json.Unmarshal(plain, &profile); err != nil {
		return Profile{}, err
	}
	if profile.ID == "" || profile.Name == "" || profile.Config == "" {
		return Profile{}, errors.New("неполный профиль")
	}
	if profile.Routing == "" {
		profile.Routing = defaultRouting
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = profile.UpdatedAt
	}
	return profile, nil
}

func protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("нет данных для шифрования")
	}
	input := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var output windows.DataBlob
	description, _ := windows.UTF16PtrFromString("Nexus Client AWG profile")
	if err := windows.CryptProtectData(&input, description, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func unprotect(protected []byte) ([]byte, error) {
	if len(protected) == 0 {
		return nil, errors.New("зашифрованный файл пуст")
	}
	input := windows.DataBlob{Size: uint32(len(protected)), Data: &protected[0]}
	var output windows.DataBlob
	var description *uint16
	if err := windows.CryptUnprotectData(&input, &description, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	if description != nil {
		defer windows.LocalFree(windows.Handle(unsafe.Pointer(description)))
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
