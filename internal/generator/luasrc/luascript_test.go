package luascript

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

// noopExecer satisfies Execer without touching a database; scripts in these
// tests never issue db calls.
type noopExecer struct{}

func (noopExecer) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return nil, nil
}
func (noopExecer) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return nil, nil
}
func (noopExecer) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return nil
}

func TestRunWithOutputCapturesPrint(t *testing.T) {
	var sb strings.Builder
	err := RunWithOutput(context.Background(), noopExecer{}, Scope{ID: 1}, `print("hello", 42)`, &sb)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := sb.String(), "hello\t42\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRunWithOutputMultiLine(t *testing.T) {
	var sb strings.Builder
	err := RunWithOutput(context.Background(), noopExecer{}, Scope{}, "print(1)\nprint('two lines')\nprint(true, nil)", &sb)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := sb.String(), "1\ntwo lines\ntrue\tnil\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRunPrintsToStdout(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "lua-print-*")
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()
	old := os.Stdout
	os.Stdout = tmp
	runErr := Run(context.Background(), noopExecer{}, Scope{}, `print("hi")`)
	os.Stdout = old
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "hi\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}