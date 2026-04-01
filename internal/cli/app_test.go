package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eemax/tinyflags/internal/cli"
	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/provider"
	"github.com/eemax/tinyflags/internal/session"
	"github.com/eemax/tinyflags/internal/store"
)

type scriptedProvider struct {
	responses []core.CompletionResponse
	requests  []core.CompletionRequest
}

func (p *scriptedProvider) Complete(ctx context.Context, req core.CompletionRequest) (core.CompletionResponse, error) {
	_ = ctx
	p.requests = append(p.requests, req)
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

type blockingProvider struct{}

func (p *blockingProvider) Complete(ctx context.Context, req core.CompletionRequest) (core.CompletionResponse, error) {
	_ = req
	<-ctx.Done()
	return core.CompletionResponse{}, ctx.Err()
}

type failingWriter struct {
	err error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	_ = p
	return 0, w.err
}

func TestAppRunJSONEnvelope(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	})

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if payload["result"] != "done" {
		t.Fatalf("result = %#v", payload["result"])
	}
}

func TestAppRootPromptTextMode(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	})

	code := app.Execute([]string{"--config", cfgPath, "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "done" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestAppRunJSONResultOnly(t *testing.T) {
	app, stdout, _, cfgPath := newTestApp(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	})

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "--result-only", "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q", code, stdout.String())
	}
	if got := stdout.String(); got != "\"done\"\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestAppSchemaFailureReturnsJSONErrorEnvelope(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "not-json"}}},
	})
	schemaPath := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["name"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "--output-schema", schemaPath, "hello"})
	if code != 4 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected stderr message")
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if ok, _ := payload["ok"].(bool); ok {
		t.Fatalf("expected ok=false payload: %v", payload)
	}
	if exitCode, _ := payload["exit_code"].(float64); int(exitCode) != 4 {
		t.Fatalf("exit_code = %#v", payload["exit_code"])
	}
}

func TestAppSchemaFailureMarksRunAsError(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "not-json"}}},
	})
	schemaPath := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["name"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--output-schema", schemaPath, "hello"})
	if code != 4 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var status string
	var exitCode int
	if err := db.QueryRow(`SELECT status, exit_code FROM runs ORDER BY id DESC LIMIT 1`).Scan(&status, &exitCode); err != nil {
		t.Fatal(err)
	}
	if status != "error" || exitCode != 4 {
		t.Fatalf("run status/exit = %q/%d", status, exitCode)
	}
}

func TestAppEnforcesInvocationTimeout(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &blockingProvider{})

	code := app.Execute([]string{"--config", cfgPath, "--timeout", "20ms", "hello"})
	if code != 2 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected stderr timeout message")
	}

	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var status string
	var exitCode int
	if err := db.QueryRow(`SELECT status, exit_code FROM runs ORDER BY id DESC LIMIT 1`).Scan(&status, &exitCode); err != nil {
		t.Fatal(err)
	}
	if status != "error" || exitCode != 2 {
		t.Fatalf("run status/exit = %q/%d", status, exitCode)
	}
}

func TestAppForkSessionWithoutSessionFailsUsage(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &scriptedProvider{})
	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "--fork-session", "child", "hello"})
	if code != 8 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAppModeShowResolved(t *testing.T) {
	app, stdout, _, cfgPath := newTestApp(t, &scriptedProvider{})
	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "mode", "show", "commander"})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if payload["model"] != "openai/gpt-4.1" {
		t.Fatalf("model = %#v", payload["model"])
	}
}

func TestAppSessionForkCommand(t *testing.T) {
	app, stdout, _, cfgPath := newTestApp(t, &scriptedProvider{})
	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions := session.NewSQLiteStore(db)
	src, err := sessions.LoadOrCreate("src")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendMessages(src.ID, nil, []core.Message{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "session", "fork", "src", "dst"})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.Len() == 0 {
		t.Fatal("expected stdout content")
	}

	forked, err := sessions.LoadOrCreate("dst")
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
}

func TestAppSessionExportJSON(t *testing.T) {
	app, stdout, _, cfgPath := newTestApp(t, &scriptedProvider{})
	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions := session.NewSQLiteStore(db)
	src, err := sessions.LoadOrCreate("src")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendMessages(src.ID, nil, []core.Message{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "session", "export", "src"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q", code, stdout.String())
	}
	var payload core.SessionExport
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload.Session.Name != "src" || len(payload.Messages) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAppSessionClearCommand(t *testing.T) {
	app, stdout, _, cfgPath := newTestApp(t, &scriptedProvider{})
	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions := session.NewSQLiteStore(db)
	src, err := sessions.LoadOrCreate("src")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendMessages(src.ID, nil, []core.Message{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "session", "clear", "src"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q", code, stdout.String())
	}
	msgs, err := sessions.GetMessages(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestAppDoctorJSON(t *testing.T) {
	app, stdout, _, cfgPath := newTestApp(t, &scriptedProvider{})
	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "doctor"})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if _, ok := payload["db"]; !ok {
		t.Fatalf("doctor payload missing db check: %v", payload)
	}
}

func TestAppVerboseEnablesOperationalLogs(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	})

	code := app.Execute([]string{"--config", cfgPath, "--verbose", "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected operational logs on stderr")
	}
}

func TestAppRenderFailureMarksRunAsError(t *testing.T) {
	app, _, stderr, cfgPath := newTestApp(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	})
	app.Stdout = &failingWriter{err: errors.New("write failed")}

	code := app.Execute([]string{"--config", cfgPath, "hello"})
	if code != 1 {
		t.Fatalf("exit code = %d stderr=%q", code, stderr.String())
	}

	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var status string
	var exitCode int
	if err := db.QueryRow(`SELECT status, exit_code FROM runs ORDER BY id DESC LIMIT 1`).Scan(&status, &exitCode); err != nil {
		t.Fatal(err)
	}
	if status != "error" || exitCode != 1 {
		t.Fatalf("run status/exit = %q/%d", status, exitCode)
	}
}

func TestAppConfigValidateJSON(t *testing.T) {
	app, stdout, _, cfgPath := newTestApp(t, &scriptedProvider{})
	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "config", "validate"})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestAppUsesConfigDefaultJSONForErrors(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{}, "default_format = \"json\"\n")

	code := app.Execute([]string{"--config", cfgPath, "--mode", "missing", "hello"})
	if code != 8 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected stderr output")
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if ok, _ := payload["ok"].(bool); ok {
		t.Fatalf("expected ok=false payload: %v", payload)
	}
	if exitCode, _ := payload["exit_code"].(float64); int(exitCode) != 8 {
		t.Fatalf("exit_code = %#v", payload["exit_code"])
	}
}

func TestAppRejectsUnsupportedFormat(t *testing.T) {
	app, stdout, stderr, _ := newTestApp(t, &scriptedProvider{})

	code := app.Execute([]string{"--format", "yaml", "version"})
	if code != 8 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected stderr output")
	}
}

func TestAppSkipsRunLogsWhenDisabledByMode(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	}, "default_mode = \"text\"\n[modes.text]\nstore_run_log = false\n")

	code := app.Execute([]string{"--config", cfgPath, "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("runs count = %d, want 0", count)
	}
}

func TestAppLinksPersistedSessionMessagesToRun(t *testing.T) {
	app, _, stderr, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	}, "default_mode = \"text\"\n")

	code := app.Execute([]string{"--config", cfgPath, "--session", "demo", "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%q", code, stderr.String())
	}

	db, err := store.OpenDB(dbPathFromConfig(t, cfgPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	admin := session.NewSQLiteStore(db)
	_, messages, err := admin.Show("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	for _, msg := range messages {
		if msg.RunID == nil || *msg.RunID == 0 {
			t.Fatalf("message missing run_id: %+v", msg)
		}
	}
}

func newTestApp(t *testing.T, fake provider.Provider) (*cli.App, *bytes.Buffer, *bytes.Buffer, string) {
	return newTestAppWithExtraConfig(t, fake, "")
}

func newTestAppWithExtraConfig(t *testing.T, fake provider.Provider, extraConfig string) (*cli.App, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := cli.NewApp(stdout, stderr)
	registry := provider.NewRegistry()
	registry.Register("openrouter", fake)
	app.ProviderRegistry = registry

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "tinyflags.db")
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configText := "version = 1\n" +
		"db_path = \"" + dbPath + "\"\n" +
		"skills_dir = \"" + skillsDir + "\"\n" +
		extraConfig
	if err := os.WriteFile(cfgPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}
	return app, stdout, stderr, cfgPath
}

func dbPathFromConfig(t *testing.T, cfgPath string) string {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("db_path = ")) {
			value := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("db_path = ")))
			return string(bytes.Trim(value, `"`))
		}
	}
	t.Fatal("db_path not found in config")
	return ""
}
