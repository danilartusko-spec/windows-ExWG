package profileimport

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"nexusclient/internal/awg"
)

const (
	maxArchiveBytes      = 4 << 20
	maxArchiveEntries    = 128
	maxArchiveConfigs    = 20
	maxExpandedBytes     = 2 << 20
	maxConfigurationSize = 256 << 10
)

type Candidate struct {
	Name       string
	Config     string
	Generation awg.Version
}

func FromFile(path string) ([]Candidate, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".conf":
		data, err := readLimitedFile(path, maxConfigurationSize)
		if err != nil {
			return nil, err
		}
		candidate, err := validateCandidate(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), string(data))
		if err != nil {
			return nil, err
		}
		return []Candidate{candidate}, nil
	case ".zip":
		return fromZIP(path)
	case ".png", ".jpg", ".jpeg", ".gif":
		return fromQRImage(path)
	default:
		return nil, errors.New("поддерживаются .conf, .zip и изображения QR (.png/.jpg/.gif)")
	}
}

func FromText(name, configuration string) (Candidate, error) {
	return validateCandidate(name, configuration)
}

func fromZIP(path string) ([]Candidate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxArchiveBytes {
		return nil, fmt.Errorf("ZIP превышает %d МиБ", maxArchiveBytes>>20)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > maxArchiveEntries {
		return nil, fmt.Errorf("ZIP содержит больше %d записей", maxArchiveEntries)
	}
	var candidates []Candidate
	var expanded uint64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(entry.Name), ".conf") {
			continue
		}
		if len(candidates) >= maxArchiveConfigs {
			return nil, fmt.Errorf("ZIP содержит больше %d конфигураций", maxArchiveConfigs)
		}
		if entry.UncompressedSize64 > maxConfigurationSize || expanded+entry.UncompressedSize64 > maxExpandedBytes {
			return nil, errors.New("распакованные конфигурации превышают безопасный размер")
		}
		expanded += entry.UncompressedSize64
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maxConfigurationSize+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > maxConfigurationSize {
			return nil, errors.New("конфигурация в ZIP превышает 256 КиБ")
		}
		name := strings.TrimSuffix(filepath.Base(filepath.ToSlash(entry.Name)), filepath.Ext(entry.Name))
		candidate, err := validateCandidate(name, string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name, err)
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return nil, errors.New("ZIP не содержит .conf")
	}
	return uniqueNames(candidates), nil
}

func fromQRImage(path string) ([]Candidate, error) {
	data, err := readLimitedFile(path, 16<<20)
	if err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать изображение QR: %w", err)
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(decoded)
	if err != nil {
		return nil, err
	}
	result, err := qrcode.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		return nil, fmt.Errorf("QR-код с AWG-конфигурацией не найден: %w", err)
	}
	candidate, err := validateCandidate(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), result.GetText())
	if err != nil {
		return nil, fmt.Errorf("QR не содержит корректную AWG-конфигурацию: %w", err)
	}
	return []Candidate{candidate}, nil
}

func validateCandidate(name, configuration string) (Candidate, error) {
	configuration = strings.TrimSpace(strings.TrimPrefix(configuration, "\ufeff"))
	if len(configuration) > maxConfigurationSize {
		return Candidate{}, errors.New("конфигурация превышает 256 КиБ")
	}
	parsed, err := awg.Parse(configuration)
	if err != nil {
		return Candidate{}, err
	}
	if len(parsed.Peers) != 1 {
		return Candidate{}, fmt.Errorf("Ex WG поддерживает один [Peer]; в конфигурации найдено: %d", len(parsed.Peers))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Nexus"
	}
	return Candidate{Name: name, Config: configuration, Generation: parsed.Version}, nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("файл превышает %d байт", limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("файл превышает %d байт", limit)
	}
	return data, nil
}

func uniqueNames(candidates []Candidate) []Candidate {
	seen := make(map[string]int)
	for index := range candidates {
		base := candidates[index].Name
		key := strings.ToLower(base)
		seen[key]++
		if seen[key] > 1 {
			candidates[index].Name = fmt.Sprintf("%s (%d)", base, seen[key])
		}
	}
	return candidates
}
