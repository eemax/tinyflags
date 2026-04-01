package errors

import "fmt"

const (
	ExitSuccess          = 0
	ExitRuntime          = 1
	ExitTimeout          = 2
	ExitRefusal          = 3
	ExitSchemaValidation = 4
	ExitToolFailure      = 5
	ExitShellFailure     = 6
	ExitSessionFailure   = 7
	ExitCLIUsage         = 8
)

type ExitCodeError struct {
	Code    int
	Message string
	Wrapped error
}

func (e *ExitCodeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Wrapped != nil {
		return e.Wrapped.Error()
	}
	return fmt.Sprintf("exit code %d", e.Code)
}

func (e *ExitCodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Wrapped
}

func (e *ExitCodeError) ExitCode() int {
	if e == nil {
		return ExitSuccess
	}
	return e.Code
}

func New(code int, message string) *ExitCodeError {
	return &ExitCodeError{Code: code, Message: message}
}

func Wrap(code int, message string, err error) *ExitCodeError {
	return &ExitCodeError{Code: code, Message: message, Wrapped: err}
}
