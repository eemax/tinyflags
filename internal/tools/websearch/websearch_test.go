package websearch_test

import (
	"context"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/tools"
	"github.com/eemax/tinyflags/internal/tools/websearch"
)

func TestStubReturnsUnavailable(t *testing.T) {
	stub := websearch.NewStub()
	if stub.Name() != "web_search" {
		t.Fatalf("Name() = %q", stub.Name())
	}

	result, err := stub.Execute(context.Background(), core.ToolCallRequest{
		ID:   "1",
		Name: "web_search",
	}, tools.ExecContext{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != "unavailable" {
		t.Fatalf("status = %q", result.Status)
	}
}
