package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Homiakus/repoark/internal/app"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/observability"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	args := os.Args[1:]
	cfgPath, commandArgs := splitConfigArg(args)
	if isWebCommand(commandArgs) {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fatal(err)
		}
		if len(commandArgs) > 0 && commandArgs[0] == "tui" {
			fmt.Fprintln(os.Stderr, "repoark: 'tui' is deprecated; starting the web console")
		}
		fmt.Printf("RepoArk web console: http://%s/\n", cfg.Observability.Listen)
		if err := observability.New(cfg).RunConsole(ctx); err != nil {
			fatal(err)
		}
		return
	}

	if isHelpCommand(commandArgs) {
		if err := app.Run(ctx, args); err != nil {
			fatal(err)
		}
		fmt.Println("\nInteractive UI:\n  repoark web                    Start the browser console (also the default)\n  repoark tui                    Deprecated alias for the browser console")
		return
	}

	if err := app.Run(ctx, args); err != nil {
		fatal(err)
	}
}

func splitConfigArg(args []string) (string, []string) {
	path := strings.TrimSpace(os.Getenv("REPOARK_CONFIG"))
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			path = args[i+1]
			i++
			continue
		}
		out = append(out, args[i])
	}
	return path, out
}

func isWebCommand(args []string) bool {
	if len(args) == 0 {
		return true
	}
	return args[0] == "web" || args[0] == "tui"
}

func isHelpCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "help", "--help", "-h":
		return true
	default:
		return false
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "repoark:", err)
	os.Exit(1)
}
