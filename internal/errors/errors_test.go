package errors_test

import (
	"fmt"
	"testing"

	cerr "github.com/eemax/tinyflags/internal/errors"
)

func TestExitCodeError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *cerr.ExitCodeError
		want string
	}{
		{"message set", &cerr.ExitCodeError{Code: 1, Message: "broken"}, "broken"},
		{"wrapped only", &cerr.ExitCodeError{Code: 2, Wrapped: fmt.Errorf("inner")}, "inner"},
		{"fallback format", &cerr.ExitCodeError{Code: 3}, "exit code 3"},
		{"nil receiver", (*cerr.ExitCodeError)(nil), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExitCodeError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("root cause")
	err := &cerr.ExitCodeError{Code: 1, Wrapped: inner}
	if err.Unwrap() != inner {
		t.Fatal("Unwrap() did not return wrapped error")
	}

	noWrap := &cerr.ExitCodeError{Code: 1}
	if noWrap.Unwrap() != nil {
		t.Fatal("Unwrap() should return nil when no wrapped error")
	}

	var nilErr *cerr.ExitCodeError
	if nilErr.Unwrap() != nil {
		t.Fatal("Unwrap() on nil receiver should return nil")
	}
}

func TestExitCodeError_ExitCode(t *testing.T) {
	err := &cerr.ExitCodeError{Code: 5}
	if err.ExitCode() != 5 {
		t.Fatalf("ExitCode() = %d, want 5", err.ExitCode())
	}

	var nilErr *cerr.ExitCodeError
	if nilErr.ExitCode() != cerr.ExitSuccess {
		t.Fatalf("ExitCode() on nil = %d, want %d", nilErr.ExitCode(), cerr.ExitSuccess)
	}
}

func TestNew(t *testing.T) {
	err := cerr.New(cerr.ExitTimeout, "timed out")
	if err.Code != cerr.ExitTimeout {
		t.Fatalf("Code = %d, want %d", err.Code, cerr.ExitTimeout)
	}
	if err.Message != "timed out" {
		t.Fatalf("Message = %q, want %q", err.Message, "timed out")
	}
	if err.Wrapped != nil {
		t.Fatal("Wrapped should be nil")
	}
}

func TestWrap(t *testing.T) {
	inner := fmt.Errorf("disk full")
	err := cerr.Wrap(cerr.ExitRuntime, "write failed", inner)
	if err.Code != cerr.ExitRuntime {
		t.Fatalf("Code = %d, want %d", err.Code, cerr.ExitRuntime)
	}
	if err.Message != "write failed" {
		t.Fatalf("Message = %q, want %q", err.Message, "write failed")
	}
	if err.Wrapped != inner {
		t.Fatal("Wrapped should be the inner error")
	}
}
