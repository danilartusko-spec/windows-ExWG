//go:build !windows

package awg

// The official Windows parser is Windows-only. Portable tests still exercise
// Nexus validation; the cross-compiled Windows test/build exercises the
// upstream parser used by the actual client.
func validateOfficial(string) error { return nil }
