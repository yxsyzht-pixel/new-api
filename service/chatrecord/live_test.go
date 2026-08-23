package chatrecord

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
			if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files"); err != nil {
				t.Logf("cleanup: %v", err)
			}
			if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records"); err != nil {
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
		cfg.Enabled, cfg.DSN = prevEnabled, prevDSN
		Stop()
	})
	cfg.Enabled, cfg.DSN = true, dsn

	// Attachments go under a temporary root so the test leaves nothing behind.
	fileRoot := t.TempDir()
	prevRoot, prevStore := cfg.FileRoot, cfg.StoreFiles
	cfg.FileRoot, cfg.StoreFiles = fileRoot, true
	t.Cleanup(func() { cfg.FileRoot, cfg.StoreFiles = prevRoot, prevStore })

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

	var storedPath, kind, mediaType string
	var size int64
	err = pool.QueryRow(ctx,
		"SELECT path, kind, media_type, file_size FROM chat_record_files WHERE record_id = $1",
		recordID).Scan(&storedPath, &kind, &mediaType, &size)
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
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records")
		}()
	}

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; Stop() })
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
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records")
		}()
	}

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; Stop() })
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
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records")
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
		recordID+9_000_000, "A1", "image", "image/png", "x.png", 10, "deadbeef", "p/x.png", "", time.Now())
	if err == nil {
		t.Error("an attachment pointing at a turn that does not exist was accepted")
	}

	// A real one is accepted and counted on the turn.
	_, err = pool.Exec(ctx, insertFileStatement,
		recordID, "A1", "image", "image/png", "x.png", 10, "cafebabe", "p/x.png", "", time.Now())
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
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records")
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
		*cfg = previous
		Stop()
		if os.Getenv("CHATRECORD_TEST_KEEP") == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_record_files")
			_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS chat_records")
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
