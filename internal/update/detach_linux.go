package update

import (
	"os/exec"
	"syscall"
)

// own session so procd restarting the service doesnt take the script with it
func detach(script string) error {
	cmd := exec.Command("/bin/sh", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
