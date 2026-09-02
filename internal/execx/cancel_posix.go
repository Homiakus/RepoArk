//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package execx

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureCommandCancellation(cmd *exec.Cmd) {
	// Put every external command in its own process group. Commands such as
	// restic, git, docker and shell wrappers can spawn descendants; killing only
	// the immediate process leaves those descendants alive and can keep os/exec
	// stdout/stderr pipes open indefinitely.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
