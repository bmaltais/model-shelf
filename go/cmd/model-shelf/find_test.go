package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestCmdFind_EmptyQuery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdFind([]string{""})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	output := string(out)
	if !strings.Contains(output, "query cannot be empty") {
		t.Errorf("expected 'query cannot be empty' error, got:\n%s", output)
	}
}

func TestCmdFind_WhitespaceQuery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdFind([]string{"   "})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	output := string(out)
	if !strings.Contains(output, "query cannot be empty") {
		t.Errorf("expected 'query cannot be empty' error, got:\n%s", output)
	}
}

func TestCmdFind_NoArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Capture stderr.
	oldErr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := cmdFind([]string{})

	w.Close()
	os.Stderr = oldErr
	out, _ := io.ReadAll(r)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	output := string(out)
	if !strings.Contains(output, "usage:") {
		t.Errorf("expected usage message, got:\n%s", output)
	}
}

func TestCmdFind_Help_Output(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdFind([]string{"--help"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if len(out) == 0 {
		t.Fatal("expected help output")
	}
}
