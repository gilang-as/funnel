//go:build !darwin && !linux && !windows

package cmd

import "fmt"

func doAutostart(enable bool) error {
	return fmt.Errorf("autostart is not supported on this platform")
}
