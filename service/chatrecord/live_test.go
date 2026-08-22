package chatrecord

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/jackc/pgx/v5/pgxpool"
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
