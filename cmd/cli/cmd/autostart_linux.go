package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

var unitTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=Funnel torrent-to-S3 daemon
After=network.target

[Service]
ExecStart={{.Exe}} daemon
Restart=on-failure

[Install]
WantedBy=default.target
`))

func doAutostart(enable bool) error {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	unitDir := filepath.Join(cfgDir, "systemd", "user")
	unitPath := filepath.Join(unitDir, "funnel.service")

	if !enable {
		exec.Command("systemctl", "--user", "disable", "--now", "funnel").Run() //nolint
		return os.Remove(unitPath)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	if err := os.MkdirAll(unitDir, 0755); err != nil {
		return err
	}

	f, err := os.Create(unitPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := unitTmpl.Execute(f, struct{ Exe string }{Exe: exe}); err != nil {
		return err
	}

	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	return exec.Command("systemctl", "--user", "enable", "--now", "funnel").Run()
}
