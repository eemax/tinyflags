package schema_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/eemax/tinyflags/internal/schema"
)

func TestLoadReadsSchemaBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.json")
	want := []byte(`{"type":"object"}`)
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := schema.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("schema = %q, want %q", string(got), string(want))
	}
}

func TestValidateAcceptsValidJSON(t *testing.T) {
	got, err := schema.Validate(context.Background(), []byte(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`), `{"name":"tinyflags"}`)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if string(got) != `{"name":"tinyflags"}` {
		t.Fatalf("validated JSON = %q", string(got))
	}
}

func TestValidateRejectsInvalidJSON(t *testing.T) {
	_, err := schema.Validate(context.Background(), []byte(`{"type":"object"}`), `not-json`)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestValidateRejectsSchemaMismatch(t *testing.T) {
	_, err := schema.Validate(context.Background(), []byte(`{"type":"object","required":["name"]}`), `{"missing":true}`)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
