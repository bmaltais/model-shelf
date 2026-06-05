//go:build linux

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitContent(t *testing.T) {
	content := unitContent("/usr/local/bin/model-shelf")

	if !strings.Contains(content, "ExecStart=/usr/local/bin/model-shelf daemon") {
		t.Error("unit file should contain ExecStart with daemon command")
	}
	if !strings.Contains(content, "[Unit]") {
		t.Error("unit file should contain [Unit] section")
	}
	if !strings.Contains(content, "[Service]") {
		t.Error("unit file should contain [Service] section")
	}
	if !strings.Contains(content, "[Install]") {
		t.Error("unit file should contain [Install] section")
	}
	if !strings.Contains(content, "WantedBy=default.target") {
		t.Error("unit file should want default.target for user service")
	}
	if !strings.Contains(content, "Restart=on-failure") {
		t.Error("unit file should restart on failure")
	}
}

func TestUnitDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	dir := unitDir()
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".config", "systemd", "user")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestUnitDirXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := unitDir()
	expected := filepath.Join(tmp, "systemd", "user")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestUnitPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	path := unitPath()
	if !strings.HasSuffix(path, "model-shelf.service") {
		t.Errorf("unit path should end with model-shelf.service, got %q", path)
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
