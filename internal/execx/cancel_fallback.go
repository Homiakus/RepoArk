//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package execx

import (
	"os/exec"
	"time"
)

func configureCommandCancellation(cmd *exec.Cmd) {
	// Preserve os/exec's native CommandContext cancellation on less common
	// platforms while still bounding waits on inherited stdout/stderr handles.
	cmd.WaitDelay = 2 * time.Second
}
