package chatrecord

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The transcript store is a database the gateway does not own, so its schema is
// created on demand from the settings page rather than by a migration here.

const createSchema = `
CREATE TABLE IF NOT EXISTS chat_records (
    id           BIGSERIAL PRIMARY KEY,
    request_id   VARCHAR(64)  NOT NULL DEFAULT '',
    user_id      BIGINT       NOT NULL DEFAULT 0,
    token_id     BIGINT       NOT NULL DEFAULT 0,
    token_name   VARCHAR(128) NOT NULL DEFAULT '',
    staff_id     VARCHAR(64)  NOT NULL DEFAULT '',
    model_name   VARCHAR(128) NOT NULL DEFAULT '',
    endpoint     VARCHAR(128) NOT NULL DEFAULT '',
    status_code  INT          NOT NULL DEFAULT 0,
    user_message TEXT         NOT NULL DEFAULT '',
    human_message TEXT        NOT NULL DEFAULT '',
    source_rank  INT          NOT NULL DEFAULT 0,
    ai_message   TEXT         NOT NULL DEFAULT '',
    turn_key     VARCHAR(64)  NOT NULL DEFAULT '',
    source       VARCHAR(16)  NOT NULL DEFAULT '',
    source_confidence VARCHAR(8) NOT NULL DEFAULT '',
    source_signal     VARCHAR(32) NOT NULL DEFAULT '',
    client_name       VARCHAR(32) NOT NULL DEFAULT '',
    thread_source     VARCHAR(32) NOT NULL DEFAULT '',
    client_turn_id    VARCHAR(64) NOT NULL DEFAULT '',
    client_session_id VARCHAR(64) NOT NULL DEFAULT '',
    request_count INT         NOT NULL DEFAULT 1,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_chat_records_created_at ON chat_records (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_chat_records_token_name ON chat_records (token_name);
CREATE INDEX IF NOT EXISTS idx_chat_records_staff_id   ON chat_records (staff_id);
CREATE INDEX IF NOT EXISTS idx_chat_records_user_id    ON chat_records (user_id);
CREATE INDEX IF NOT EXISTS idx_chat_records_model_name ON chat_records (model_name);
CREATE INDEX IF NOT EXISTS idx_chat_records_request_id ON chat_records (request_id);
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS staff_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS turn_key VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_chat_records_source ON chat_records (source);
-- The raw signals are kept beside the verdict so a reader can apply their own
-- rule instead of inheriting ours. Feeding a memory, for instance, should ask
-- for source_confidence = 'hard' and not settle for the text-shape guess.
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS source_confidence VARCHAR(8)  NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS source_signal     VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS client_name       VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS thread_source     VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS client_turn_id    VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS client_session_id VARCHAR(64) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_chat_records_confidence ON chat_records (source_confidence);
-- What the person actually typed, with any wrapper the client added taken off.
-- A memory should be told this and never user_message: every observed image
-- message carried a client's description in front of the real question.
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS human_message TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS source_rank INT NOT NULL DEFAULT 0;
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS request_count INT NOT NULL DEFAULT 1;
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
-- One row per user turn. Partial, so rows written before this existed (and any
-- turn we could not identify) are left alone instead of colliding on an empty key.
CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_records_turn_key
    ON chat_records (turn_key) WHERE turn_key <> '';

CREATE TABLE IF NOT EXISTS chat_record_files (
    id         BIGSERIAL PRIMARY KEY,
    record_id  BIGINT       NOT NULL DEFAULT 0,
    staff_id   VARCHAR(64)  NOT NULL DEFAULT '',
    kind       VARCHAR(16)  NOT NULL DEFAULT '',
    media_type VARCHAR(128) NOT NULL DEFAULT '',
    file_name  VARCHAR(255) NOT NULL DEFAULT '',
    file_size  BIGINT       NOT NULL DEFAULT 0,
    sha256     VARCHAR(64)  NOT NULL DEFAULT '',
    path       TEXT         NOT NULL DEFAULT '',
    source_url TEXT         NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_chat_record_files_record   ON chat_record_files (record_id);
CREATE INDEX IF NOT EXISTS idx_chat_record_files_staff    ON chat_record_files (staff_id);
CREATE INDEX IF NOT EXISTS idx_chat_record_files_sha256   ON chat_record_files (sha256);
CREATE INDEX IF NOT EXISTS idx_chat_record_files_created  ON chat_record_files (created_at DESC);
-- An agent replays its conversation on every round-trip, so the same picture
-- arrives with every one of them. One row per file per turn.
CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_record_files_once
    ON chat_record_files (record_id, sha256) WHERE sha256 <> '';
`

// insertStatement records a turn, or folds this request into the turn already
// recorded. An agent working on one question sends the conversation again on
// every tool round-trip: without the fold, one question becomes a hundred rows,
// most of them holding a tool call and no answer at all. The model's replies
// are appended in the order they arrived, so the row ends up holding the whole
// answer rather than whichever fragment came last.
const insertStatement = `
INSERT INTO chat_records
  (request_id, user_id, token_id, token_name, staff_id, model_name, endpoint,
   status_code, user_message, ai_message, created_at, updated_at, turn_key, source,
   source_confidence, source_signal, client_name, thread_source, client_turn_id,
   client_session_id, human_message, source_rank)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12,$14,$15,$16,$17,$18,$19,$20,$21,$22)
ON CONFLICT (turn_key) WHERE turn_key <> '' DO UPDATE SET
  ai_message = CASE
    WHEN EXCLUDED.ai_message = '' THEN chat_records.ai_message
    WHEN chat_records.ai_message = '' THEN EXCLUDED.ai_message
    -- The same text can arrive twice when a client retries; do not repeat it.
    WHEN right(chat_records.ai_message, length(EXCLUDED.ai_message)) = EXCLUDED.ai_message
      THEN chat_records.ai_message
    ELSE left(chat_records.ai_message || E'\n\n' || EXCLUDED.ai_message, $13)
  END,
  model_name    = EXCLUDED.model_name,
  status_code   = EXCLUDED.status_code,
  -- A turn folds many requests into one row. It keeps the strongest thing any
  -- of them was: a person's question is still a person's question after fifty
  -- tool round-trips land on top of it. $22 carries the incoming rank.
  source            = CASE WHEN $22 > chat_records.source_rank
                           THEN EXCLUDED.source ELSE chat_records.source END,
  source_confidence = CASE WHEN $22 > chat_records.source_rank
                           THEN EXCLUDED.source_confidence ELSE chat_records.source_confidence END,
  source_signal     = CASE WHEN $22 > chat_records.source_rank
                           THEN EXCLUDED.source_signal ELSE chat_records.source_signal END,
  human_message     = CASE WHEN chat_records.human_message = ''
                           THEN EXCLUDED.human_message ELSE chat_records.human_message END,
  source_rank       = greatest(chat_records.source_rank, $22),
  client_name       = EXCLUDED.client_name,
  thread_source     = EXCLUDED.thread_source,
  client_turn_id    = EXCLUDED.client_turn_id,
  client_session_id = EXCLUDED.client_session_id,
  request_count = chat_records.request_count + 1,
  updated_at    = EXCLUDED.created_at
RETURNING id`

const insertFileStatement = `
INSERT INTO chat_record_files
  (record_id, staff_id, kind, media_type, file_name, file_size, sha256, path, source_url, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (record_id, sha256) WHERE sha256 <> '' DO NOTHING`

// fileListStatement pages through attachments. An empty staff id means every
// one of them, so the caller does not have to build two queries.
const fileListStatement = `
SELECT id, record_id, staff_id, kind, media_type, file_name, file_size, path, source_url, created_at
FROM chat_record_files
WHERE ($1 = '' OR staff_id = $1)
ORDER BY id DESC LIMIT $2 OFFSET $3`

// fileLookupStatement finds one stored attachment for the serving endpoint.
const fileLookupStatement = `
SELECT path, media_type, file_name, file_size, source_url
FROM chat_record_files WHERE id = $1`

// newPool opens a small pool. The gateway's own traffic must not be able to
// starve itself of file descriptors because the transcript store went slow.
func newPool(dsn string) (*pgxpool.Pool, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("chat record: no database address configured")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 8
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Two kinds of caller need a connection, and they want opposite things. The
// settings page tests an address that may not even be saved yet, so it gets a
// pool that is thrown away afterwards. The pages that read transcripts back use
// the saved address over and over — a gallery of fifty attachments would open
// fifty pools — so those share one.

// withTempPool runs fn against a pool opened just for this call.
func withTempPool(dsn string, timeout time.Duration, fn func(context.Context, *pgxpool.Pool) error) error {
	pool, err := newPool(dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx, pool)
}

// readers is the shared pool for reading transcripts back, rebuilt when the
// operator points the recorder at a different database.
var readers struct {
	mu   sync.Mutex
	dsn  string
	pool *pgxpool.Pool
}

// withReadPool runs fn against the shared read pool.
func withReadPool(dsn string, timeout time.Duration, fn func(context.Context, *pgxpool.Pool) error) error {
	pool, retired, err := sharedReadPool(dsn)
	if err != nil {
		return err
	}
	// Close waits for queries already in flight, so retiring the previous pool
	// cannot cut one off.
	if retired != nil {
		retired.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx, pool)
}

// sharedReadPool hands back the pool for dsn, plus any pool it displaced for
// the caller to close outside the lock.
func sharedReadPool(dsn string) (pool, retired *pgxpool.Pool, err error) {
	readers.mu.Lock()
	defer readers.mu.Unlock()

	if readers.pool != nil && readers.dsn == dsn {
		return readers.pool, nil, nil
	}
	fresh, err := newPool(dsn)
	if err != nil {
		return nil, nil, err
	}
	retired, readers.pool, readers.dsn = readers.pool, fresh, dsn
	return fresh, retired, nil
}

// CloseReadPool releases the shared read pool.
func CloseReadPool() {
	readers.mu.Lock()
	pool := readers.pool
	readers.pool, readers.dsn = nil, ""
	readers.mu.Unlock()
	if pool != nil {
		pool.Close()
	}
}

// InitSchema creates the tables and their indexes, and reports whether the
// address works at all — which is the question the operator is really asking
// when they press the button.
func InitSchema(dsn string) error {
	return withTempPool(dsn, 30*time.Second, func(ctx context.Context, pool *pgxpool.Pool) error {
		if err := pool.Ping(ctx); err != nil {
			return err
		}
		_, err := pool.Exec(ctx, createSchema)
		return err
	})
}

// TestConnection checks the address without changing anything.
func TestConnection(dsn string) error {
	return withTempPool(dsn, 15*time.Second, func(ctx context.Context, pool *pgxpool.Pool) error {
		return pool.Ping(ctx)
	})
}

// StoredFile is one attachment as the serving endpoint needs it.
type StoredFile struct {
	Path      string
	MediaType string
	FileName  string
	Size      int64
	SourceURL string
}

// LookupFile finds one attachment by id.
func LookupFile(dsn string, id int64) (*StoredFile, error) {
	var file StoredFile
	err := withReadPool(dsn, 15*time.Second, func(ctx context.Context, pool *pgxpool.Pool) error {
		return pool.QueryRow(ctx, fileLookupStatement, id).Scan(
			&file.Path, &file.MediaType, &file.FileName, &file.Size, &file.SourceURL)
	})
	if err != nil {
		return nil, err
	}
	return &file, nil
}

// FileListing is one attachment as the listing shows it.
type FileListing struct {
	ID        int64     `json:"id"`
	RecordID  int64     `json:"record_id"`
	StaffID   string    `json:"staff_id"`
	Kind      string    `json:"kind"`
	MediaType string    `json:"media_type"`
	FileName  string    `json:"file_name"`
	Size      int64     `json:"size"`
	Path      string    `json:"path"`
	SourceURL string    `json:"source_url"`
	CreatedAt time.Time `json:"created_at"`
}

// ListFiles returns a page of attachments, newest first, optionally limited to
// one staff id.
func ListFiles(dsn string, staffID string, limit, offset int) ([]FileListing, error) {
	var items []FileListing
	err := withReadPool(dsn, 15*time.Second, func(ctx context.Context, pool *pgxpool.Pool) error {
		rows, err := pool.Query(ctx, fileListStatement, staffID, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var item FileListing
			if err := rows.Scan(&item.ID, &item.RecordID, &item.StaffID, &item.Kind,
				&item.MediaType, &item.FileName, &item.Size, &item.Path,
				&item.SourceURL, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}
