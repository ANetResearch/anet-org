// Package store opens the SQLite database and holds its schema.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // the pure-Go SQLite driver
)

// SchemaVersion is the version written to meta.schema_version.
const SchemaVersion = 1

const schema = `
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
`

// Open opens (creating if needed) the database at path with WAL, foreign
// keys and a busy timeout, and applies the schema. The file and its
// directory are created private to this account.
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("store: no database path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if f != nil {
			f.Close()
		}
	}
	if strings.ContainsAny(path, "?#%\x00") {
		return nil, errors.New("store: the database path must not contain '?', '#', '%' or NUL")
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serialises every statement. An organisation's load is
	// small, and it removes SQLITE_BUSY between transactions of this process.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', ?)
		ON CONFLICT(key) DO NOTHING`, fmt.Sprint(SchemaVersion)); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate adds what later versions need to a database made by an earlier
// one. Columns are added empty; the service fills them (org.New).
func migrate(db *sql.DB) error {
	for _, t := range []string{"members", "held_handles"} {
		has, err := hasColumn(db, t, "skeleton")
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(`ALTER TABLE ` + t + ` ADD COLUMN skeleton TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	}
	// 0.3.9: the built-in assistant and its token accounting are gone.
	if _, err := db.Exec(`DROP TABLE IF EXISTS usage`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS members_skeleton ON members(skeleton);
		CREATE INDEX IF NOT EXISTS held_skeleton ON held_handles(skeleton);
		CREATE INDEX IF NOT EXISTS board_author ON board_items(author_aid);
		CREATE INDEX IF NOT EXISTS comments_author ON board_comments(author_aid);
		CREATE INDEX IF NOT EXISTS notes_author ON notes(author_aid);
		CREATE INDEX IF NOT EXISTS messages_pair ON messages(to_aid, from_aid);`)
	return err
}

func hasColumn(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return false, err
		}
		if n == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Backup writes a consistent copy of the database to dest with VACUUM INTO,
// which is safe while the server is running. The copy is written to a
// temporary name beside dest, made private and then renamed, so dest is
// either the old file or a complete new one. An existing dest is replaced
// only when replace is true.
func Backup(ctx context.Context, db *sql.DB, dest string, replace bool) error {
	if dest == "" {
		return errors.New("store: no backup path")
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if !replace {
		if _, err := os.Lstat(abs); err == nil {
			return fmt.Errorf("store: %s exists (use --replace to overwrite)", abs)
		}
	}
	if strings.ContainsRune(abs, 0) {
		return errors.New("store: bad backup path")
	}
	tmp := abs + ".tmp-backup"
	_ = os.Remove(tmp)
	// VACUUM INTO accepts an empty existing file; creating it first gives
	// the copy its private mode from the first byte.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("store: backup: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
