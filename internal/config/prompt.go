package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrInputRequired is returned when a value is missing and the run cannot ask
// for it, either because it is non-interactive or because stdin is not a
// terminal.
var ErrInputRequired = errors.New("required input missing and prompting is disabled")

type prompter struct {
	reader         *bufio.Reader
	out            io.Writer
	nonInteractive bool
}

func newPrompter(in io.Reader, out io.Writer, nonInteractive bool) *prompter {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	return &prompter{reader: bufio.NewReader(in), out: out, nonInteractive: nonInteractive}
}

// text asks for a value, returning fallback when the operator just presses enter.
func (p *prompter) text(label, fallback string) (string, error) {
	if p.nonInteractive {
		if fallback != "" {
			return fallback, nil
		}
		return "", fmt.Errorf("%w: %s", ErrInputRequired, label)
	}
	if fallback != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", label, fallback)
	} else {
		fmt.Fprintf(p.out, "%s: ", label)
	}
	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

// secret asks for a password. When stdin is a terminal the input is not echoed;
// otherwise it falls back to a plain read so that piped input still works.
func (p *prompter) secret(label string) (string, error) {
	if p.nonInteractive {
		return "", fmt.Errorf("%w: %s", ErrInputRequired, label)
	}
	fmt.Fprintf(p.out, "%s: ", label)
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(p.out)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", label, err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	return strings.TrimSpace(line), nil
}

// Confirm asks a yes/no question on the terminal. It is exported because the
// Controller uses it before starting the migration.
func Confirm(question string, in io.Reader, out io.Writer) (bool, error) {
	p := newPrompter(in, out, false)
	answer, err := p.text(question+" [y/N]", "n")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
