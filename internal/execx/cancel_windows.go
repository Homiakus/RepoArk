//go:build windows

package execx

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const createNewProcessGroup = 0x00000200

func configureCommandCancellation(cmd *exec.Cmd) {
	// A separate Windows process group plus taskkill /T lets cancellation reach
	// descendants spawned by git/docker/restic wrappers instead of terminating
	// only the immediate shell process.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup, HideWindow: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		killer := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		killer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := killer.Run(); err == nil {
			return nil
		}
		// taskkill can race with a naturally exiting process. Fall back to the
		// direct process handle so CommandContext can still complete promptly.
		if err := cmd.Process.Kill(); err == nil || errors.Is(err, os.ErrProcessDone) {
			return nil
		} else {
			return err
		}
	}
}
