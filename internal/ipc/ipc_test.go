package ipc

import (
	"runtime"
	"strings"
	"testing"
)

func TestSocketPath_NotEmpty(t *testing.T) {
	overridePath = ""
	p := SocketPath()
	if p == "" {
		t.Fatal("SocketPath returned empty string")
	}
}

func TestSocketPath_ContainsFunnel(t *testing.T) {
	overridePath = ""
	p := SocketPath()
	if !strings.Contains(strings.ToLower(p), "funnel") {
		t.Fatalf("expected 'funnel' in socket path, got %q", p)
	}
}

func TestSocketPath_Override(t *testing.T) {
	const custom = "/tmp/test.sock"
	SetSocketPath(custom)
	t.Cleanup(func() { overridePath = "" })

	if got := SocketPath(); got != custom {
		t.Fatalf("want %q, got %q", custom, got)
	}
}

func TestSocketPath_PlatformSpecific(t *testing.T) {
	overridePath = ""
	p := SocketPath()
	switch runtime.GOOS {
	case "windows":
		if !strings.HasPrefix(p, `\\.\pipe\`) {
			t.Fatalf("windows path should start with named pipe prefix, got %q", p)
		}
	case "darwin":
		if !strings.Contains(p, "Library/Application Support") {
			t.Fatalf("darwin path should contain Application Support, got %q", p)
		}
	default:
		// Linux: XDG_RUNTIME_DIR or ~/.local/share
		if !strings.HasSuffix(p, ".sock") {
			t.Fatalf("unix path should end with .sock, got %q", p)
		}
	}
}
