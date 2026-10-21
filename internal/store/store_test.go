package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "data", "org.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fi, _ := os.Stat(filepath.Join(dir, "data", "org.db"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("db mode %v", fi.Mode().Perm())
	}
	if _, err := db.Exec(`INSERT INTO members(aid, handle, kind, joined_at) VALUES('aid-x-000000001', 'x1', 'student', 1)`); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "backup.db")
	if err := Backup(context.Background(), db, dest, false); err != nil {
		t.Fatal(err)
	}
	if err := Backup(context.Background(), db, dest, false); err == nil {
		t.Fatal("existing backup overwritten without replace")
	}
	if err := Backup(context.Background(), db, dest, true); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(dest)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v", fi.Mode().Perm())
	}
	b, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	b.QueryRow(`SELECT COUNT(*) FROM members`).Scan(&n)
	if n != 1 {
		t.Fatal("backup lacks data")
	}
	if _, err := Open(filepath.Join(dir, "bad?name.db")); err == nil {
		t.Fatal("path with '?' accepted")
	}
}
