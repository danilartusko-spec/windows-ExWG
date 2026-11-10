//go:build windows

package tunnel

import "testing"

func TestEnumerateRASConnectionsAcceptsAnUnavailableRASInterface(t *testing.T) {
	connections, err := enumerateRASConnections()
	if err != nil {
		t.Fatalf("enumerating RAS connections must not block tunnel startup: %v", err)
	}
	for _, connection := range connections {
		if connection.Size == 0 {
			t.Fatal("RAS returned a connection with an uninitialized structure")
		}
	}
}
