// Package memory is the agent's long-term memory: facts, decisions and notes that
// people (and the assistant) want to be able to find again by meaning, not just by
// exact words.
//
// THE DESIGN (docs/memory.md):
//
//   - Memories live in the cluster agent's Turso database. Remembering something is a
//     plain write plus a row in a durable job queue; it NEVER waits for, or depends on,
//     an embedding. An embedding that is slow, failing or not built at all cannot
//     block a person, a playbook, or the assistant.
//   - A worker drains the queue in the background: it asks an Embedder for vectors,
//     stores them next to the memory, and retries failures with growing delays. The
//     Embedder is the seam for the planned dedicated embedding pod (Go, MiniLM-L6-v2
//     INT8 through ONNX Runtime, 1-2 cores, stateless, rescheduled by Kubernetes
//     like anything else). Until that pod exists, a lexical stand-in (HashEmbedder)
//     fills the seam, and says so.
//   - Retrieval stays here, in the Go backend. Search embeds the question, ranks the
//     stored vectors by cosine similarity, and falls back to keyword matching when the
//     embedder is unavailable or a memory has no vector yet, so recall works (more
//     crudely) even when embedding does not.
//
// Memories are not secrets storage. Anything remembered is shown back to the
// assistant, which sends it to an AI service, so Remember refuses text that looks
// like a credential.
package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Limits.
const (
	maxTextLength = 4000
	maxTags       = 8
	maxTagLength  = 40
	maxSearchRows = 5000
	// maxAttempts is how many times a failing embedding is retried before the job is
	// parked as failed (the memory stays searchable by keyword).
	maxAttempts = 8
)

// Embedder turns text into vectors.
type Embedder interface {
	// Model names the embedding space. Vectors from different models are never
	// compared, so changing the model does not corrupt search.
	Model() string
	// Embed returns one vector per text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Memory is one remembered thing.
type Memory struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Tags    []string  `json:"tags"`
	Source  string    `json:"source"`
	Created time.Time `json:"created"`
	// Status is "searchable" (it has a vector), "queued" (waiting for one; keyword
	// search still finds it), or "failed" (embedding gave up; keyword only).
	Status string `json:"status"`
}

// Result is a search hit.
type Result struct {
	Memory
	Score float64 `json:"score"`
	// Match is "semantic" (vector similarity) or "keyword".
	Match string `json:"match"`
}

// Status describes the queue, for the page and the assistant.
type Status struct {
	Embedder string `json:"embedder"`
	Total    int    `json:"total"`
	Embedded int    `json:"embedded"`
	Queued   int    `json:"queued"`
	Failed   int    `json:"failed"`
	// Degraded means the last embedding attempt failed, so search is keyword-only.
	Degraded  bool   `json:"degraded"`
	LastError string `json:"lastError,omitempty"`
}

// Migrations adds the memory tables to the cluster agent's database.
var Migrations = []string{
	`CREATE TABLE memories (
		id         TEXT PRIMARY KEY,
		text       TEXT NOT NULL,
		tags       TEXT NOT NULL DEFAULT '',
		source     TEXT NOT NULL,
		created_ms INTEGER NOT NULL,
		deleted_ms INTEGER
	)`,
	`CREATE TABLE embedding_jobs (
		memory_id   TEXT PRIMARY KEY,
		state       TEXT NOT NULL,
		attempts    INTEGER NOT NULL DEFAULT 0,
		last_error  TEXT NOT NULL DEFAULT '',
		next_try_ms INTEGER NOT NULL,
		updated_ms  INTEGER NOT NULL
	)`,
	`CREATE TABLE embeddings (
		memory_id TEXT PRIMARY KEY,
		model     TEXT NOT NULL,
		dim       INTEGER NOT NULL,
		vector    BLOB NOT NULL
	)`,
}

// Service is the memory store, queue and search.
type Service struct {
	DB *sql.DB
	// Embedder may be nil: nothing is embedded and search is keyword-only.
	Embedder Embedder
	Now      func() time.Time
	// NewID makes memory IDs; tests replace it.
	NewID func() string

	mu        sync.Mutex
	lastError string
}

// embedder is the embedder in use, or nil when embedding is off (including a Manager
// that has been switched to "none"): the callers treat both the same way.
func (s *Service) embedder() Embedder {
	if s.Embedder == nil {
		return nil
	}
	if switchable, ok := s.Embedder.(interface{ Enabled() bool }); ok && !switchable.Enabled() {
		return nil
	}
	return s.Embedder
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// An obvious credential: refuse rather than store and later send it onward.
var secretShape = regexp.MustCompile(`(?i)(sk-ant-[a-z0-9_-]{10,}|adj_[0-9a-f]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|\b[0-9a-f]{64}\b|ghp_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})`)

// Remember stores a memory and queues it for embedding. It returns as soon as the
// memory is stored.
func (s *Service) Remember(ctx context.Context, text string, tags []string, source string) (Memory, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return Memory{}, errors.New("there is nothing to remember")
	case len(text) > maxTextLength:
		return Memory{}, fmt.Errorf("a memory may be at most %d characters; summarise it", maxTextLength)
	case secretShape.MatchString(text):
		return Memory{}, errors.New("this looks like a password, key or token. Memories are sent to an AI service when recalled, so secrets are not stored")
	}
	cleanTags, err := normalizeTags(tags)
	if err != nil {
		return Memory{}, err
	}
	if source == "" {
		source = "unknown"
	}
	id := s.newID()
	now := s.now().UTC()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO memories(id, text, tags, source, created_ms) VALUES (?, ?, ?, ?, ?)`,
		id, text, strings.Join(cleanTags, ","), source, now.UnixMilli()); err != nil {
		return Memory{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO embedding_jobs(memory_id, state, next_try_ms, updated_ms) VALUES (?, 'pending', ?, ?)`,
		id, now.UnixMilli(), now.UnixMilli()); err != nil {
		return Memory{}, err
	}
	if err := tx.Commit(); err != nil {
		return Memory{}, err
	}
	return Memory{ID: id, Text: text, Tags: cleanTags, Source: source, Created: now, Status: "queued"}, nil
}

// Forget removes a memory and its vector. It is a soft delete of the text (so the
// record that something existed is not silently rewritten) with the content cleared.
func (s *Service) Forget(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE memories SET deleted_ms = ?, text = '' WHERE id = ? AND deleted_ms IS NULL`, s.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	for _, statement := range []string{`DELETE FROM embeddings WHERE memory_id = ?`, `DELETE FROM embedding_jobs WHERE memory_id = ?`} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrNotFound means there is no such memory.
var ErrNotFound = errors.New("no such memory")

// List returns the most recent memories.
func (s *Service) List(ctx context.Context, limit int) ([]Memory, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT m.id, m.text, m.tags, m.source, m.created_ms,
		       CASE WHEN e.memory_id IS NOT NULL THEN 'searchable' WHEN j.state = 'failed' THEN 'failed' ELSE 'queued' END
		FROM memories m
		LEFT JOIN embeddings e ON e.memory_id = m.id
		LEFT JOIN embedding_jobs j ON j.memory_id = m.id
		WHERE m.deleted_ms IS NULL
		ORDER BY m.created_ms DESC, m.id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var memories []Memory
	for rows.Next() {
		var memory Memory
		var tags string
		var created int64
		if err := rows.Scan(&memory.ID, &memory.Text, &tags, &memory.Source, &created, &memory.Status); err != nil {
			return nil, err
		}
		memory.Tags = splitTags(tags)
		memory.Created = time.UnixMilli(created).UTC()
		memories = append(memories, memory)
	}
	return memories, rows.Err()
}

// Search finds memories relevant to a question. It never fails because of the
// embedder: if embedding the question fails, the answer is keyword matches, and
// Status reports the degradation.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("ask what to look for")
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	all, err := s.searchable(ctx)
	if err != nil {
		return nil, err
	}

	var semantic []Result
	embedder := s.embedder()
	if embedder != nil {
		embedCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		vectors, embedErr := embedder.Embed(embedCtx, []string{query})
		cancel()
		if embedErr == nil && len(vectors) == 1 {
			s.setError("")
			for _, candidate := range all {
				if candidate.vector == nil || candidate.model != embedder.Model() || len(candidate.vector) != len(vectors[0]) {
					continue
				}
				if score := cosine(vectors[0], candidate.vector); score > 0.15 {
					semantic = append(semantic, Result{Memory: candidate.Memory, Score: score, Match: "semantic"})
				}
			}
		} else if embedErr != nil {
			s.setError(embedErr.Error())
		}
	}

	// Keyword matches cover memories that have no vector yet (just remembered, or
	// failed) and the whole store when the embedder is down.
	seen := map[string]bool{}
	for _, result := range semantic {
		seen[result.ID] = true
	}
	words := queryWords(query)
	var keyword []Result
	for _, candidate := range all {
		if seen[candidate.ID] || (candidate.vector != nil && len(semantic) > 0) {
			continue
		}
		if score := keywordScore(words, candidate.Text+" "+strings.Join(candidate.Tags, " ")); score > 0 {
			keyword = append(keyword, Result{Memory: candidate.Memory, Score: score, Match: "keyword"})
		}
	}

	results := append(semantic, keyword...)
	sort.SliceStable(results, func(a, b int) bool { return results[a].Score > results[b].Score })
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

type candidate struct {
	Memory
	vector []float32
	model  string
}

func (s *Service) searchable(ctx context.Context) ([]candidate, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT m.id, m.text, m.tags, m.source, m.created_ms, e.model, e.vector
		FROM memories m
		LEFT JOIN embeddings e ON e.memory_id = m.id
		WHERE m.deleted_ms IS NULL
		ORDER BY m.created_ms DESC LIMIT ?`, maxSearchRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		var tags string
		var created int64
		var model sql.NullString
		var blob []byte
		if err := rows.Scan(&c.ID, &c.Text, &tags, &c.Source, &created, &model, &blob); err != nil {
			return nil, err
		}
		c.Tags = splitTags(tags)
		c.Created = time.UnixMilli(created).UTC()
		c.Status = "queued"
		if model.Valid && len(blob) > 0 {
			c.model, c.vector, c.Status = model.String, decodeVector(blob), "searchable"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Status reports the queue and whether embedding is working.
func (s *Service) Status(ctx context.Context) (Status, error) {
	status := Status{Embedder: "none (keyword search only)"}
	if embedder := s.embedder(); embedder != nil {
		status.Embedder = embedder.Model()
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE deleted_ms IS NULL`).Scan(&status.Total); err != nil {
		return status, err
	}
	// Only vectors from the model in use count: others cannot be compared with a query,
	// so those memories are not searchable by meaning until re-embedded.
	if embedder := s.embedder(); embedder != nil {
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM embeddings e JOIN memories m ON m.id = e.memory_id WHERE m.deleted_ms IS NULL AND e.model = ?`, embedder.Model()).Scan(&status.Embedded); err != nil {
			return status, err
		}
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM embedding_jobs WHERE state != 'failed'`).Scan(&status.Queued); err != nil {
		return status, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM embedding_jobs WHERE state = 'failed'`).Scan(&status.Failed); err != nil {
		return status, err
	}
	s.mu.Lock()
	status.LastError = s.lastError
	status.Degraded = s.lastError != ""
	s.mu.Unlock()
	return status, nil
}

func (s *Service) setError(message string) {
	s.mu.Lock()
	s.lastError = message
	s.mu.Unlock()
}

func (s *Service) newID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return randomID()
}

// --- helpers

func normalizeTags(tags []string) ([]string, error) {
	var clean []string
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || seen[tag] {
			continue
		}
		if len(tag) > maxTagLength || strings.ContainsAny(tag, ",\n") {
			return nil, fmt.Errorf("a tag is a short word or phrase of at most %d characters, with no commas", maxTagLength)
		}
		seen[tag] = true
		clean = append(clean, tag)
	}
	if len(clean) > maxTags {
		return nil, fmt.Errorf("at most %d tags", maxTags)
	}
	return clean, nil
}

func splitTags(text string) []string {
	if text == "" {
		return []string{}
	}
	return strings.Split(text, ",")
}

var wordPattern = regexp.MustCompile(`[\p{L}\p{N}_-]+`)

func queryWords(query string) []string {
	var words []string
	for _, word := range wordPattern.FindAllString(strings.ToLower(query), -1) {
		if len(word) >= 3 {
			words = append(words, word)
		}
	}
	return words
}

// keywordScore is the fraction of query words that appear in the text.
func keywordScore(words []string, text string) float64 {
	if len(words) == 0 {
		return 0
	}
	text = strings.ToLower(text)
	hits := 0
	for _, word := range words {
		if strings.Contains(text, word) {
			hits++
		}
	}
	return float64(hits) / float64(len(words)) * 0.9 // below a strong semantic match, above noise
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func encodeVector(vector []float32) []byte {
	out := make([]byte, 4*len(vector))
	for i, value := range vector {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(value))
	}
	return out
}

func decodeVector(blob []byte) []float32 {
	out := make([]float32, len(blob)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out
}
