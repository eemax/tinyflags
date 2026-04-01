package logging_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eemax/tinyflags/internal/logging"
)

func TestStdLogger_LevelFiltering(t *testing.T) {
	tests := []struct {
		level     string
		wantError bool
		wantInfo  bool
		wantDebug bool
	}{
		{"error", true, false, false},
		{"info", true, true, false},
		{"debug", true, true, true},
		{"", true, true, false}, // empty defaults to info
	}
	for _, tt := range tests {
		t.Run("level="+tt.level, func(t *testing.T) {
			var buf bytes.Buffer
			logger := logging.New(&buf, tt.level)

			buf.Reset()
			logger.Errorf("e")
			if got := buf.Len() > 0; got != tt.wantError {
				t.Fatalf("Errorf wrote=%v, want=%v", got, tt.wantError)
			}

			buf.Reset()
			logger.Infof("i")
			if got := buf.Len() > 0; got != tt.wantInfo {
				t.Fatalf("Infof wrote=%v, want=%v", got, tt.wantInfo)
			}

			buf.Reset()
			logger.Debugf("d")
			if got := buf.Len() > 0; got != tt.wantDebug {
				t.Fatalf("Debugf wrote=%v, want=%v", got, tt.wantDebug)
			}
		})
	}
}

func TestStdLogger_NilWriter(t *testing.T) {
	logger := logging.New(nil, "debug")
	logger.Errorf("should not panic")
	logger.Infof("should not panic")
	logger.Debugf("should not panic")
}

func TestStdLogger_OutputFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, "debug")

	buf.Reset()
	logger.Errorf("oops %d", 42)
	if got := buf.String(); !strings.Contains(got, "[error]") || !strings.Contains(got, "oops 42") {
		t.Fatalf("Errorf output = %q, want [error] prefix and formatted message", got)
	}

	buf.Reset()
	logger.Infof("hello %s", "world")
	if got := buf.String(); !strings.Contains(got, "[tinyflags]") || !strings.Contains(got, "hello world") {
		t.Fatalf("Infof output = %q, want [tinyflags] prefix and formatted message", got)
	}

	buf.Reset()
	logger.Debugf("detail")
	if got := buf.String(); !strings.Contains(got, "[tinyflags]") || !strings.Contains(got, "detail") {
		t.Fatalf("Debugf output = %q, want [tinyflags] prefix and message", got)
	}
}
