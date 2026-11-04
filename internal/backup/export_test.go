package backup

import "testing"

// SetFreeSpace replaces the free-space lookup for one test.
func SetFreeSpace(t testing.TB, f func(string) (uint64, error)) {
	old := freeSpace
	freeSpace = f
	t.Cleanup(func() { freeSpace = old })
}

// SetDBPartBytes lowers the database part size for one test.
func SetDBPartBytes(t testing.TB, n int) {
	old := dbPartBytes
	dbPartBytes = n
	t.Cleanup(func() { dbPartBytes = old })
}

// SizeWarningFor is the capsule size warning for n bytes of members.
func SizeWarningFor(n int64) string { return sizeWarning(n) }
