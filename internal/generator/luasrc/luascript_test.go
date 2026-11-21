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

func TestRunRedirect(t *testing.T) {
	err := RunWithOutput(context.Background(), noopExecer{}, Scope{ID: 7}, `redirect("/admin/customer/42")`, nil)
	if err == nil {
		t.Fatal("expected RedirectError, got nil")
	}
	re, ok := err.(*RedirectError)
	if !ok {
		t.Fatalf("expected *RedirectError, got %T: %v", err, err)
	}
	if re.URL != "/admin/customer/42" {
		t.Errorf("redirect URL = %q, want %q", re.URL, "/admin/customer/42")
	}
	if !IsRedirect(err) {
		t.Error("IsRedirect(err) should be true")
	}
	if IsAbort(err) {
		t.Error("IsAbort(err) should be false for redirect")
	}
}

func TestRunRedirectTo(t *testing.T) {
	err := RunWithOutput(context.Background(), noopExecer{}, Scope{ID: 7}, `redirect_to("/admin/customer/" .. tostring(ctx.id), { flash = "created", order_id = "99" })`, nil)
	if err == nil {
		t.Fatal("expected RedirectError, got nil")
	}
	re, ok := err.(*RedirectError)
	if !ok {
		t.Fatalf("expected *RedirectError, got %T: %v", err, err)
	}
	if re.URL != "/admin/customer/7?flash=created&order_id=99" {
		t.Errorf("redirect URL = %q, want %q", re.URL, "/admin/customer/7?flash=created&order_id=99")
	}
}

func TestRunRedirectNonPath(t *testing.T) {
	err := RunWithOutput(context.Background(), noopExecer{}, Scope{}, `redirect("http://evil.example.com")`, nil)
	if err == nil {
		t.Fatal("expected RedirectError, got nil")
	}
	re, ok := err.(*RedirectError)
	if !ok {
		t.Fatalf("expected *RedirectError, got %T: %v", err, err)
	}
	if re.URL != "http://evil.example.com" {
		t.Errorf("redirect URL = %q, want %q", re.URL, "http://evil.example.com")
	}
}

func TestRunNoRedirect(t *testing.T) {
	err := RunWithOutput(context.Background(), noopExecer{}, Scope{}, `local x = 1`, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if IsRedirect(err) {
		t.Error("IsRedirect(err) should be false when no redirect")
	}
}