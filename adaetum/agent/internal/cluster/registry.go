// Package cluster is the cluster agent's brain: which hosts exist, whether they are
// trusted, and the events they have reported. It is the receiving half of the
// design in internal/membership; docs/auth.md explains the trust model.
//
// The cluster agent never edits a host's facts. A host is one row (identity and
// trust) plus the append-only events only that host may write. Everything the
// dashboard shows about a host (last check-in, version) is derived from its events.
package cluster

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/assistant"
	"github.com/Adaetum/Adaetum/agent/internal/catalog"
	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/memory"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

var migrations = []string{
	`CREATE TABLE hosts (
		host_id        TEXT PRIMARY KEY,
		hostname       TEXT NOT NULL,
		public_key     TEXT NOT NULL,
		status         TEXT NOT NULL,
		reason         TEXT NOT NULL,
		enrolled_at_ms INTEGER NOT NULL,
		approved_by    TEXT NOT NULL DEFAULT '',
		acked_seq      INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE join_tokens (
		hash          TEXT PRIMARY KEY,
		note          TEXT NOT NULL,
		created_at_ms INTEGER NOT NULL,
		expires_at_ms INTEGER NOT NULL,
		used_at_ms    INTEGER
	)`,
	`CREATE TABLE events (
		host_id        TEXT    NOT NULL,
		seq            INTEGER NOT NULL,
		kind           TEXT    NOT NULL,
		at_ms          INTEGER NOT NULL,
		payload        TEXT    NOT NULL,
		received_at_ms INTEGER NOT NULL,
		PRIMARY KEY (host_id, seq)
	)`,
	`CREATE TABLE settings (
		key           TEXT PRIMARY KEY,
		enabled       INTEGER NOT NULL,
		until_ms      INTEGER,
		changed_by    TEXT NOT NULL,
		changed_at_ms INTEGER NOT NULL
	)`,
}

func init() {
	// Memory shares the cluster agent's database; its tables follow the registry's.
	migrations = append(migrations, memory.Migrations...)
	migrations = append(migrations, memory.ManagerMigrations...)
	migrations = append(migrations, assistant.AssistantMigrations...)
	migrations = append(migrations, catalog.Migrations...)
}

// Limits that keep one misbehaving or hostile host from exhausting the registry.
const (
	maxBatchEvents  = 500
	maxPayloadBytes = 16 * 1024
	maxKindBytes    = 64
	maxHostnameLen  = 253
)

// Registry is the cluster agent's database. Safe for concurrent use.
type Registry struct {
	db  *sql.DB
	Now func() time.Time
}

// OpenRegistry opens, creating if needed, the cluster agent's database.
func OpenRegistry(path string) (*Registry, error) {
	db, err := store.OpenDatabase(path, migrations)
	if err != nil {
		return nil, err
	}
	return &Registry{db: db, Now: time.Now}, nil
}

// Close releases the database.
func (r *Registry) Close() error { return r.db.Close() }

// DB is the underlying database, for services that share it (memory).
func (r *Registry) DB() *sql.DB { return r.db }

// Host is a registered host.
type Host struct {
	HostID     string    `json:"hostId"`
	Hostname   string    `json:"hostname"`
	Status     string    `json:"status"` // identity.Approved or identity.Pending
	Reason     string    `json:"reason"`
	EnrolledAt time.Time `json:"enrolledAt"`
	ApprovedBy string    `json:"approvedBy,omitempty"`
	AckedSeq   int64     `json:"ackedSeq"`
}

// MintJoinToken creates a one-time token for enrolling one host and returns its
// secret, which is shown once and never stored (only its hash is).
func (r *Registry) MintJoinToken(ctx context.Context, note string, ttl time.Duration) (secret string, expires time.Time, err error) {
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return "", time.Time{}, errors.New("a join token must last between a moment and seven days")
	}
	now := r.Now()
	token, err := identity.NewJoinToken(now, ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO join_tokens(hash, note, created_at_ms, expires_at_ms) VALUES (?, ?, ?, ?)`,
		token.Hash, truncate(note, 200), now.UnixMilli(), token.ExpiresAt.UnixMilli())
	if err != nil {
		return "", time.Time{}, err
	}
	return token.Secret, token.ExpiresAt, nil
}

// validateEnroll checks the shape of an enrollment before anything is stored.
func validateEnroll(e membership.EnrollRequest) error {
	if len(e.HostID) != 32 {
		return errors.New("hostId must be the agent's 32-character host ID")
	}
	if e.Hostname == "" || len(e.Hostname) > maxHostnameLen {
		return errors.New("hostname is required")
	}
	raw, err := base64.StdEncoding.DecodeString(e.PublicKey)
	if err != nil || len(raw) != 32 {
		return errors.New("publicKey must be a base64 Ed25519 public key")
	}
	return nil
}

// Enroll registers a host and says how far it is trusted.
//
// A host that is already registered with the same key gets its current status back
// and does not need (or use up) a token, so an agent that lost the reply can simply
// ask again. A registered host ID presented with a different key is refused: that is
// what a takeover attempt looks like. The token is consumed in the same transaction
// that registers the host, so one token can never enroll two hosts.
func (r *Registry) Enroll(ctx context.Context, request membership.EnrollRequest, nodes []string, nodesKnown bool) (identity.Trust, string, error) {
	if err := validateEnroll(request); err != nil {
		return identity.Rejected, err.Error(), nil
	}
	now := r.Now()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()

	var existingKey, existingStatus, existingReason string
	err = tx.QueryRowContext(ctx, `SELECT public_key, status, reason FROM hosts WHERE host_id = ?`, request.HostID).
		Scan(&existingKey, &existingStatus, &existingReason)
	switch {
	case err == nil:
		if existingKey != request.PublicKey {
			return identity.Rejected, "this host ID is already enrolled with a different key", nil
		}
		return identity.Trust(existingStatus), existingReason, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", "", err
	}

	// Consume the token only if it is unexpired and unused. The UPDATE's row count
	// is the check, so two concurrent enrollments cannot both win.
	result, err := tx.ExecContext(ctx,
		`UPDATE join_tokens SET used_at_ms = ? WHERE hash = ? AND used_at_ms IS NULL AND expires_at_ms > ?`,
		now.UnixMilli(), identity.HashToken(request.JoinToken), now.UnixMilli())
	if err != nil {
		return "", "", err
	}
	used, _ := result.RowsAffected()

	trust, reason := identity.Decide(request.Hostname, nodes, nodesKnown, used == 1)
	if trust == identity.Rejected {
		return trust, reason, nil
	}
	approvedBy := ""
	if trust == identity.Approved {
		approvedBy = kubernetesMatch
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO hosts(host_id, hostname, public_key, status, reason, enrolled_at_ms, approved_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		request.HostID, request.Hostname, request.PublicKey, string(trust), reason, now.UnixMilli(), approvedBy); err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return trust, reason, nil
}

// Approve lets a pending host send events. by names the person who decided.
func (r *Registry) Approve(ctx context.Context, hostID, by string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE hosts SET status = ?, reason = ?, approved_by = ? WHERE host_id = ? AND status = ?`,
		string(identity.Approved), "approved by a person", truncate(by, 100), hostID, string(identity.Pending))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotPending
	}
	return nil
}

// ErrNotPending means there is no pending host with that ID to approve.
var ErrNotPending = errors.New("no pending host with that ID")

// PublicKey is the lookup identity.Verifier uses: the key enrolled for a host and
// whether the host is approved. Any error is treated as "not allowed".
func (r *Registry) PublicKey(hostID string) (string, bool) {
	var key, status string
	err := r.db.QueryRowContext(context.Background(), `SELECT public_key, status FROM hosts WHERE host_id = ?`, hostID).Scan(&key, &status)
	if err != nil {
		return "", false
	}
	return key, status == string(identity.Approved)
}

// Ingest stores events from one host and returns the highest sequence number it now
// holds without a gap. A host may only write its own events: an event naming any
// other host is refused for the whole batch, because a host that tries is broken or
// hostile and half-accepting its batch would hide that. Repeats are ignored.
func (r *Registry) Ingest(ctx context.Context, hostID string, events []store.Event) (int64, error) {
	if len(events) > maxBatchEvents {
		return 0, fmt.Errorf("at most %d events per request", maxBatchEvents)
	}
	for _, event := range events {
		switch {
		case event.HostID != hostID:
			return 0, fmt.Errorf("event %d names host %q, but the request is from %q", event.Seq, event.HostID, hostID)
		case event.Seq < 1:
			return 0, errors.New("event sequence numbers start at 1")
		case event.Kind == "" || len(event.Kind) > maxKindBytes:
			return 0, errors.New("an event needs a kind of up to 64 characters")
		case len(event.Payload) > maxPayloadBytes:
			return 0, fmt.Errorf("event %d is larger than %d bytes", event.Seq, maxPayloadBytes)
		case len(event.Payload) > 0 && !json.Valid(event.Payload):
			return 0, fmt.Errorf("event %d has a payload that is not JSON", event.Seq)
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	received := r.Now().UnixMilli()
	for _, event := range events {
		payload := string(event.Payload)
		if payload == "" {
			payload = "null"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO events(host_id, seq, kind, at_ms, payload, received_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
			hostID, event.Seq, event.Kind, event.At.UnixMilli(), payload, received); err != nil {
			return 0, err
		}
	}

	// Advance over every sequence number now held, so a batch that fills an earlier
	// gap moves the cursor past it.
	var acked int64
	if err := tx.QueryRowContext(ctx, `SELECT acked_seq FROM hosts WHERE host_id = ?`, hostID).Scan(&acked); err != nil {
		return 0, err
	}
	for {
		var present int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE host_id = ? AND seq = ?`, hostID, acked+1).Scan(&present); err != nil {
			return 0, err
		}
		if present == 0 {
			break
		}
		acked++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE hosts SET acked_seq = ? WHERE host_id = ?`, acked, hostID); err != nil {
		return 0, err
	}
	return acked, tx.Commit()
}

// HostSummary is a host with what its events say about it.
type HostSummary struct {
	Host
	// LastCheckIn and Version come from the host's newest agent.checkin event. They
	// are empty when it has none.
	LastCheckIn *time.Time `json:"lastCheckIn,omitempty"`
	Version     string     `json:"version,omitempty"`
}

// Hosts lists every registered host, newest enrollment first.
func (r *Registry) Hosts(ctx context.Context) ([]HostSummary, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT host_id, hostname, status, reason, enrolled_at_ms, approved_by, acked_seq FROM hosts ORDER BY enrolled_at_ms DESC, host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hosts []HostSummary
	for rows.Next() {
		var host HostSummary
		var enrolled int64
		if err := rows.Scan(&host.HostID, &host.Hostname, &host.Status, &host.Reason, &enrolled, &host.ApprovedBy, &host.AckedSeq); err != nil {
			return nil, err
		}
		host.EnrolledAt = time.UnixMilli(enrolled).UTC()
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	for index := range hosts {
		var at int64
		var payload string
		err := r.db.QueryRowContext(ctx,
			`SELECT at_ms, payload FROM events WHERE host_id = ? AND kind = 'agent.checkin' ORDER BY seq DESC LIMIT 1`,
			hosts[index].HostID).Scan(&at, &payload)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		seen := time.UnixMilli(at).UTC()
		hosts[index].LastCheckIn = &seen
		var parsed struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal([]byte(payload), &parsed)
		hosts[index].Version = parsed.Version
	}
	return hosts, nil
}

func truncate(text string, limit int) string {
	if len(text) > limit {
		return text[:limit]
	}
	return text
}

// Setting and SaveSetting make the registry the policy switchboard's store, so a
// security setting flipped on the cluster agent survives a restart and is the same
// for every request, on every server it acts on.
func (r *Registry) Setting(key string) (policy.Setting, bool, error) {
	var setting policy.Setting
	var enabled int
	var until sql.NullInt64
	var changed int64
	err := r.db.QueryRow(`SELECT key, enabled, until_ms, changed_by, changed_at_ms FROM settings WHERE key = ?`, key).
		Scan(&setting.Key, &enabled, &until, &setting.ChangedBy, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return policy.Setting{}, false, nil
	}
	if err != nil {
		return policy.Setting{}, false, err
	}
	setting.Enabled = enabled == 1
	setting.ChangedAt = time.UnixMilli(changed).UTC()
	if until.Valid {
		at := time.UnixMilli(until.Int64).UTC()
		setting.Until = &at
	}
	return setting, true, nil
}

// SaveSetting stores a setting, replacing any earlier one for the key.
func (r *Registry) SaveSetting(setting policy.Setting) error {
	var until any
	if setting.Until != nil {
		until = setting.Until.UnixMilli()
	}
	enabled := 0
	if setting.Enabled {
		enabled = 1
	}
	_, err := r.db.Exec(`INSERT OR REPLACE INTO settings(key, enabled, until_ms, changed_by, changed_at_ms) VALUES (?, ?, ?, ?, ?)`,
		setting.Key, enabled, until, setting.ChangedBy, setting.ChangedAt.UnixMilli())
	return err
}
