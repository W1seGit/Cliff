package store

import (
	"path/filepath"
	"testing"
)

// openTestStore opens a fresh SQLite store in a temp dir and closes it when
// the test ends.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "cliff.db"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
