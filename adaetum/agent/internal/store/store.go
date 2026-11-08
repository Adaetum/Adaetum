// Package store is the host's own database: the source of truth for what this
// agent has seen and done, kept in a local Turso (SQLite-compatible) file that
// works with no network and no cluster.
//
// Its first job is the event outbox. A host appends events about itself (check-ins,
// finished cleanups, and so on); a central service later collects them. Two rules
// make eventual consistency safe without conflict resolution:
//
//   - Events are append-only and owned by exactly one host. A host never edits an
//     event and never writes another host's events, so central has nothing to merge.
//   - Every event has a stable identity (host ID and a per-host sequence number), so
//     delivering one twice is harmless and central can ignore the repeat.
//
// The same package is meant to back the cluster agent's own database (internal/
// cluster): it will gain tables for received events and the cluster view there, using
// the same migration list, so there is one way to evolve a schema.
//
// FUTURE USES recorded here so they are decisions and not accidents: the agent's
// memory and retrieval layer, and the queue of text to embed, belong in this
// database too. An embedding job is just an event; the embedding service is a
// separate stateless pod, and a failure there must never block anything else.
//
// Delivery progress is a single cursor: the highest sequence number central has
// acknowledged. Everything above it is pending and is resent until acknowledged.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	// The Turso engine. It is the SQLite-compatible database Adaetum standardizes
	// on, driven without cgo (the native library is embedded in the binary and
	// unpacked at run time into TURSO_GO_CACHE_DIR, or the user cache directory).
	_ "turso.tech/database/tursogo"
)

// Event is one thing that happened on one host.
type Event struct {
	HostID  string          `json:"hostId"`
	Seq     int64           `json:"seq"`
	Kind    string          `json:"kind"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

// ID is the event's identity across the whole cluster.
func (e Event) ID() string { return fmt.Sprintf("%s:%d", e.HostID, e.Seq) }

// Store is a host's local database. It is safe for concurrent use.
type Store struct {
	db     *sql.DB
	hostID string
	now    func() time.Time
	// appendMu serializes appends so sequence numbers are gapless and ordered.
	appendMu sync.Mutex
}

// migrations are applied in order, once each. Never edit one that has shipped;
// add another.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE events (
		seq     INTEGER PRIMARY KEY,
		kind    TEXT    NOT NULL,
		at_ms   INTEGER NOT NULL,
		payload TEXT    NOT NULL
	)`,
}

const (
	metaSchemaVersion = "schema_version"
	metaHostID        = "host_id"
	metaAckedSeq      = "acked_seq"
)

// Open opens, creating if needed, the database at path and brings its schema up
// to date. The directory is private to the agent because the file will come to
// hold host state.
func Open(path string) (*Store, error) {
	db, err := OpenDatabase(path, migrations)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, now: time.Now}
	if err := s.loadHostID(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// HostID is this host's stable identity, made once when the database is created.
// It is the only thing that distinguishes this host's events from another's, so it
// is random and never derived from the hostname, which can change.
func (s *Store) HostID() string { return s.hostID }

func (s *Store) migrate(ctx context.Context) error {
	return Migrate(ctx, s.db, migrations)
}

// Migrate brings any database up to date with an ordered list of schema changes,
// applying each once and recording progress in the meta table. It is exported so
// the cluster agent's database, which holds different tables, evolves the same
// way. Never edit a migration that has shipped; add another.
func Migrate(ctx context.Context, db *sql.DB, list []string) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("preparing the schema: %w", err)
	}
	version := 0
	var recorded string
	err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, metaSchemaVersion).Scan(&recorded)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("reading the schema version: %w", err)
	default:
		version, _ = strconv.Atoi(recorded)
	}
	if version > len(list) {
		return fmt.Errorf("the database is schema version %d but this agent only understands up to %d; upgrade the agent", version, len(list))
	}
	for index := version; index < len(list); index++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, list[index]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", index+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, metaSchemaVersion, strconv.Itoa(index+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", index+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: %w", index+1, err)
		}
	}
	return nil
}

// OpenDatabase opens a Turso database file with the agent's standard settings
// (private directory, one connection) and applies migrations. The host store and
// the cluster registry both use it.
func OpenDatabase(path string, list []string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating the data directory: %w", err)
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := Migrate(context.Background(), db, list); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (s *Store) loadHostID(ctx context.Context) error {
	id, err := s.meta(ctx, metaHostID)
	if err != nil {
		return err
	}
	if id == "" {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("making a host ID: %w", err)
		}
		id = hex.EncodeToString(raw)
		if _, err := s.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES (?, ?)`, metaHostID, id); err != nil {
			return fmt.Errorf("saving the host ID: %w", err)
		}
	}
	s.hostID = id
	return nil
}

// meta returns a stored value, or "" when the key is not set.
func (s *Store) meta(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", key, err)
	}
	return value, nil
}

// Append records an event about this host and returns it. The payload is stored
// as JSON.
func (s *Store) Append(ctx context.Context, kind string, payload any) (Event, error) {
	if kind == "" {
		return Event{}, errors.New("an event needs a kind")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encoding the %s payload: %w", kind, err)
	}

	s.appendMu.Lock()
	defer s.appendMu.Unlock()

	at := s.now().UTC()
	var seq int64
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO events(seq, kind, at_ms, payload)
		 VALUES ((SELECT COALESCE(MAX(seq), 0) + 1 FROM events), ?, ?, ?)
		 RETURNING seq`, kind, at.UnixMilli(), string(encoded)).Scan(&seq)
	if err != nil {
		return Event{}, fmt.Errorf("recording the %s event: %w", kind, err)
	}
	return Event{HostID: s.hostID, Seq: seq, Kind: kind, At: time.UnixMilli(at.UnixMilli()).UTC(), Payload: encoded}, nil
}

// Pending returns events central has not acknowledged, oldest first.
func (s *Store) Pending(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	acked, err := s.AckedSeq(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, kind, at_ms, payload FROM events WHERE seq > ? ORDER BY seq LIMIT ?`, acked, limit)
	if err != nil {
		return nil, fmt.Errorf("reading pending events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var atMs int64
		var payload string
		if err := rows.Scan(&event.Seq, &event.Kind, &atMs, &payload); err != nil {
			return nil, err
		}
		event.HostID = s.hostID
		event.At = time.UnixMilli(atMs).UTC()
		event.Payload = json.RawMessage(payload)
		events = append(events, event)
	}
	return events, rows.Err()
}

// AckedSeq is the highest sequence number central has acknowledged, or 0.
func (s *Store) AckedSeq(ctx context.Context) (int64, error) {
	value, err := s.meta(ctx, metaAckedSeq)
	if err != nil || value == "" {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

// Ack records that central has durably stored every event up to and including
// seq. The cursor only moves forward, so a late or repeated acknowledgement is
// harmless, and it cannot pass the newest event, which would hide one from
// delivery.
func (s *Store) Ack(ctx context.Context, seq int64) error {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()

	var newest int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&newest); err != nil {
		return err
	}
	if seq > newest {
		return fmt.Errorf("cannot acknowledge event %d: the newest is %d", seq, newest)
	}
	current, err := s.AckedSeq(ctx)
	if err != nil {
		return err
	}
	if seq <= current {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, metaAckedSeq, strconv.FormatInt(seq, 10))
	return err
}
