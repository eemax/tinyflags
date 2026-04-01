package core_test

import (
	"testing"

	"github.com/eemax/tinyflags/internal/core"
)

func TestFirstNonEmpty(t *testing.T) {
	if got := core.FirstNonEmpty("", "", "c"); got != "c" {
		t.Fatalf("got %q", got)
	}
	if got := core.FirstNonEmpty("a", "b"); got != "a" {
		t.Fatalf("got %q", got)
	}
	if got := core.FirstNonEmpty("", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestSupportedFormat(t *testing.T) {
	if !core.SupportedFormat("text") {
		t.Fatal("text should be supported")
	}
	if !core.SupportedFormat("json") {
		t.Fatal("json should be supported")
	}
	if core.SupportedFormat("yaml") {
		t.Fatal("yaml should not be supported")
	}
}

func TestJoinNonEmpty(t *testing.T) {
	if got := core.JoinNonEmpty("\n\n", "a", "", "b"); got != "a\n\nb" {
		t.Fatalf("got %q", got)
	}
	if got := core.JoinNonEmpty(" ", "  ", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}
