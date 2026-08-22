package chatrecord

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
    ai_message   TEXT         NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_chat_records_created_at ON chat_records (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_chat_records_token_name ON chat_records (token_name);
CREATE INDEX IF NOT EXISTS idx_chat_records_staff_id   ON chat_records (staff_id);
CREATE INDEX IF NOT EXISTS idx_chat_records_user_id    ON chat_records (user_id);
CREATE INDEX IF NOT EXISTS idx_chat_records_model_name ON chat_records (model_name);
CREATE INDEX IF NOT EXISTS idx_chat_records_request_id ON chat_records (request_id);
ALTER TABLE chat_records ADD COLUMN IF NOT EXISTS staff_id VARCHAR(64) NOT NULL DEFAULT '';

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
`

const insertStatement = `
INSERT INTO chat_records
  (request_id, user_id, token_id, token_name, staff_id, model_name, endpoint, status_code, user_message, ai_message, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING id`

const insertFileStatement = `
INSERT INTO chat_record_files
  (record_id, staff_id, kind, media_type, file_name, file_size, sha256, path, source_url, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`

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

// InitSchema creates the table and its indexes, and reports whether the address
// works at all — which is the question the operator is really asking when they
// press the button.
func InitSchema(dsn string) error {
	pool, err := newPool(dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, createSchema)
	return err
}

// TestConnection checks the address without changing anything.
func TestConnection(dsn string) error {
	pool, err := newPool(dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return pool.Ping(ctx)
}

// StoredFile is one attachment as the serving endpoint needs it.
type StoredFile struct {
	Path      string
	MediaType string
	FileName  string
	Size      int64
	SourceURL string
}

// LookupFile finds one attachment by id. It opens a short-lived pool rather
// than borrowing the writer's: serving a file is a rare, human-paced request,
// and it must not compete with the queue for connections.
func LookupFile(dsn string, id int64) (*StoredFile, error) {
	pool, err := newPool(dsn)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var file StoredFile
	err = pool.QueryRow(ctx, fileLookupStatement, id).Scan(
		&file.Path, &file.MediaType, &file.FileName, &file.Size, &file.SourceURL)
	if err != nil {
		return nil, err
	}
	return &file, nil
}

// ListFiles returns a page of attachments, newest first, optionally limited to
// one staff id.
func ListFiles(dsn string, staffID string, limit, offset int) ([]map[string]any, error) {
	pool, err := newPool(dsn)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	query := `SELECT id, record_id, staff_id, kind, media_type, file_name, file_size, path, source_url, created_at
	          FROM chat_record_files`
	args := []any{}
	if staffID != "" {
		query += ` WHERE staff_id = $1`
		args = append(args, staffID)
	}
	query += fmt.Sprintf(` ORDER BY id DESC LIMIT %d OFFSET %d`, limit, offset)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []map[string]any
	for rows.Next() {
		var (
			id, recordID, size        int64
			staff, kind, mediaType    string
			fileName, path, sourceURL string
			createdAt                 time.Time
		)
		if err := rows.Scan(&id, &recordID, &staff, &kind, &mediaType,
			&fileName, &size, &path, &sourceURL, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "record_id": recordID, "staff_id": staff, "kind": kind,
			"media_type": mediaType, "file_name": fileName, "size": size,
			"path": path, "source_url": sourceURL, "created_at": createdAt,
		})
	}
	return items, rows.Err()
}
