package backup

import "testing"

// SetFreeSpace replaces the free-space lookup for one test.
func SetFreeSpace(t testing.TB, f func(string) (uint64, error)) {
	old := freeSpace
	freeSpace = f
	t.Cleanup(func() { freeSpace = old })
}
