package execx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Result struct {
	Stdout string
	Stderr string
}

func Run(ctx context.Context, dir string, env []string, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommandCancellation(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	res := Result{Stdout: strings.TrimSpace(out.String()), Stderr: strings.TrimSpace(errOut.String())}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return res, ctxErr
		}
		return res, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, res.Stderr)
	}
	return res, nil
}

func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func AskPassEnv(token, username string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []string{
		"GIT_ASKPASS=" + exe,
		"GIT_TERMINAL_PROMPT=0",
		"REPOARK_ASKPASS=1",
		"REPOARK_GIT_TOKEN=" + token,
		"REPOARK_GIT_USERNAME=" + username,
	}, nil
}
