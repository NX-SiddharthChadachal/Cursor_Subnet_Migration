// Package logging provides the run log used by every component. The Controller
// owns one Logger and passes scoped children to the Prechecks, Executioner and
// Postchecks so that each line is attributable to a phase and, where relevant,
// to a single subnet.
package logging

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level is a log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// ParseLevel maps a level name to a Level, defaulting to LevelInfo.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Fields are the structured key/value pairs attached to a log line.
type Fields map[string]any

type sink struct {
	mu       sync.Mutex
	writers  []io.Writer
	minLevel Level
}

func (s *sink) write(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.writers {
		fmt.Fprintln(w, line)
	}
}

// Logger emits timestamped, field-annotated lines to one or more writers. It is
// safe for concurrent use, which matters because the Prechecks run their checks
// in parallel.
type Logger struct {
	sink   *sink
	fields Fields
}

// Options configures a root Logger.
type Options struct {
	// Level is the minimum level that is emitted.
	Level Level
	// Console receives human readable output; defaults to os.Stderr.
	Console io.Writer
	// FilePath, when set, receives the same lines so the run leaves an audit
	// trail on disk.
	FilePath string
}

// New builds a root Logger. The returned closer flushes and closes the log file
// when one was requested.
func New(opts Options) (*Logger, io.Closer, error) {
	console := opts.Console
	if console == nil {
		console = os.Stderr
	}
	writers := []io.Writer{console}

	var closer io.Closer = noopCloser{}
	if opts.FilePath != "" {
		f, err := os.OpenFile(opts.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file %s: %w", opts.FilePath, err)
		}
		writers = append(writers, f)
		closer = f
	}

	return &Logger{
		sink:   &sink{writers: writers, minLevel: opts.Level},
		fields: Fields{},
	}, closer, nil
}

// Discard returns a Logger that drops everything, for use in tests.
func Discard() *Logger {
	return &Logger{sink: &sink{writers: nil, minLevel: LevelError + 1}, fields: Fields{}}
}

// With returns a child Logger that adds the given fields to every line.
func (l *Logger) With(fields Fields) *Logger {
	merged := make(Fields, len(l.fields)+len(fields))
	for k, v := range l.fields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	return &Logger{sink: l.sink, fields: merged}
}

// WithPhase is shorthand for tagging a component's lines.
func (l *Logger) WithPhase(phase string) *Logger {
	return l.With(Fields{"phase": phase})
}

func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, format, args...) }

func (l *Logger) logf(level Level, format string, args ...any) {
	if level < l.sink.minLevel {
		return
	}
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	b.WriteString(" ")
	b.WriteString(fmt.Sprintf("%-5s", level.String()))
	if scope := l.scope(); scope != "" {
		b.WriteString(" ")
		b.WriteString(scope)
	}
	b.WriteString(" ")
	b.WriteString(fmt.Sprintf(format, args...))
	if extra := l.extraFields(); extra != "" {
		b.WriteString(" ")
		b.WriteString(extra)
	}
	l.sink.write(b.String())
}

// scope renders the phase and subnet fields inline because they are the two
// dimensions an operator scans for when reading the run log.
func (l *Logger) scope() string {
	var parts []string
	if phase, ok := l.fields["phase"].(string); ok && phase != "" {
		parts = append(parts, phase)
	}
	if subnet, ok := l.fields["subnet"].(string); ok && subnet != "" {
		parts = append(parts, subnet)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, "/") + "]"
}

func (l *Logger) extraFields() string {
	keys := make([]string, 0, len(l.fields))
	for k := range l.fields {
		if k == "phase" || k == "subnet" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, l.fields[k]))
	}
	return "(" + strings.Join(parts, " ") + ")"
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }
