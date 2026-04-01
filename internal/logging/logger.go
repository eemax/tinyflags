package logging

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

type Logger interface {
	Errorf(format string, args ...any)
	Infof(format string, args ...any)
	Debugf(format string, args ...any)
}

type StdLogger struct {
	out   io.Writer
	level string
	mu    sync.Mutex
}

func New(out io.Writer, level string) *StdLogger {
	if strings.TrimSpace(level) == "" {
		level = "info"
	}
	return &StdLogger{out: out, level: strings.ToLower(level)}
}

func (l *StdLogger) Errorf(format string, args ...any) {
	l.write("error", format, args...)
}

func (l *StdLogger) Infof(format string, args ...any) {
	if l.level == "info" || l.level == "debug" {
		l.write("tinyflags", format, args...)
	}
}

func (l *StdLogger) Debugf(format string, args ...any) {
	if l.level == "debug" {
		l.write("tinyflags", format, args...)
	}
}

func (l *StdLogger) write(prefix, format string, args ...any) {
	if l.out == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.out, "[%s] %s\n", prefix, fmt.Sprintf(format, args...))
}
