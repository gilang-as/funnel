package cmd

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "Funnel"

func doAutostart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry key: %w", err)
	}
	defer k.Close()

	if !enable {
		return k.DeleteValue(runValue)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	return k.SetStringValue(runValue, fmt.Sprintf(`"%s" daemon`, exe))
}
