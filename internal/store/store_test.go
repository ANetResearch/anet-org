package store

import (
	"context"
	"database/sql"
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

// schemaV1 is the schema of 0.3.8 (schema_version 1), before the via
// columns and with the assistant's usage table.
const schemaV1 = `
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS invites (
  id          INTEGER PRIMARY KEY,
  lookup      TEXT NOT NULL UNIQUE,
  hash        BLOB NOT NULL,
  kind        TEXT NOT NULL DEFAULT 'student',
  note        TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  created_by  TEXT NOT NULL,
  expires_at  INTEGER NOT NULL,
  max_uses    INTEGER NOT NULL,
  uses        INTEGER NOT NULL DEFAULT 0,
  revoked_at  INTEGER
);
CREATE TABLE IF NOT EXISTS members (
  aid              TEXT PRIMARY KEY,
  handle           TEXT NOT NULL UNIQUE,
  kind             TEXT NOT NULL,
  joined_at        INTEGER NOT NULL,
  invite_id        INTEGER,
  renamed_at       INTEGER,
  mailbox_dropped  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS members_invite ON members(invite_id, joined_at);
CREATE TABLE IF NOT EXISTS held_handles (
  handle  TEXT PRIMARY KEY,
  aid     TEXT NOT NULL,
  until   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS bans (
  aid  TEXT PRIMARY KEY,
  at   INTEGER NOT NULL,
  by   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
  id           INTEGER PRIMARY KEY,
  to_aid       TEXT NOT NULL,
  from_aid     TEXT NOT NULL,
  from_handle  TEXT NOT NULL,
  via_group    TEXT NOT NULL DEFAULT '',
  text         TEXT NOT NULL,
  sent_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_to ON messages(to_aid, id);
CREATE TABLE IF NOT EXISTS board_items (
  id           INTEGER PRIMARY KEY,
  author_aid   TEXT NOT NULL,
  title        TEXT NOT NULL,
  body         TEXT NOT NULL,
  tags         TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL,
  claimer_aid  TEXT,
  note         TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS board_status ON board_items(status, id);
CREATE TABLE IF NOT EXISTS board_comments (
  id          INTEGER PRIMARY KEY,
  item_id     INTEGER NOT NULL REFERENCES board_items(id) ON DELETE CASCADE,
  author_aid  TEXT NOT NULL,
  text        TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS board_comments_item ON board_comments(item_id, id);
CREATE TABLE IF NOT EXISTS projects (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  title        TEXT NOT NULL,
  description  TEXT NOT NULL,
  owner_aid    TEXT NOT NULL,
  created_at   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS notes (
  id          INTEGER PRIMARY KEY,
  project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  author_aid  TEXT NOT NULL,
  kind        TEXT NOT NULL,
  title       TEXT NOT NULL,
  text        TEXT NOT NULL,
  url         TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS notes_project ON notes(project_id, id);
CREATE TABLE IF NOT EXISTS usage (
  day     TEXT NOT NULL,
  aid     TEXT NOT NULL,
  tokens  INTEGER NOT NULL,
  PRIMARY KEY (day, aid)
);
`

// A database written by 0.3.8 opens with every via column added and empty,
// the usage table dropped and schema_version 2; rows are kept.
func TestMigrateFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "org.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{schemaV1,
		`INSERT INTO meta(key, value) VALUES('schema_version', '1')`,
		`INSERT INTO members(aid, handle, kind, joined_at) VALUES('aid-x-000000001', 'x1', 'student', 1)`,
		`INSERT INTO messages(to_aid, from_aid, from_handle, text, sent_at) VALUES('aid-x-000000001', 'aid-y-000000001', 'y1', 'hi', 1)`,
		`INSERT INTO board_items(author_aid, title, body, status, created_at, updated_at) VALUES('aid-x-000000001', 't', '', 'open', 1, 1)`,
		`INSERT INTO board_comments(item_id, author_aid, text, created_at) VALUES(1, 'aid-x-000000001', 'c', 1)`,
		`INSERT INTO projects(name, title, description, owner_aid, created_at) VALUES('p1', 'p', '', 'aid-x-000000001', 1)`,
		`INSERT INTO notes(project_id, author_aid, kind, title, text, created_at) VALUES(1, 'aid-x-000000001', 'note', '', 'n', 1)`,
		`INSERT INTO usage(day, aid, tokens) VALUES('2026-10-01', 'aid-x-000000001', 10)`,
	} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, c := range addedColumns {
		has, err := hasColumn(db, c.table, c.column)
		if err != nil || !has {
			t.Fatalf("%s.%s missing: %v", c.table, c.column, err)
		}
	}
	for _, tbl := range []string{"messages", "board_items", "board_comments", "projects", "notes"} {
		var via string
		if err := db.QueryRow(`SELECT via FROM ` + tbl).Scan(&via); err != nil || via != "" {
			t.Fatalf("%s.via backfill %q: %v", tbl, via, err)
		}
	}
	var cv, xv string
	if err := db.QueryRow(`SELECT claim_via, close_via FROM board_items`).Scan(&cv, &xv); err != nil || cv != "" || xv != "" {
		t.Fatalf("board_items backfill: %v", err)
	}
	if has, _ := hasColumn(db, "usage", "tokens"); has {
		t.Fatal("usage table kept")
	}
	var v string
	db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v)
	if v != "2" {
		t.Fatalf("schema_version %q", v)
	}
	// Opening again changes nothing.
	db.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
}
