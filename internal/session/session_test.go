package session_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/session"
	"github.com/eemax/tinyflags/internal/store"
)

func TestSQLiteStoreForkClearDeletePreservesRunLogs(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "tinyflags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sessions := session.NewSQLiteStore(db)
	source, err := sessions.LoadOrCreate("source")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendMessages(source.ID, nil, []core.Message{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatal(err)
	}

	logger := store.NewSQLiteRunLogger(db)
	sessionID := source.ID
	runID, err := logger.StartRun(core.RunRecord{
		SessionID: &sessionID,
		ModeName:  "text",
		ModelName: "model",
		Prompt:    "prompt",
		Format:    "text",
		Status:    "running",
		ExitCode:  0,
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := logger.FinishRun(runID, core.RunRecord{Status: "success", ExitCode: 0, FinishedAt: &now}); err != nil {
		t.Fatal(err)
	}

	forked, err := sessions.Fork("source", "forked")
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := sessions.GetMessages(forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Fatalf("forked messages = %+v", msgs)
	}

	if err := sessions.Clear("source"); err != nil {
		t.Fatal(err)
	}
	cleared, err := sessions.GetMessages(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 0 {
		t.Fatalf("cleared messages = %d, want 0", len(cleared))
	}

	if err := sessions.Delete("source"); err != nil {
		t.Fatal(err)
	}
	var runSessionID sql.NullInt64
	if err := db.QueryRow(`SELECT session_id FROM runs WHERE id = ?`, runID).Scan(&runSessionID); err != nil {
		t.Fatal(err)
	}
	if runSessionID.Valid {
		t.Fatalf("expected run session_id to be NULL after delete, got %d", runSessionID.Int64)
	}
}
