package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func randomID() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

const (
	batchSize     = 16
	embedTimeout  = 30 * time.Second
	pollInterval  = 2 * time.Second
	baseBackoff   = 5 * time.Second
	maxBackoff    = 15 * time.Minute
	failedState   = "failed"
	pendingState  = "pending"
	retryingState = "retry"
)

// Run drains the embedding queue until ctx ends. It is meant to run for the life of
// the cluster agent. A failing embedder only slows embeddings down: nothing else
// waits on this loop.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		for {
			worked, err := s.ProcessBatch(ctx)
			if err != nil || !worked {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ProcessBatch embeds one batch of due jobs. It reports whether it found any work.
// Exposed so tests (and a future "embed now" action) can drive it deterministically.
func (s *Service) ProcessBatch(ctx context.Context) (bool, error) {
	embedder := s.embedder()
	if embedder == nil {
		return false, nil
	}
	now := s.now().UnixMilli()
	rows, err := s.DB.QueryContext(ctx, `
		SELECT j.memory_id, j.attempts, m.text
		FROM embedding_jobs j JOIN memories m ON m.id = j.memory_id
		WHERE j.state IN ('pending', 'retry') AND j.next_try_ms <= ? AND m.deleted_ms IS NULL
		ORDER BY j.next_try_ms LIMIT ?`, now, batchSize)
	if err != nil {
		return false, err
	}
	type job struct {
		id       string
		attempts int
		text     string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.attempts, &j.text); err != nil {
			rows.Close()
			return false, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if len(jobs) == 0 {
		return false, nil
	}

	texts := make([]string, len(jobs))
	for i, j := range jobs {
		texts[i] = j.text
	}
	embedCtx, cancel := context.WithTimeout(ctx, embedTimeout)
	vectors, embedErr := embedder.Embed(embedCtx, texts)
	cancel()
	if embedErr == nil && len(vectors) != len(jobs) {
		embedErr = fmt.Errorf("the embedder returned %d vectors for %d texts", len(vectors), len(jobs))
	}

	if embedErr != nil {
		s.setError(embedErr.Error())
		for _, j := range jobs {
			state, next := retryingState, s.now().Add(backoff(j.attempts+1))
			if j.attempts+1 >= maxAttempts {
				state = failedState
			}
			if _, err := s.DB.ExecContext(ctx,
				`UPDATE embedding_jobs SET state = ?, attempts = ?, last_error = ?, next_try_ms = ?, updated_ms = ? WHERE memory_id = ?`,
				state, j.attempts+1, truncate(embedErr.Error(), 300), next.UnixMilli(), s.now().UnixMilli(), j.id); err != nil {
				return true, err
			}
		}
		return true, nil
	}

	s.setError("")
	for i, j := range jobs {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return true, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO embeddings(memory_id, model, dim, vector) VALUES (?, ?, ?, ?)`,
			j.id, embedder.Model(), len(vectors[i]), encodeVector(vectors[i])); err != nil {
			tx.Rollback()
			return true, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM embedding_jobs WHERE memory_id = ?`, j.id); err != nil {
			tx.Rollback()
			return true, err
		}
		if err := tx.Commit(); err != nil {
			return true, err
		}
	}
	return true, nil
}

// RetryFailed puts parked jobs back in the queue (for example after the embedding
// service has been fixed).
func (s *Service) RetryFailed(ctx context.Context) (int, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE embedding_jobs SET state = 'retry', attempts = 0, next_try_ms = ?, updated_ms = ? WHERE state = 'failed'`,
		s.now().UnixMilli(), s.now().UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

func backoff(attempt int) time.Duration {
	delay := baseBackoff << min(attempt, 10)
	if delay > maxBackoff || delay <= 0 {
		return maxBackoff
	}
	return delay
}

func truncate(text string, limit int) string {
	if len(text) > limit {
		return text[:limit]
	}
	return text
}
