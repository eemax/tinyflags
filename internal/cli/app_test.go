package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eemax/tinyflags/internal/cli"
	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/provider"
	"github.com/eemax/tinyflags/internal/provider/openrouter"
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

func TestAppConfigPathDiscoversRepoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repo := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config.toml"), []byte("version = 1\ndefault_mode = \"text\"\ndefault_model = \"openai/gpt-4o-mini\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := cli.NewApp(&bytes.Buffer{}, &bytes.Buffer{})
	stdout := &bytes.Buffer{}
	app.Stdout = stdout

	restoreCWD(t, nested)

	code := app.Execute([]string{"--format", "json", "config", "path"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q", code, stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	wantPath, err := filepath.EvalSymlinks(filepath.Join(repo, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if payload["path"] != wantPath {
		t.Fatalf("path = %#v", payload["path"])
	}
}

func TestAppRunUsesRepoConfigAndModelsToml(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repo := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config.toml"), []byte("version = 1\ndefault_mode = \"text\"\ndefault_model = \"fast\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "models.toml"), []byte("[models.fast]\nid = \"openai/gpt-4o-mini\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	}
	app := cli.NewApp(&bytes.Buffer{}, &bytes.Buffer{})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app.Stdout = stdout
	app.Stderr = stderr
	registry := provider.NewRegistry()
	registry.Register("openrouter", fake)
	app.ProviderRegistry = registry

	restoreCWD(t, nested)

	code := app.Execute([]string{"hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(fake.requests) != 1 {
		t.Fatalf("requests = %+v", fake.requests)
	}
	if fake.requests[0].Model != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q", fake.requests[0].Model)
	}
}

func TestAppModeShowUsesEnvDefaultModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TINYFLAGS_DEFAULT_MODE", "text")
	t.Setenv("TINYFLAGS_DEFAULT_MODEL", "openai/gpt-4o-mini")

	app := cli.NewApp(&bytes.Buffer{}, &bytes.Buffer{})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app.Stdout = stdout
	app.Stderr = stderr

	code := app.Execute([]string{"--format", "json", "mode", "show", "text"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if payload["model"] != "openai/gpt-4o-mini" {
		t.Fatalf("model = %#v", payload["model"])
	}
}

func TestAppModeShowFailsWithoutAnyConfiguredModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	app := cli.NewApp(&bytes.Buffer{}, &bytes.Buffer{})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app.Stdout = stdout
	app.Stderr = stderr

	restoreCWD(t, t.TempDir())

	code := app.Execute([]string{"mode", "show", "text"})
	if code != 8 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "model is required") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAppRunWithExplicitConfigUsesModelsTomlFromConfigRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(repo, "config.toml")
	if err := os.WriteFile(configPath, []byte("version = 1\ndefault_mode = \"text\"\ndefault_model = \"repofast\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "models.toml"), []byte("[models.repofast]\nid = \"openai/gpt-4o-mini\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restoreCWD(t, t.TempDir())

	fake := &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	}
	app := cli.NewApp(&bytes.Buffer{}, &bytes.Buffer{})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app.Stdout = stdout
	app.Stderr = stderr
	registry := provider.NewRegistry()
	registry.Register("openrouter", fake)
	app.ProviderRegistry = registry

	code := app.Execute([]string{"--config", configPath, "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(fake.requests) != 1 {
		t.Fatalf("requests = %+v", fake.requests)
	}
	if fake.requests[0].Model != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q", fake.requests[0].Model)
	}
}

func TestAppRunAcceptsFullModelIDWithoutModelsToml(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fake := &scriptedProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	}
	app, stdout, stderr, cfgPath := newTestAppWithExtraConfig(t, fake, "default_mode = \"text\"\ndefault_model = \"\"\n")

	code := app.Execute([]string{"--config", cfgPath, "--mode", "text", "--model", "openai/gpt-4o-mini", "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(fake.requests) != 1 {
		t.Fatalf("requests = %+v", fake.requests)
	}
	if fake.requests[0].Model != "openai/gpt-4o-mini" {
		t.Fatalf("model = %q", fake.requests[0].Model)
	}
}

func TestAppRunRejectsUnknownCLIModelAliasAsUsage(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestApp(t, &scriptedProvider{})

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "--mode", "text", "--model", "fast", "hello"})
	if code != 8 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error payload = %#v", payload["error"])
	}
	if errorPayload["type"] != "invalid_cli_usage" {
		t.Fatalf("error type = %#v", errorPayload["type"])
	}
}

func TestAppModeShowTreatsBadEnvAliasAsRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TINYFLAGS_DEFAULT_MODE", "text")
	t.Setenv("TINYFLAGS_DEFAULT_MODEL", "fast")

	app := cli.NewApp(&bytes.Buffer{}, &bytes.Buffer{})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app.Stdout = stdout
	app.Stderr = stderr

	restoreCWD(t, t.TempDir())

	code := app.Execute([]string{"--format", "json", "mode", "show", "text"})
	if code != 1 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error payload = %#v", payload["error"])
	}
	if errorPayload["type"] != "runtime_error" {
		t.Fatalf("error type = %#v", errorPayload["type"])
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
	server := newModelCatalogServer(t, http.StatusOK, defaultCatalogModels(), nil)
	app, stdout, _, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{}, "base_url = \""+server.URL+"\"\n")
	app.HTTPClient = server.Client()
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
	if _, ok := payload["openrouter_model_validation"]; !ok {
		t.Fatalf("doctor payload missing openrouter_model_validation: %v", payload)
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
	server := newModelCatalogServer(t, http.StatusOK, defaultCatalogModels(), nil)
	app, stdout, _, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{}, "base_url = \""+server.URL+"\"\n")
	app.HTTPClient = server.Client()
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
	if _, ok := payload["openrouter_validation"]; !ok {
		t.Fatalf("expected openrouter_validation payload: %+v", payload)
	}
}

func TestAppConfigValidateWarnsWhenCatalogUnavailable(t *testing.T) {
	server := newModelCatalogServer(t, http.StatusInternalServerError, nil, nil)
	app, stdout, _, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{}, "base_url = \""+server.URL+"\"\n")
	app.HTTPClient = server.Client()

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "config", "validate"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q", code, stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	warnings, ok := payload["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("expected warnings payload: %+v", payload)
	}
}

func TestAppConfigValidateFailsOnUnsupportedModel(t *testing.T) {
	server := newModelCatalogServer(t, http.StatusOK, []map[string]any{
		{"id": "openai/gpt-4o-mini", "name": "GPT-4o mini", "supported_parameters": []string{"response_format"}},
		{"id": "openai/gpt-4.1", "name": "GPT-4.1", "supported_parameters": []string{"tools"}},
	}, nil)
	app, stdout, stderr, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{}, "base_url = \""+server.URL+"\"\ndefault_model = \"openai/gpt-4o-mini\"\n")
	app.HTTPClient = server.Client()

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "config", "validate"})
	if code != 1 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ok, _ := payload["ok"].(bool); ok {
		t.Fatalf("expected error payload: %+v", payload)
	}
}

func TestAppConfigValidateTreatsBadConfiguredAliasAsRuntime(t *testing.T) {
	app, stdout, stderr, cfgPath := newTestAppWithExtraConfig(t, &scriptedProvider{}, "default_model = \"fast\"\n")

	code := app.Execute([]string{"--config", cfgPath, "--format", "json", "config", "validate"})
	if code != 1 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error payload = %#v", payload["error"])
	}
	if errorPayload["type"] != "runtime_error" {
		t.Fatalf("error type = %#v", errorPayload["type"])
	}
}

func TestAppRunFailsPreflightWhenModelLacksTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var chatCalls int
	server := newModelCatalogServer(t, http.StatusOK, []map[string]any{
		{"id": "openai/gpt-4o-mini", "name": "GPT-4o mini", "supported_parameters": []string{"response_format", "structured_outputs"}},
		{"id": "openai/gpt-4.1", "name": "GPT-4.1", "supported_parameters": []string{"tools"}},
		{"id": "anthropic/claude-opus-4.5", "name": "Claude Opus 4.5", "supported_parameters": []string{"response_format", "structured_outputs"}},
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			chatCalls++
		}
	})

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := cli.NewApp(stdout, stderr)
	registry := provider.NewRegistry()
	registry.Register("openrouter", openrouter.New(server.URL, "secret", server.Client()))
	app.ProviderRegistry = registry
	app.HTTPClient = server.Client()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "tinyflags.db")
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configText := "version = 1\n" +
		"api_key = \"secret\"\n" +
		"base_url = \"" + server.URL + "\"\n" +
		"db_path = \"" + dbPath + "\"\n" +
		"skills_dir = \"" + skillsDir + "\"\n" +
		"[modes.tool]\nmodel = \"openai/gpt-4o-mini\"\n"
	if err := os.WriteFile(cfgPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--mode", "tool", "hello"})
	if code != 1 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if chatCalls != 0 {
		t.Fatalf("chat completions should not have been called, got %d", chatCalls)
	}
}

func TestAppRunPreflightUsesInjectedOpenRouterClientCatalog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var configModelCalls int
	configServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		configModelCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "anthropic/claude-opus-4.5", "name": "Claude Opus 4.5", "supported_parameters": []string{"tools"}},
			},
		})
	}))
	defer configServer.Close()

	var providerModelCalls int
	var chatCalls int
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			providerModelCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "anthropic/claude-opus-4.5", "name": "Claude Opus 4.5", "supported_parameters": []string{"structured_outputs"}},
				},
			})
		case "/chat/completions":
			chatCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "gen-123",
				"model": "anthropic/claude-opus-4.5",
				"choices": []map[string]any{{
					"message":       map[string]any{"content": "done"},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := cli.NewApp(stdout, stderr)
	registry := provider.NewRegistry()
	registry.Register("openrouter", openrouter.New(providerServer.URL, "secret", providerServer.Client()))
	app.ProviderRegistry = registry
	app.HTTPClient = configServer.Client()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "tinyflags.db")
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configText := "version = 1\n" +
		"api_key = \"secret\"\n" +
		"base_url = \"" + configServer.URL + "\"\n" +
		"db_path = \"" + dbPath + "\"\n" +
		"skills_dir = \"" + skillsDir + "\"\n" +
		"default_model = \"anthropic/claude-opus-4.5\"\n"
	if err := os.WriteFile(cfgPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--mode", "tool", "hello"})
	if code != 1 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if providerModelCalls == 0 {
		t.Fatal("expected injected provider catalog to be queried")
	}
	if configModelCalls != 0 {
		t.Fatalf("expected config base_url catalog to be ignored, got %d calls", configModelCalls)
	}
	if chatCalls != 0 {
		t.Fatalf("chat completions should not have been called, got %d", chatCalls)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("does not support required features: tools")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAppRunFailsPreflightWhenSchemaModelLacksResponseFormat(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var chatCalls int
	server := newModelCatalogServer(t, http.StatusOK, []map[string]any{
		{"id": "openai/gpt-4o-mini", "name": "GPT-4o mini", "supported_parameters": []string{"structured_outputs"}},
		{"id": "openai/gpt-4.1", "name": "GPT-4.1", "supported_parameters": []string{"tools"}},
		{"id": "anthropic/claude-opus-4.5", "name": "Claude Opus 4.5", "supported_parameters": []string{"tools"}},
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			chatCalls++
		}
		http.NotFound(w, r)
	})

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := cli.NewApp(stdout, stderr)
	registry := provider.NewRegistry()
	registry.Register("openrouter", openrouter.New(server.URL, "secret", server.Client()))
	app.ProviderRegistry = registry

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "tinyflags.db")
	skillsDir := filepath.Join(dir, "skills")
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	configText := "version = 1\n" +
		"api_key = \"secret\"\n" +
		"base_url = \"" + server.URL + "\"\n" +
		"db_path = \"" + dbPath + "\"\n" +
		"skills_dir = \"" + skillsDir + "\"\n" +
		"default_model = \"openai/gpt-4o-mini\"\n"
	if err := os.WriteFile(cfgPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--mode", "text", "--output-schema", schemaPath, "hello"})
	if code != 1 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if chatCalls != 0 {
		t.Fatalf("chat completions should not have been called, got %d", chatCalls)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("response_format")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAppRunSkipsSlowGenerationMetadataNearTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var generationCalls int
	server := newModelCatalogServer(t, http.StatusOK, defaultCatalogModels(), func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":    "gen-123",
				"model": "openai/gpt-4o-mini",
				"choices": []map[string]any{{
					"message":       map[string]any{"content": "done"},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
			})
		case "/generation":
			generationCalls++
			time.Sleep(200 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":            "gen-123",
					"model":         "openai/gpt-4o-mini",
					"finish_reason": "stop",
				},
			})
		default:
			http.NotFound(w, r)
		}
	})

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := cli.NewApp(stdout, stderr)
	registry := provider.NewRegistry()
	registry.Register("openrouter", openrouter.New(server.URL, "secret", server.Client()))
	app.ProviderRegistry = registry

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "tinyflags.db")
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configText := "version = 1\n" +
		"api_key = \"secret\"\n" +
		"base_url = \"" + server.URL + "\"\n" +
		"db_path = \"" + dbPath + "\"\n" +
		"skills_dir = \"" + skillsDir + "\"\n" +
		"default_model = \"openai/gpt-4o-mini\"\n"
	if err := os.WriteFile(cfgPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}

	code := app.Execute([]string{"--config", cfgPath, "--mode", "text", "--timeout", "50ms", "hello"})
	if code != 0 {
		t.Fatalf("exit code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "done" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if generationCalls != 0 {
		t.Fatalf("expected slow generation metadata to be skipped, got %d calls", generationCalls)
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

func restoreCWD(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func newTestApp(t *testing.T, fake provider.Provider) (*cli.App, *bytes.Buffer, *bytes.Buffer, string) {
	return newTestAppWithExtraConfig(t, fake, "")
}

func newTestAppWithExtraConfig(t *testing.T, fake provider.Provider, extraConfig string) (*cli.App, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
	defaultModel := ""
	if !strings.Contains(extraConfig, "default_model") {
		defaultModel = "default_model = \"openai/gpt-4.1\"\n"
	}
	configText := "version = 1\n" +
		defaultModel +
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

func newModelCatalogServer(t *testing.T, modelStatus int, models []map[string]any, extra func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if extra != nil && r.URL.Path != "/models" {
			extra(w, r)
			return
		}
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(modelStatus)
		if models == nil {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": models})
	}))
	t.Cleanup(server.Close)
	return server
}

func defaultCatalogModels() []map[string]any {
	return []map[string]any{
		{"id": "openai/gpt-4o-mini", "name": "GPT-4o mini", "supported_parameters": []string{"response_format", "structured_outputs"}},
		{"id": "openai/gpt-4.1", "name": "GPT-4.1", "supported_parameters": []string{"tools", "structured_outputs"}},
		{"id": "anthropic/claude-opus-4.5", "name": "Claude Opus 4.5", "supported_parameters": []string{"tools", "structured_outputs"}},
	}
}
