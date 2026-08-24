package chatrecord

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// A transcript that is never pruned is a disk that eventually fills, and the
// attachments fill it far faster than the rows do — one conversation with a
// screenshot in it outweighs thousands of plain turns.
//
// Nothing is deleted unless the operator has said how long to keep it. Both
// retentions default to zero, which means keep everything: a gateway that
// quietly started discarding a company's records after an upgrade would be a
// far worse failure than a full disk, which at least announces itself.

// sweepBatch bounds one delete. Small enough that the statement never holds a
// lock long enough to matter, and the loop simply runs again.
const sweepBatch = 500

// sweepFirstDelay lets a restart settle before the first pass — the gateway has
// better things to do in its first minutes than walk the whole table.
const sweepFirstDelay = 2 * time.Minute

const sweepInterval = 6 * time.Hour

// sweep belongs to the writer's generation: it dies with the context when the
// operator repoints the recorder, so a settings change never leaves two of them
// deleting at once.
func (w *writer) sweep(ctx context.Context) {
	if !w.waitReady(ctx) {
		return
	}
	timer := time.NewTimer(sweepFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		w.sweepOnce(ctx)
		timer.Reset(sweepInterval)
	}
}

func (w *writer) sweepOnce(ctx context.Context) {
	cfg := operation_setting.GetChatRecordSetting()
	fileDays, recordDays := cfg.FileRetentionDays, cfg.RecordRetentionDays

	if fileDays > 0 {
		w.expireFiles(ctx, time.Now().AddDate(0, 0, -fileDays), "attachment retention")
	}
	if recordDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -recordDays)
		// Disk first. The foreign key removes the rows for us when the records
		// go, and a cascade cannot unlink a file — the attachments would be
		// left on disk with nothing left pointing at them.
		w.expireFiles(ctx, cutoff, "record retention")
		w.expireRecords(ctx, cutoff)
	}
}

const expireFilesStatement = `
DELETE FROM chat_record_files
 WHERE id IN (SELECT id FROM chat_record_files WHERE created_at < $1 ORDER BY id LIMIT $2)
RETURNING record_id, path`

// stillReferenced asks whether any surviving row points at this file. The same
// picture sent in two conversations is stored once and referenced twice, so the
// last reference is what makes it safe to unlink.
const stillReferencedStatement = `SELECT 1 FROM chat_record_files WHERE path = $1 LIMIT 1`

func (w *writer) expireFiles(ctx context.Context, cutoff time.Time, why string) {
	removed := 0
	for {
		if ctx.Err() != nil {
			return
		}
		batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		rows, err := w.pool.Query(batchCtx, expireFilesStatement, cutoff, sweepBatch)
		if err != nil {
			cancel()
			common.SysError("chat record: " + why + " could not remove attachment rows: " + err.Error())
			return
		}
		paths := make([]string, 0, sweepBatch)
		touched := make(map[int64]struct{}, sweepBatch)
		for rows.Next() {
			var recordID int64
			var path string
			if err := rows.Scan(&recordID, &path); err != nil {
				continue
			}
			touched[recordID] = struct{}{}
			if path != "" {
				paths = append(paths, path)
			}
		}
		rows.Close()

		for _, path := range paths {
			var exists int
			err := w.pool.QueryRow(batchCtx, stillReferencedStatement, path).Scan(&exists)
			if err == nil {
				continue // another turn still shows this file
			}
			resolved, err := ResolveStoredPath(path)
			if err != nil {
				common.SysError("chat record: refusing to delete " + path + ": " + err.Error())
				continue
			}
			if err := os.Remove(resolved); err != nil && !os.IsNotExist(err) {
				common.SysError("chat record: could not delete " + resolved + ": " + err.Error())
				continue
			}
			// The day's folder goes too once it is empty. Remove refuses a
			// directory with anything in it, which is exactly the test wanted.
			_ = os.Remove(filepath.Dir(resolved))
			removed++
		}

		// The turns those files belonged to name their attachments by id. A
		// pruned file has to leave that list too, or the row points at rows
		// that no longer exist.
		for recordID := range touched {
			if _, err := w.pool.Exec(batchCtx, countFilesStatement, recordID); err != nil {
				common.SysError("chat record: " + why +
					" could not refresh a turn's attachment list: " + err.Error())
			}
		}
		cancel()

		if len(paths) < sweepBatch {
			break
		}
	}
	if removed > 0 {
		common.SysLog("chat record: " + why + " removed " + strconv.Itoa(removed) + " attachments")
	}
}

const expireRecordsStatement = `
DELETE FROM chat_records
 WHERE id IN (SELECT id FROM chat_records WHERE created_at < $1 ORDER BY id LIMIT $2)`

func (w *writer) expireRecords(ctx context.Context, cutoff time.Time) {
	removed := int64(0)
	for {
		if ctx.Err() != nil {
			return
		}
		batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		tag, err := w.pool.Exec(batchCtx, expireRecordsStatement, cutoff, sweepBatch)
		cancel()
		if err != nil {
			common.SysError("chat record: record retention failed: " + err.Error())
			return
		}
		removed += tag.RowsAffected()
		if tag.RowsAffected() < sweepBatch {
			break
		}
	}
	if removed > 0 {
		common.SysLog("chat record: record retention removed " + strconv.Itoa(int(removed)) + " turns")
	}
}
