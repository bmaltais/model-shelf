package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestCmdResolve_Help(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdResolve([]string{"--help"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(string(out), "Resolve a model to a local path") {
		t.Fatalf("expected help text, got: %s", out)
	}
}

func TestCmdResolve_HelpShortFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdResolve([]string{"-h"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(string(out), "Resolve a model to a local path") {
		t.Fatalf("expected help text, got: %s", out)
	}
}

func TestCmdFind_Help(t *testing.T) {
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
	if !strings.Contains(string(out), "Search Hugging Face for models") {
		t.Fatalf("expected help text, got: %s", out)
	}
}

func TestCmdInit_Help(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdInit([]string{"--help"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(string(out), "Initialize a mesh node") {
		t.Fatalf("expected help text, got: %s", out)
	}
}

func TestCmdService_Help(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	code := cmdService([]string{"--help"})

	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	output := string(out)
	if !strings.Contains(output, "restart") {
		t.Errorf("expected 'restart' in service help, got:\n%s", output)
	}
	if !strings.Contains(output, "stop + start") {
		t.Errorf("expected 'stop + start' description in service help, got:\n%s", output)
	}
}

