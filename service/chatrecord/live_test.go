package chatrecord

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgainstLiveDatabase exercises the whole storage path — DDL, the insert
// statement's columns, and what actually lands in the row — against a real
// PostgreSQL. Set CHATRECORD_TEST_DSN to run it; it creates and then drops the
// chat_records table, so point it at a database you are willing to have touched.
func TestAgainstLiveDatabase(t *testing.T) {
	dsn := os.Getenv("CHATRECORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHATRECORD_TEST_DSN to run the live storage test")
	}

	if err := InitSchema(dsn); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	pool, err := newPool(dsn)
	if err != nil {
		t.Fatalf("newPool: %v", err)
	}
	defer pool.Close()
	if os.Getenv("CHATRECORD_TEST_KEEP") == "" {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files CASCADE"); err != nil {
				t.Logf("cleanup: %v", err)
			}
			if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records CASCADE"); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}()
	}

	// Running it twice must be safe: the operator can press the button again.
	if err := InitSchema(dsn); err != nil {
		t.Fatalf("InitSchema is not repeatable: %v", err)
	}

	cfg := operation_setting.GetChatRecordSetting()
	prevEnabled, prevDSN := cfg.Enabled, cfg.DSN
	t.Cleanup(func() {
		// Stop first: restoring the settings while a worker is still reading
		// them is a data race, and it hides any real one behind the noise.
		Stop()
		cfg.Enabled, cfg.DSN = prevEnabled, prevDSN
	})
	cfg.Enabled, cfg.DSN = true, dsn

	// Attachments go under a temporary root so the test leaves nothing behind.
	fileRoot := t.TempDir()
	prevRoot, prevStore := cfg.FileRoot, cfg.StoreFiles
	cfg.FileRoot, cfg.StoreFiles = fileRoot, true
	t.Cleanup(func() { Stop(); cfg.FileRoot, cfg.StoreFiles = prevRoot, prevStore })

	marker := "live-test-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID:  marker,
		UserID:     7,
		TokenID:    42,
		TokenName:  "live-test-key",
		StaffID:    "A1024",
		ModelName:  "gpt-5.6-sol",
		Endpoint:   "/v1/chat/completions",
		StatusCode: 200,
		CreatedAt:  time.Now(),
		RequestBody: []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"text","text":"海边有多远"},` +
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + onePixelPNG + `"}}]}]}`),
		ResponseBody: []byte(`{"choices":[{"message":{"role":"assistant","content":"大约三公里。"}}]}`),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var staffID, userMessage, aiMessage, tokenName string
	for deadline := time.Now().Add(15 * time.Second); ; {
		err = pool.QueryRow(ctx,
			"SELECT staff_id, token_name, user_message, ai_message FROM chat_records WHERE request_id = $1",
			marker).Scan(&staffID, &tokenName, &userMessage, &aiMessage)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the turn never reached the table: %v", err)
	}

	if staffID != "A1024" {
		t.Errorf("staff_id = %q, want %q", staffID, "A1024")
	}
	if tokenName != "live-test-key" {
		t.Errorf("token_name = %q", tokenName)
	}
	if userMessage != "海边有多远" {
		t.Errorf("user_message = %q", userMessage)
	}
	if aiMessage != "大约三公里。" {
		t.Errorf("ai_message = %q", aiMessage)
	}

	// The attachment must be on disk under the staff id, with only its path in
	// the database.
	var recordID int64
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT id FROM chat_records WHERE request_id = $1", marker).Scan(&recordID))

	// The attachment row is written after the record, so it needs the same
	// patience the record needed — a single shot here is a test that passes
	// only when the machine happens to be idle.
	var storedPath, kind, mediaType string
	var size int64
	for deadline := time.Now().Add(15 * time.Second); ; {
		err = pool.QueryRow(ctx,
			"SELECT path, kind, media_type, file_size FROM chat_record_files WHERE record_id = $1",
			recordID).Scan(&storedPath, &kind, &mediaType, &size)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the attachment never reached the table: %v", err)
	}
	if !strings.HasPrefix(storedPath, "A1024/") {
		t.Errorf("path = %q, want it filed under the staff id", storedPath)
	}
	if kind != "image" || mediaType != "image/png" {
		t.Errorf("kind/media = %q/%q", kind, mediaType)
	}
	onDisk := filepath.Join(fileRoot, filepath.FromSlash(storedPath))
	info, err := os.Stat(onDisk)
	if err != nil {
		t.Fatalf("the file is not on disk: %v", err)
	}
	if info.Size() != size {
		t.Errorf("size on disk %d, recorded %d", info.Size(), size)
	}

	// And the serving lookup finds it back.
	file, err := LookupFile(dsn, mustFileID(t, pool, ctx, recordID))
	require.NoError(t, err)
	if file.Path != storedPath {
		t.Errorf("LookupFile path = %q, want %q", file.Path, storedPath)
	}
}

func mustFileID(t *testing.T, pool *pgxpool.Pool, ctx context.Context, recordID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT id FROM chat_record_files WHERE record_id = $1", recordID).Scan(&id))
	return id
}

// An agent working on one question sends the conversation again on every tool
// round-trip. Those requests must fold into the one turn they belong to, and
// the row must end up holding the whole answer — not a hundred rows, most of
// them a tool call with nothing in them.
func TestAnAgentLoopFoldsIntoOneRow(t *testing.T) {
	dsn := os.Getenv("CHATRECORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHATRECORD_TEST_DSN to run the live storage test")
	}
	if err := InitSchema(dsn); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	pool, err := newPool(dsn)
	if err != nil {
		t.Fatalf("newPool: %v", err)
	}
	defer pool.Close()
	if os.Getenv("CHATRECORD_TEST_KEEP") == "" {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files CASCADE")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records CASCADE")
		}()
	}

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { Stop(); *cfg = previous })
	cfg.Enabled, cfg.DSN, cfg.Host, cfg.StoreFiles = true, dsn, "", false

	question := "把首页改成奢品风格 " + time.Now().Format("150405.000")
	// The client replays the same conversation each round-trip; only the tail
	// of tool results grows, and the newest user message never changes.
	request := func(toolRounds int) []byte {
		body := `{"prompt_cache_key":"sess-` + question + `","input":[` +
			`{"role":"user","content":[{"type":"input_text","text":"` + question + `"}]}`
		for i := 0; i < toolRounds; i++ {
			body += `,{"type":"function_call_output","call_id":"c","output":"done"}`
		}
		return []byte(body + `]}`)
	}
	reply := func(text string) []byte {
		return []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}]}`)
	}

	// Twelve requests for one question: some answer with text, most are pure
	// tool calls and carry no reply at all.
	for i := 0; i < 12; i++ {
		body := reply("")
		switch i {
		case 0:
			body = reply("先看现有版式")
		case 5:
			body = reply("官网可以访问")
		case 11:
			body = reply("方案定为三层")
		}
		Submit(Turn{
			RequestID: "req", TokenID: 4242, TokenName: "live-test-key", StaffID: "A1024",
			ModelName: "gpt-5.6-sol", Endpoint: "/v1/responses", StatusCode: 200,
			CreatedAt: time.Now(), RequestBody: request(i), ResponseBody: body,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var rows int64
	var requestCount int
	var stored string
	for deadline := time.Now().Add(20 * time.Second); ; {
		err = pool.QueryRow(ctx,
			`SELECT count(*), coalesce(max(request_count),0), coalesce(max(ai_message),'')
			 FROM chat_records WHERE user_message = $1`, question).
			Scan(&rows, &requestCount, &stored)
		if err == nil && requestCount == 12 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("reading the turn back: %v", err)
	}

	if rows != 1 {
		t.Fatalf("the question is recorded on %d rows, want 1", rows)
	}
	if requestCount != 12 {
		t.Errorf("request_count = %d, want 12", requestCount)
	}
	for _, fragment := range []string{"先看现有版式", "官网可以访问", "方案定为三层"} {
		if !strings.Contains(stored, fragment) {
			t.Errorf("the stored answer lost %q; it holds %q", fragment, stored)
		}
	}
}

// A person asks something; the agent then makes a dozen tool round-trips on the
// same turn. Those later requests must not demote the row: the turn is still a
// person's question, and a memory built from these rows would otherwise never
// see it.
func TestToolRoundTripsDoNotDemoteAPersonsTurn(t *testing.T) {
	dsn := os.Getenv("CHATRECORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHATRECORD_TEST_DSN to run the live storage test")
	}
	if err := InitSchema(dsn); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	pool, err := newPool(dsn)
	if err != nil {
		t.Fatalf("newPool: %v", err)
	}
	defer pool.Close()
	if os.Getenv("CHATRECORD_TEST_KEEP") == "" {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files CASCADE")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records CASCADE")
		}()
	}

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { Stop(); *cfg = previous })
	cfg.Enabled, cfg.DSN, cfg.Host, cfg.StoreFiles = true, dsn, "", false

	question := "代码审查一遍 " + time.Now().Format("150405.000")
	turnID := "turn-" + time.Now().Format("150405.000")
	meta := `{"thread_source":"user","request_kind":"turn","turn_id":"` + turnID +
		`","session_id":"sess-1"}`

	body := func(toolRounds int) []byte {
		out := `{"client_metadata":{"x-codex-turn-metadata":` + strconvQuote(meta) + `},"input":[` +
			`{"role":"user","content":[{"type":"input_text","text":"` + question + `"}]}`
		for i := 0; i < toolRounds; i++ {
			out += `,{"type":"function_call_output","call_id":"c","output":"done"}`
		}
		return []byte(out + `]}`)
	}

	for i := 0; i < 6; i++ {
		Submit(Turn{
			RequestID: "req", TokenID: 4243, TokenName: "live-test-key", StaffID: "A1024",
			ModelName: "gpt-5.6-sol", Endpoint: "/v1/responses", StatusCode: 200,
			CreatedAt: time.Now(), RequestBody: body(i),
			ResponseBody: []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"看完了"}]}]}`),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var rows int
	var source, confidence, human string
	for deadline := time.Now().Add(20 * time.Second); ; {
		err = pool.QueryRow(ctx, `SELECT count(*), coalesce(max(source),''),
		    coalesce(max(source_confidence),''), coalesce(max(human_message),'')
		  FROM chat_records WHERE user_message = $1`, question).
			Scan(&rows, &source, &confidence, &human)
		if err == nil && rows == 1 && source != "" {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("reading the turn back: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the turn is on %d rows, want 1", rows)
	}
	if source != SourceHuman {
		t.Errorf("source = %q after the tool round-trips, want human", source)
	}
	if confidence != ConfidenceHard {
		t.Errorf("confidence = %q, want hard", confidence)
	}
	if human != question {
		t.Errorf("human_message = %q, want the question itself", human)
	}
}

// strconvQuote embeds the metadata the way a client does: a JSON string holding
// JSON.
func strconvQuote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(append(out, '"'))
}

// The attachments belong to a turn, and the database should be the one saying
// so. Left as a convention, nothing stops a row pointing at a turn that was
// deleted, and nothing cleans up when one is.
func TestAttachmentsAreTiedToTheirTurn(t *testing.T) {
	dsn := os.Getenv("CHATRECORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHATRECORD_TEST_DSN to run the live storage test")
	}
	if err := InitSchema(dsn); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	pool, err := newPool(dsn)
	if err != nil {
		t.Fatalf("newPool: %v", err)
	}
	defer pool.Close()
	if os.Getenv("CHATRECORD_TEST_KEEP") == "" {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files CASCADE")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records CASCADE")
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var recordID int64
	err = pool.QueryRow(ctx, `INSERT INTO chat_records
	  (request_id, user_id, token_id, token_name, staff_id, model_name, endpoint,
	   status_code, user_message, ai_message, created_at, updated_at, turn_key)
	  VALUES ('fk-probe',1,1,'k','A1','m','/v1/x',200,'hi','there',now(),now(),'')
	  RETURNING id`).Scan(&recordID)
	if err != nil {
		t.Fatalf("inserting a turn: %v", err)
	}

	// An attachment on a turn that does not exist must be refused outright.
	_, err = pool.Exec(ctx, insertFileStatement,
		recordID+9_000_000, "A1", "image", OriginPrompt, "image/png", "x.png", 10, "deadbeef", "p/x.png", "", time.Now())
	if err == nil {
		t.Error("an attachment pointing at a turn that does not exist was accepted")
	}

	// A real one is accepted and counted on the turn.
	_, err = pool.Exec(ctx, insertFileStatement,
		recordID, "A1", "image", OriginPrompt, "image/png", "x.png", 10, "cafebabe", "p/x.png", "", time.Now())
	if err != nil {
		t.Fatalf("inserting an attachment: %v", err)
	}
	if _, err := pool.Exec(ctx, countFilesStatement, recordID); err != nil {
		t.Fatalf("updating the tally: %v", err)
	}
	var tally int
	if err := pool.QueryRow(ctx, `SELECT file_count FROM chat_records WHERE id = $1`, recordID).Scan(&tally); err != nil {
		t.Fatal(err)
	}
	if tally != 1 {
		t.Errorf("file_count = %d, want 1 — the turn cannot say it has an attachment", tally)
	}

	// Removing the turn takes its attachments with it, instead of leaving rows
	// pointing at nothing.
	if _, err := pool.Exec(ctx, `DELETE FROM chat_records WHERE id = $1`, recordID); err != nil {
		t.Fatalf("deleting the turn: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM chat_record_files WHERE record_id = $1`, recordID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d attachment rows outlived their turn", left)
	}
}

// An agent sends the same turn again on every tool round-trip. The transcript
// folds those into one row; the memory writer has to be told which of them was
// the first, or the same sentence is fed to the memory once per round-trip and
// the same fact is derived over and over.
func TestOnlyTheFirstRequestOfATurnIsNew(t *testing.T) {
	dsn := os.Getenv("CHATRECORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHATRECORD_TEST_DSN to run the live storage test")
	}
	if err := InitSchema(dsn); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	pool, err := newPool(dsn)
	if err != nil {
		t.Fatalf("newPool: %v", err)
	}
	defer pool.Close()
	if os.Getenv("CHATRECORD_TEST_KEEP") == "" {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files CASCADE")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records CASCADE")
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	turnKey := "probe-turn-" + time.Now().Format("150405.000")
	write := func(reply string) bool {
		var id int64
		var inserted bool
		err := pool.QueryRow(ctx, insertStatement,
			"req", 1, 4242, "k", "A1024", "gpt-5.6-sol", "/v1/responses", 200,
			"把首页改成奢品风格", reply, time.Now(), turnKey, 32000,
			"human", "hard", "client.thread_source", "codex", "user", "t1", "s1",
			"把首页改成奢品风格", 5).Scan(&id, &inserted)
		if err != nil {
			t.Fatalf("writing the turn: %v", err)
		}
		return inserted
	}

	if !write("先看现有版式") {
		t.Fatal("the first request of a turn was not reported as new")
	}
	for i := 0; i < 4; i++ {
		if write("") {
			t.Fatalf("round-trip %d was reported as a new turn; the memory would "+
				"receive the same sentence again", i+2)
		}
	}

	// And the transcript still holds exactly one row for the turn.
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM chat_records WHERE turn_key = $1`, turnKey).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("the turn occupies %d rows, want 1", rows)
	}
}

// liveWriter points the recorder at a scratch database and hands back a pool to
// check what landed. The two tests below both need the whole path — the client
// parsing, the key choice and the insert — not just the SQL.
func liveWriter(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CHATRECORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHATRECORD_TEST_DSN to run the live storage test")
	}
	require.NoError(t, InitSchema(dsn))
	pool, err := newPool(dsn)
	require.NoError(t, err)

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() {
		Stop()
		StopMemory()
		*cfg = previous
		if os.Getenv("CHATRECORD_TEST_KEEP") == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files CASCADE")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records CASCADE")
		}
		pool.Close()
	})
	cfg.Enabled, cfg.DSN, cfg.StoreFiles = true, dsn, false
	cfg.MemoryEnabled = false
	return pool
}

func codexBody(turnID, sessionID, text string) []byte {
	meta, _ := json.Marshal(map[string]string{
		"thread_source": "user",
		"request_kind":  "turn",
		"turn_id":       turnID,
		"session_id":    sessionID,
	})
	body, _ := json.Marshal(map[string]any{
		"client_metadata": map[string]string{"x-codex-turn-metadata": string(meta)},
		"messages":        []any{map[string]string{"role": "user", "content": text}},
	})
	return body
}

func awaitRow(t *testing.T, pool *pgxpool.Pool, requestID string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var id int64
	var err error
	for deadline := time.Now().Add(15 * time.Second); ; {
		err = pool.QueryRow(ctx,
			"SELECT id FROM chat_records WHERE request_id = $1", requestID).Scan(&id)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	require.NoError(t, err, "the turn never reached the table")
	return id
}

// A client naming its own turn is trusted about which requests belong
// together, never about uniqueness: turn_key is unique across the whole table
// and the id arrives in the request body. Taken at face value, two keys sending
// the same one land on a single row — the second person's reply appended to the
// first person's message, under the first person's staff number.
func TestAClientsOwnTurnIdDoesNotMergeTwoKeys(t *testing.T) {
	pool := liveWriter(t)

	shared := "shared-turn-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID: "mine-" + shared, UserID: 1, TokenID: 111,
		TokenName: "A的key", StaffID: "10000001", ModelName: "gpt-5.6-sol",
		Endpoint: "/v1/responses", StatusCode: 200, CreatedAt: time.Now(),
		RequestBody:  codexBody(shared, "session-a", "A 说的话"),
		ResponseBody: []byte(`{"choices":[{"message":{"content":"给 A 的回复"}}]}`),
	})
	Submit(Turn{
		RequestID: "theirs-" + shared, UserID: 2, TokenID: 222,
		TokenName: "B的key", StaffID: "10000002", ModelName: "gpt-5.6-sol",
		Endpoint: "/v1/responses", StatusCode: 200, CreatedAt: time.Now(),
		RequestBody:  codexBody(shared, "session-b", "B 说的话"),
		ResponseBody: []byte(`{"choices":[{"message":{"content":"给 B 的回复"}}]}`),
	})

	mine := awaitRow(t, pool, "mine-"+shared)
	theirs := awaitRow(t, pool, "theirs-"+shared)
	assert.NotEqual(t, mine, theirs, "two keys sharing a turn id landed on one row")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var staffID, user, ai string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT staff_id, user_message, ai_message FROM chat_records WHERE id = $1`,
		mine).Scan(&staffID, &user, &ai))
	assert.Equal(t, "10000001", staffID)
	assert.Equal(t, "A 说的话", user)
	assert.NotContains(t, ai, "给 B 的回复", "B's answer was filed under A")

	// The same key sending the same turn id must still fold, or the folding
	// this key exists for is gone.
	Submit(Turn{
		RequestID: "again-" + shared, UserID: 1, TokenID: 111,
		TokenName: "A的key", StaffID: "10000001", ModelName: "gpt-5.6-sol",
		Endpoint: "/v1/responses", StatusCode: 200, CreatedAt: time.Now(),
		RequestBody:  codexBody(shared, "session-a", "A 说的话"),
		ResponseBody: []byte(`{"choices":[{"message":{"content":"接着说"}}]}`),
	})
	var folded int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT request_count FROM chat_records WHERE id = $1`, mine).Scan(&folded))
		if folded > 1 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	assert.Greater(t, folded, 1, "a second request of the same turn did not fold")
}

// Several identifying columns are fixed width and hold the client's own words.
// Postgres does not shorten an oversized value, it refuses the row — so an
// unbounded one costs the whole turn, silently, with only a line in the log.
// prompt_cache_key is the one that bites: it is a documented request field that
// callers set to whatever they like.
func TestOversizedClientValuesDoNotCostTheTurn(t *testing.T) {
	pool := liveWriter(t)

	marker := "oversized-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID: marker, UserID: 3, TokenID: 333,
		TokenName: strings.Repeat("名", 300),
		StaffID:   "10000003",
		ModelName: strings.Repeat("m", 300),
		Endpoint:  "/v1/responses", StatusCode: 200, CreatedAt: time.Now(),
		RequestBody: codexBody(
			strings.Repeat("t", 300),
			strings.Repeat("s", 300),
			"这句话必须留下来"),
		ResponseBody: []byte(`{"choices":[{"message":{"content":"收到"}}]}`),
	})

	id := awaitRow(t, pool, marker)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var user, ai, sessionID, turnID, model string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT user_message, ai_message, client_session_id, client_turn_id, model_name
		   FROM chat_records WHERE id = $1`, id).
		Scan(&user, &ai, &sessionID, &turnID, &model))

	assert.Equal(t, "这句话必须留下来", user, "the turn's content survived the clipping")
	assert.Equal(t, "收到", ai)
	assert.Len(t, []rune(sessionID), 64)
	assert.Len(t, []rune(turnID), 64)
	assert.Len(t, []rune(model), 128)
}

// The seven readers below the entry point each used to validate the body
// themselves. Collapsing that to one scan is only safe if the one that remains
// still refuses a body that is not JSON: gjson will happily find "content":"…"
// inside an audio upload's bytes and store it as somebody's question.
func TestANonJsonBodyIsNotMinedForText(t *testing.T) {
	pool := liveWriter(t)

	marker := "notjson-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID: marker, UserID: 5, TokenID: 555, TokenName: "k", StaffID: "10000005",
		ModelName: "gpt-5.6-sol", Endpoint: "/v1/audio/transcriptions", StatusCode: 200,
		CreatedAt: time.Now(),
		// A multipart upload whose bytes happen to contain something
		// message-shaped. It is not JSON, so none of it is a question.
		RequestBody: []byte("------form-boundary\r\nContent-Disposition: form-data\r\n\r\n" +
			`{"messages":[{"role":"user","content":"这不是用户说的话"}]}` + "\r\n------form-boundary--"),
		ResponseBody: []byte(`{"choices":[{"message":{"content":"转写结果"}}]}`),
	})

	id := awaitRow(t, pool, marker)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var user, ai string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT user_message, ai_message FROM chat_records WHERE id = $1`, id).Scan(&user, &ai))
	assert.Empty(t, user, "text was mined out of a body that is not JSON")
	assert.Equal(t, "转写结果", ai, "the reply is JSON and should still be read")
}

// An agent's first request for a turn very often carries no prose at all, only
// tool calls. Telling the memory store then handed it a person's question with
// no answer attached; waiting, and letting the row decide who sends, gives it
// the pair.
func TestTheMemoryWaitsForAnAnswerAndIsToldOnce(t *testing.T) {
	pool := liveWriter(t)

	var sent atomic.Int64
	seen := make(chan map[string]any, 8)
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent.Add(1)
			select {
			case seen <- body:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer store.Close()
	defer StopMemory()

	cfg := operation_setting.GetChatRecordSetting()
	cfg.MemoryEnabled = true
	cfg.MemoryBaseURL = store.URL
	cfg.MemoryAPIKey = "test-key"
	cfg.MemoryWorkspace = "yxsy"
	cfg.MemoryPeerTemplate = "{staff_id}"
	cfg.MemoryAssistantPeer = "codex-{staff_id}"
	cfg.MemoryMinChars = 2
	StopMemory()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	turnID := "answer-later-" + time.Now().Format("150405.000")
	send := func(request, reply string) {
		Submit(Turn{
			RequestID: request, UserID: 6, TokenID: 666, TokenName: "k", StaffID: "10000006",
			ModelName: "gpt-5.6-sol", Endpoint: "/v1/responses", StatusCode: 200,
			CreatedAt:    time.Now(),
			RequestBody:  codexBody(turnID, "sess", "把首页改成奢品风格"),
			ResponseBody: []byte(`{"choices":[{"message":{"content":"` + reply + `"}}]}`),
		})
	}

	// The turn folds, so only the first request keeps its id on the row; the
	// later ones show up as request_count going forward.
	awaitRequests := func(id int64, want int) {
		t.Helper()
		var count int
		for i := 0; i < 100; i++ {
			require.NoError(t, pool.QueryRow(ctx,
				`SELECT request_count FROM chat_records WHERE id = $1`, id).Scan(&count))
			if count >= want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("the turn shows %d requests, want %d", count, want)
	}

	// Two tool round-trips: the turn is in the table, the memory hears nothing.
	send("tool-1-"+turnID, "")
	id := awaitRow(t, pool, "tool-1-"+turnID)
	send("tool-2-"+turnID, "")
	awaitRequests(id, 2)
	time.Sleep(700 * time.Millisecond)
	require.Equal(t, int64(0), sent.Load(),
		"the memory was told about a turn that had no answer yet")

	// Then the answer arrives.
	send("answer-"+turnID, "先看现有版式")
	awaitRequests(id, 3)

	select {
	case body := <-seen:
		messages, _ := body["messages"].([]any)
		require.Len(t, messages, 2, "the person's words and the answer should both go")
		first, _ := messages[0].(map[string]any)
		second, _ := messages[1].(map[string]any)
		assert.Equal(t, "把首页改成奢品风格", first["content"])
		assert.Equal(t, "先看现有版式", second["content"])
	case <-time.After(10 * time.Second):
		t.Fatal("the memory was never told, even once there was an answer")
	}

	// And more round-trips afterwards must not tell it again.
	send("after-1-"+turnID, "继续说")
	awaitRequests(id, 4)
	time.Sleep(700 * time.Millisecond)
	assert.Equal(t, int64(1), sent.Load(), "the same turn was sent to the memory twice")
}

// Nothing is pruned unless the operator has said how long to keep it, and when
// they have, the attachments have to leave the disk as well as the table — a
// cascade can delete a row but it cannot unlink a file.
func TestRetentionRemovesOldTurnsAndTheirFiles(t *testing.T) {
	pool := liveWriter(t)

	cfg := operation_setting.GetChatRecordSetting()
	root := t.TempDir()
	cfg.FileRoot = root

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// One old turn with an attachment, one from today.
	old, recent := time.Now().AddDate(0, 0, -40), time.Now()
	write := func(marker string, at time.Time) int64 {
		var id int64
		var inserted bool
		require.NoError(t, pool.QueryRow(ctx, insertStatement,
			marker, 1, 777, "k", "10000007", "gpt-5.6-sol", "/v1/responses", 200,
			"一句话", "一句回答", at, "", 32000,
			"human", "hard", "client.thread_source", "codex", "user", marker, "s",
			"一句话", 5).Scan(&id, &inserted))
		return id
	}
	oldID, recentID := write("retention-old", old), write("retention-new", recent)

	attach := func(recordID int64, name string, at time.Time) string {
		relative := filepath.Join("10000007", at.Format("2006-01-02"), name)
		full := filepath.Join(root, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("x"), 0o644))
		_, err := pool.Exec(ctx, insertFileStatement,
			recordID, "10000007", "image", OriginPrompt, "image/png", name, 1, name, relative, "", at)
		require.NoError(t, err)
		return full
	}
	oldFile := attach(oldID, "old.png", old)
	recentFile := attach(recentID, "new.png", recent)

	writer := &writer{pool: pool, totals: &totals{}}

	// Bring both rows' attachment lists up to date, so "the list was emptied"
	// is a real assertion rather than one that was empty all along.
	for _, id := range []int64{oldID, recentID} {
		_, err := pool.Exec(ctx, countFilesStatement, id)
		require.NoError(t, err)
	}
	var seeded []int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT prompt_file_ids FROM chat_records WHERE id = $1`, oldID).Scan(&seeded))
	require.Len(t, seeded, 1, "the turn did not list its attachment to begin with")

	// Retention off: a sweep must remove nothing at all.
	cfg.FileRetentionDays, cfg.RecordRetentionDays = 0, 0
	writer.sweepOnce(ctx)
	assert.FileExists(t, oldFile, "a sweep deleted an attachment with retention switched off")

	// Attachments only: the old file goes, the old turn stays.
	cfg.FileRetentionDays = 30
	writer.sweepOnce(ctx)
	assert.NoFileExists(t, oldFile, "the expired attachment is still on disk")
	assert.FileExists(t, recentFile, "an attachment inside the window was deleted")

	var rows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM chat_records WHERE id = $1`, oldID).Scan(&rows))
	assert.Equal(t, 1, rows, "attachment retention removed the turn as well")

	// The turn names its attachments by id. A pruned file has to leave that
	// list too, or the row points at rows that no longer exist.
	var ids []int64
	var counted int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT prompt_file_ids, file_count FROM chat_records WHERE id = $1`,
		oldID).Scan(&ids, &counted))
	assert.Empty(t, ids, "the turn still lists an attachment that was pruned")
	assert.Equal(t, 0, counted, "the turn's tally still counts a pruned attachment")

	// Records too.
	cfg.RecordRetentionDays = 30
	writer.sweepOnce(ctx)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM chat_records WHERE id = $1`, oldID).Scan(&rows))
	assert.Equal(t, 0, rows, "the expired turn is still in the table")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM chat_records WHERE id = $1`, recentID).Scan(&rows))
	assert.Equal(t, 1, rows, "a turn inside the window was deleted")
}

// The schema statements and the first writes used to run at the same time.
// That was documented as costing the first few turns after a restart; in
// practice it cost more than that, because the migration takes table locks in
// one order and an insert takes them in the other — the writes did not queue
// behind it, they deadlocked against it.
//
// Starting from an empty database is what exercises it: that is when the
// statements have real work to do.
func TestTurnsSubmittedDuringMigrationAreNotLost(t *testing.T) {
	dsn := os.Getenv("CHATRECORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHATRECORD_TEST_DSN to run the live storage test")
	}
	pool, err := newPool(dsn)
	require.NoError(t, err)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files CASCADE")
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records CASCADE")

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() {
		Stop()
		*cfg = previous
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = pool.Exec(clean, "DROP TABLE IF EXISTS chat_record_files CASCADE")
		_, _ = pool.Exec(clean, "DROP TABLE IF EXISTS chat_records CASCADE")
	})
	cfg.Enabled, cfg.DSN, cfg.StoreFiles = true, dsn, false
	cfg.MemoryEnabled = false
	Stop()

	// No InitSchema first: the very first Submit is what brings the writer up,
	// and the statements run while these turns are already queued.
	const turns = 30
	marker := "migrating-" + time.Now().Format("150405.000")
	for i := 0; i < turns; i++ {
		Submit(Turn{
			RequestID: fmt.Sprintf("%s-%d", marker, i),
			UserID:    8, TokenID: 888, TokenName: "k", StaffID: "10000008",
			ModelName: "gpt-5.6-sol", Endpoint: "/v1/chat/completions", StatusCode: 200,
			CreatedAt:    time.Now(),
			RequestBody:  []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":"第 %d 句"}]}`, i)),
			ResponseBody: []byte(`{"choices":[{"message":{"content":"收到"}}]}`),
		})
	}

	var landed int
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		err = pool.QueryRow(ctx,
			`SELECT count(*) FROM chat_records WHERE request_id LIKE $1`, marker+"-%").Scan(&landed)
		if err == nil && landed == turns {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	assert.Equal(t, turns, landed, "turns submitted while the schema was being built were lost")
}

// The attachment table has always said which turn a file belongs to. What it
// could not say is which half of the exchange it came with — and it never held
// what the model produced at all, because only the request was ever read. Both
// together are what makes "which question was this picture drawn for" a
// question the table can answer.
func TestBothHalvesOfATurnKeepTheirAttachments(t *testing.T) {
	pool := liveWriter(t)

	cfg := operation_setting.GetChatRecordSetting()
	cfg.FileRoot = t.TempDir()
	cfg.StoreFiles = true

	sent := base64.StdEncoding.EncodeToString([]byte("\x89PNG-the-one-they-sent"))
	drawn := base64.StdEncoding.EncodeToString([]byte("\x89PNG-the-one-it-drew"))

	marker := "bothsides-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID: marker, UserID: 9, TokenID: 999, TokenName: "k", StaffID: "10000009",
		ModelName: "gpt-5.6-sol", Endpoint: "/v1/responses", StatusCode: 200,
		CreatedAt: time.Now(),
		RequestBody: []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"text","text":"照这张图的风格再画一张"},` +
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + sent + `"}}]}]}`),
		ResponseBody: []byte(`{"output":[{"type":"image_generation_call","result":"` + drawn + `"}]}`),
	})

	id := awaitRow(t, pool, marker)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var promptIDs, replyIDs []int64
	for deadline := time.Now().Add(15 * time.Second); ; {
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT prompt_file_ids, reply_file_ids FROM chat_records WHERE id = $1`,
			id).Scan(&promptIDs, &replyIDs))
		if len(promptIDs) == 1 && len(replyIDs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Read from the message side, which is the point: the row names its own
	// attachments instead of leaving the reader to work out the join.
	require.Len(t, promptIDs, 1, "the picture sent with the question is not on the message row")
	require.Len(t, replyIDs, 1, "the picture the model drew is not on the message row")

	var origin, kind string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT origin, kind FROM chat_record_files WHERE id = $1`, replyIDs[0]).Scan(&origin, &kind))
	assert.Equal(t, "reply", origin)
	assert.Equal(t, "image", kind)

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT origin FROM chat_record_files WHERE id = $1`, promptIDs[0]).Scan(&origin))
	assert.Equal(t, "prompt", origin)

	var counted int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT file_count FROM chat_records WHERE id = $1`, id).Scan(&counted))
	assert.Equal(t, 2, counted, "the turn's own tally missed one of them")
}

// One picture, sent and handed straight back. Keyed on the turn and the hash
// alone, the second one was swallowed as a duplicate and the answer looked
// empty; the direction is part of what makes them two facts rather than one.
func TestTheSamePictureSentAndReturnedIsBothThings(t *testing.T) {
	pool := liveWriter(t)

	cfg := operation_setting.GetChatRecordSetting()
	cfg.FileRoot = t.TempDir()
	cfg.StoreFiles = true

	same := base64.StdEncoding.EncodeToString([]byte("\x89PNG-the-very-same-bytes"))
	marker := "echoed-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID: marker, UserID: 9, TokenID: 998, TokenName: "k", StaffID: "10000010",
		ModelName: "gpt-5.6-sol", Endpoint: "/v1/responses", StatusCode: 200,
		CreatedAt: time.Now(),
		RequestBody: []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"text","text":"把这张原样还我"},` +
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + same + `"}}]}]}`),
		ResponseBody: []byte(`{"output":[{"type":"image_generation_call","result":"` + same + `"}]}`),
	})

	id := awaitRow(t, pool, marker)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var origins []string
	for deadline := time.Now().Add(15 * time.Second); ; {
		rows, err := pool.Query(ctx,
			`SELECT origin FROM chat_record_files WHERE record_id = $1 ORDER BY origin`, id)
		require.NoError(t, err)
		origins = origins[:0]
		for rows.Next() {
			var origin string
			require.NoError(t, rows.Scan(&origin))
			origins = append(origins, origin)
		}
		rows.Close()
		if len(origins) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	assert.Equal(t, []string{"prompt", "reply"}, origins,
		"the picture is recorded once, so one of the two directions was lost")
}

// The listing endpoint selects its columns by name and scans them by position,
// which is a pairing that breaks silently the moment a column is added to one
// list and not the other — and breaks the whole attachment page with it.
func TestTheAttachmentListingReadsEveryColumnItSelects(t *testing.T) {
	pool := liveWriter(t)
	dsn := os.Getenv("CHATRECORD_TEST_DSN")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var recordID int64
	var inserted bool
	require.NoError(t, pool.QueryRow(ctx, insertStatement,
		"listing-"+time.Now().Format("150405.000"), 1, 4242, "k", "10000011",
		"gpt-5.6-sol", "/v1/responses", 200, "一句话", "一句回答", time.Now(), "", 32000,
		"human", "hard", "client.thread_source", "codex", "user", "", "",
		"一句话", 5).Scan(&recordID, &inserted))

	_, err := pool.Exec(ctx, insertFileStatement,
		recordID, "10000011", "image", OriginReply, "image/png", "drawn.png",
		12, "beefcafe", "10000011/2026-08-24/beefcafe.png", "", time.Now())
	require.NoError(t, err)

	items, err := ListFiles(dsn, "10000011", 10, 0)
	require.NoError(t, err, "the listing could not read its own columns")
	require.Len(t, items, 1)
	assert.Equal(t, recordID, items[0].RecordID, "the listing does not say which turn the file belongs to")
	assert.Equal(t, OriginReply, items[0].Origin, "the listing does not say who the file came from")
	assert.Equal(t, "drawn.png", items[0].FileName)
}

// The unit tests prove the bytes are removed; this proves the row lands. That
// is the part that was failing: PostgreSQL refuses the whole insert over one
// byte, so the turn was lost rather than stored imperfectly.
func TestATurnCarryingAStrayByteStillLands(t *testing.T) {
	pool := liveWriter(t)

	marker := "straybyte-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID: marker, UserID: 4, TokenID: 444, TokenName: "k", StaffID: "10000004",
		ModelName: "gpt-5.6-sol", Endpoint: "/v1/responses", StatusCode: 200,
		CreatedAt: time.Now(),
		RequestBody: []byte("{\"messages\":[{\"role\":\"user\",\"content\":\"" +
			"登录不上去了\\u0000请看截图\"}]}"),
		// A reply cut mid-character: a continuation byte with no lead byte.
		ResponseBody: []byte("{\"choices\":[{\"message\":{\"content\":\"我看看\xbb\"}}]}"),
	})

	id := awaitRow(t, pool, marker)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var user, ai string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT user_message, ai_message FROM chat_records WHERE id = $1`, id).Scan(&user, &ai))

	assert.Equal(t, "登录不上去了请看截图", user, "the person's words should survive the byte that was dropped")
	assert.NotContains(t, user, "\x00")
	assert.NotContains(t, ai, "\xbb")
}
