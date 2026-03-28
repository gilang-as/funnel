package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const plistLabel = "com.gilang.funnel"

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Exe}}</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
	<key>StandardOutPath</key>
	<string>{{.LogFile}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogFile}}</string>
</dict>
</plist>
`))

func doAutostart(enable bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	plistPath := filepath.Join(agentsDir, plistLabel+".plist")

	if !enable {
		exec.Command("launchctl", "unload", plistPath).Run() //nolint
		return os.Remove(plistPath)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return err
	}
	logsDir := filepath.Join(home, "Library", "Logs")
	os.MkdirAll(logsDir, 0755) //nolint

	f, err := os.Create(plistPath)
	if err != nil {
		return err
	}
	defer f.Close()

	data := struct {
		Label   string
		Exe     string
		LogFile string
	}{
		Label:   plistLabel,
		Exe:     exe,
		LogFile: filepath.Join(logsDir, "funnel.log"),
	}
	if err := plistTmpl.Execute(f, data); err != nil {
		return err
	}

	return exec.Command("launchctl", "load", plistPath).Run()
}
