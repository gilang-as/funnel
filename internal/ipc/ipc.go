package ipc

import (
	"os"
	"path/filepath"
	"runtime"
)

var overridePath string

// SetSocketPath overrides the auto-detected socket path.
func SetSocketPath(path string) {
	overridePath = path
}

// SocketPath returns the platform-specific IPC socket path.
func SocketPath() string {
	if overridePath != "" {
		return overridePath
	}
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\funnel`
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "funnel", "funnel.sock")
	default:
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir != "" {
			return filepath.Join(dir, "funnel.sock")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "funnel", "funnel.sock")
	}
}
