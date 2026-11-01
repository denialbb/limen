package state

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsDuplicateColumnError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("duplicate column name: fatal"), true},
		{errors.New("duplicate column name: FATAL"), true},
		{errors.New("SQL logic error: duplicate column name: fatal (1)"), true},
		{errors.New("database is locked"), false},
		{errors.New("disk I/O error"), false},
		{errors.New("attempt to write a readonly database"), false},
	}

	for _, tc := range cases {
		got := isDuplicateColumnError(tc.err)
		if got != tc.want {
			t.Errorf("isDuplicateColumnError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestSQLiteStore_MigrateFatalColumn_PropagatesError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "closed.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	// Close the underlying DB so subsequent Exec fails with a real driver error.
	_ = store.db.Close()

	err = store.createSchema()
	if err == nil {
		t.Fatal("expected createSchema on closed db to fail, got nil")
	}
	if !strings.Contains(err.Error(), "execute schema") && !strings.Contains(err.Error(), "migrate fatal column") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
