package chatrecord

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
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

	marker := "live-test-" + time.Now().Format("150405.000")
	Submit(Turn{
		RequestID:    marker,
		UserID:       7,
		TokenID:      42,
		TokenName:    "live-test-key",
		StaffID:      "A1024",
		ModelName:    "gpt-5.6-sol",
		Endpoint:     "/v1/chat/completions",
		StatusCode:   200,
		CreatedAt:    time.Now(),
		RequestBody:  []byte(`{"messages":[{"role":"user","content":"海边有多远"}]}`),
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
}
