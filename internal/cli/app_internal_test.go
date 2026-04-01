package cli

import (
	"errors"
	"fmt"
	"testing"

	cerr "github.com/eemax/tinyflags/internal/errors"
)

func TestCLIErrorDetailsPreservesJoinedMessages(t *testing.T) {
	err := errors.Join(
		cerr.Wrap(cerr.ExitToolFailure, "tool retry budget exhausted", fmt.Errorf("disk full")),
		fmt.Errorf("finish run: database is locked"),
	)

	code, message := cliErrorDetails(err)
	if code != cerr.ExitToolFailure {
		t.Fatalf("code = %d, want %d", code, cerr.ExitToolFailure)
	}
	want := "tool retry budget exhausted: disk full\nfinish run: database is locked"
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}
