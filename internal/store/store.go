// Package store keeps Task records and their event history in SQLite. Only the
// Scheduler writes it.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id         TEXT PRIMARY KEY,
	data       TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	at      TEXT NOT NULL,
	signal  TEXT NOT NULL,
	from_stage TEXT NOT NULL,
	to_stage   TEXT NOT NULL,
	detail  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_task ON events(task_id, seq);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS skips (
	url   TEXT PRIMARY KEY COLLATE NOCASE,
	title TEXT NOT NULL,
	at    TEXT NOT NULL
);`

type Event struct {
	TaskID string    `json:"task_id"`
	At     time.Time `json:"at"`
	Signal string    `json:"signal"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Detail string    `json:"detail,omitempty"`
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	return &Store{db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Tasks returns every Task record as stored.
func (s *Store) Tasks() (map[string][]byte, error) {
	rows, err := s.db.Query(`SELECT id, data FROM tasks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		out[id] = []byte(data)
	}
	return out, rows.Err()
}

// Save writes one Task record and appends its new events in one transaction.
func (s *Store) Save(id string, data []byte, events []Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO tasks(id, data, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, id, string(data), now); err != nil {
		return err
	}
	for _, e := range events {
		if _, err := tx.Exec(`INSERT INTO events(task_id, at, signal, from_stage, to_stage, detail) VALUES(?, ?, ?, ?, ?, ?)`,
			e.TaskID, e.At.UTC().Format(time.RFC3339Nano), e.Signal, e.From, e.To, e.Detail); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Events(taskID string) ([]Event, error) {
	rows, err := s.db.Query(`SELECT task_id, at, signal, from_stage, to_stage, detail FROM events WHERE task_id = ? ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at string
		if err := rows.Scan(&e.TaskID, &at, &e.Signal, &e.From, &e.To, &e.Detail); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Setting returns "" and no error for a key that was never set.
func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Skip is a PR that the user holds out of every review queue.
type Skip struct {
	URL   string
	Title string
	At    time.Time
}

// Skips returns every skip, oldest first.
func (s *Store) Skips() ([]Skip, error) {
	rows, err := s.db.Query(`SELECT url, title, at FROM skips ORDER BY at, url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Skip
	for rows.Next() {
		var k Skip
		var at string
		if err := rows.Scan(&k.URL, &k.Title, &at); err != nil {
			return nil, err
		}
		k.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, k)
	}
	return out, rows.Err()
}

// AddSkip keeps the skip. A URL that differs only in case is the same skip, and
// the first write wins. added is false when the PR was skipped already.
func (s *Store) AddSkip(k Skip) (added bool, err error) {
	res, err := s.db.Exec(`INSERT INTO skips(url, title, at) VALUES(?, ?, ?) ON CONFLICT(url) DO NOTHING`,
		k.URL, k.Title, k.At.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RemoveSkip drops the skip whose URL matches url, ignoring case. removed is
// false when the PR was not skipped.
func (s *Store) RemoveSkip(url string) (removed bool, err error) {
	res, err := s.db.Exec(`DELETE FROM skips WHERE url = ?`, url)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
