//go:build windows

package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/ipc/namedpipe"
	"nexusclient/internal/debuglog"
)

type RuntimeStats struct {
	Peers         int
	RX            uint64
	TX            uint64
	LastHandshake time.Time
	Raw           string
}

func ReadRuntimeStats(ctx context.Context) (RuntimeStats, error) {
	return readRuntimeStats(ctx, true)
}

// ReadRuntimeStatsQuiet is used by the one-second UI sampler. The full UAPI
// response is still available to diagnostics, but routine speed updates must
// not inflate the persistent debug journal.
func ReadRuntimeStatsQuiet(ctx context.Context) (RuntimeStats, error) {
	return readRuntimeStats(ctx, false)
}

func readRuntimeStats(ctx context.Context, logRaw bool) (RuntimeStats, error) {
	connection, err := namedpipe.DialContext(ctx, `\\.\pipe\ProtectedPrefix\Administrators\AmneziaWG\`+ProfileName)
	if err != nil {
		return RuntimeStats{}, fmt.Errorf("канал управления AWG недоступен: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := connection.Write([]byte("get=1\n\n")); err != nil {
		return RuntimeStats{}, err
	}

	var result RuntimeStats
	var handshakeSeconds int64
	var handshakeNanoseconds int64
	var protocolErr int64
	var raw strings.Builder
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		// Preserve UAPI state verbatim except key material. Never export the
		// original IPC response wholesale: it contains the tunnel private key.
		if strings.Contains(strings.ToLower(name), "key") {
			fmt.Fprintf(&raw, "%s=[key omitted]\n", name)
		} else {
			fmt.Fprintln(&raw, line)
		}
		switch name {
		case "public_key":
			result.Peers++
		case "rx_bytes":
			number, _ := strconv.ParseUint(value, 10, 64)
			result.RX += number
		case "tx_bytes":
			number, _ := strconv.ParseUint(value, 10, 64)
			result.TX += number
		case "last_handshake_time_sec":
			number, _ := strconv.ParseInt(value, 10, 64)
			if number > handshakeSeconds {
				handshakeSeconds = number
				handshakeNanoseconds = 0
			}
		case "last_handshake_time_nsec":
			number, _ := strconv.ParseInt(value, 10, 64)
			handshakeNanoseconds = number
		case "errno":
			protocolErr, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	result.Raw = raw.String()
	if logRaw {
		debuglog.Event("AWG.UAPI.get", "%s", result.Raw)
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if protocolErr != 0 {
		return result, fmt.Errorf("AWG UAPI errno=%d; response:\n%s", protocolErr, result.Raw)
	}
	if result.Peers == 0 {
		return result, errors.New("AWG UAPI response contains no peer")
	}
	if handshakeSeconds > 0 {
		result.LastHandshake = time.Unix(handshakeSeconds, handshakeNanoseconds)
	}
	return result, nil
}

func WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if _, err := ReadRuntimeStats(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("AWG startup/UAPI wait: %w", errors.Join(ctx.Err(), lastErr))
		case <-ticker.C:
		}
	}
}
