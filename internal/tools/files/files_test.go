package files_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/tools"
	filetools "github.com/eemax/tinyflags/internal/tools/files"
)

func TestReaderReadsRelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	reader := filetools.NewReader()
	result, err := reader.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"note.txt"}`),
	}, tools.ExecContext{CWD: dir})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestWriterCreatesParentDirsAndWrites(t *testing.T) {
	dir := t.TempDir()
	writer := filetools.NewWriter()

	_, err := writer.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"nested/file.txt","content":"hello"}`),
	}, tools.ExecContext{CWD: dir})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("file content = %q", string(data))
	}
}

func TestReaderAndWriterSkipInPlanMode(t *testing.T) {
	reader := filetools.NewReader()
	readResult, err := reader.Execute(context.Background(), core.ToolCallRequest{ID: "1"}, tools.ExecContext{PlanOnly: true})
	if err != nil {
		t.Fatalf("reader plan mode error: %v", err)
	}
	if readResult.Status != "skipped_plan_mode" {
		t.Fatalf("reader status = %q", readResult.Status)
	}

	writer := filetools.NewWriter()
	writeResult, err := writer.Execute(context.Background(), core.ToolCallRequest{ID: "2"}, tools.ExecContext{PlanOnly: true})
	if err != nil {
		t.Fatalf("writer plan mode error: %v", err)
	}
	if writeResult.Status != "skipped_plan_mode" {
		t.Fatalf("writer status = %q", writeResult.Status)
	}
}

func TestReaderRejectsInvalidArguments(t *testing.T) {
	reader := filetools.NewReader()
	_, err := reader.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Arguments: json.RawMessage(`{"path":`),
	}, tools.ExecContext{CWD: t.TempDir()})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitToolFailure {
		t.Fatalf("error = %#v", err)
	}
}
