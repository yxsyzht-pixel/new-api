package chatrecord

import (
	"context"
	"errors"
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
`

const insertStatement = `
INSERT INTO chat_records
  (request_id, user_id, token_id, token_name, staff_id, model_name, endpoint, status_code, user_message, ai_message, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`

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
