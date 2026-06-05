//go:build darwin

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistContent(t *testing.T) {
	content := plistContent("/usr/local/bin/model-shelf")

	if !strings.Contains(content, "<string>com.model-shelf.daemon</string>") {
		t.Error("plist should contain the service label")
	}
	if !strings.Contains(content, "<string>/usr/local/bin/model-shelf</string>") {
		t.Error("plist should contain the executable path")
	}
	if !strings.Contains(content, "<string>daemon</string>") {
		t.Error("plist should contain 'daemon' argument")
	}
	if !strings.Contains(content, "<key>RunAtLoad</key>") {
		t.Error("plist should have RunAtLoad key")
	}
	if !strings.Contains(content, "<true/>") {
		t.Error("plist should set RunAtLoad to true")
	}
	if !strings.Contains(content, "<key>KeepAlive</key>") {
		t.Error("plist should have KeepAlive key")
	}
}

func TestPlistDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := plistDir()
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, "Library", "LaunchAgents")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestPlistPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := plistPath()
	if !strings.HasSuffix(path, "com.model-shelf.daemon.plist") {
		t.Errorf("plist path should end with com.model-shelf.daemon.plist, got %q", path)
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{Status{Installed: false}, "not installed"},
		{Status{Installed: true, Running: false}, "stopped"},
		{Status{Installed: true, Running: true}, "running"},
	}
	for _, tt := range tests {
		got := tt.status.String()
		if got != tt.want {
			t.Errorf("Status%+v.String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}
