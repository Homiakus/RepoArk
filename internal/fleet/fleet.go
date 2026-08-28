package fleet

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Homiakus/repoark/internal/backup"
	"github.com/Homiakus/repoark/internal/config"
)

type Event struct {
	Account string
	Stage   string
	Message string
}

type Result struct {
	Account   string
	Root      string
	Succeeded int
	Failed    int
	Warnings  int
	Err       error
}

func RunBackup(ctx context.Context, cfg config.Config, emit func(Event)) ([]Result, error) {
	if !cfg.Fleet.Enabled || len(cfg.Fleet.Accounts) == 0 {
		return nil, errors.New("fleet is disabled or has no accounts")
	}
	if emit == nil {
		emit = func(Event) {}
	}
	return run(ctx, cfg, emit, func(ctx context.Context, acfg config.Config, account string, emit func(Event)) Result {
		man, err := backup.New(acfg).Run(ctx, func(e backup.Event) {
			emit(Event{Account: account, Stage: e.Stage, Message: e.Message})
		})
		return Result{Account: account, Root: acfg.Backup.Root, Succeeded: man.Succeeded, Failed: man.Failed, Warnings: man.WarningCount, Err: err}
	})
}

func RunVerify(ctx context.Context, cfg config.Config, emit func(Event)) ([]Result, error) {
	if !cfg.Fleet.Enabled || len(cfg.Fleet.Accounts) == 0 {
		return nil, errors.New("fleet is disabled or has no accounts")
	}
	if emit == nil {
		emit = func(Event) {}
	}
	return run(ctx, cfg, emit, func(ctx context.Context, acfg config.Config, account string, emit func(Event)) Result {
		n, err := backup.New(acfg).Verify(ctx, func(e backup.Event) {
			emit(Event{Account: account, Stage: "verify", Message: e.Repo + " " + e.Message})
		})
		return Result{Account: account, Root: acfg.Backup.Root, Succeeded: n, Err: err}
	})
}

type runner func(context.Context, config.Config, string, func(Event)) Result

func run(ctx context.Context, cfg config.Config, emit func(Event), fn runner) ([]Result, error) {
	workers := cfg.Fleet.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(cfg.Fleet.Accounts) {
		workers = len(cfg.Fleet.Accounts)
	}
	jobs := make(chan config.FleetAccountConfig)
	results := make(chan Result, len(cfg.Fleet.Accounts))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				acfg := ResolveAccountConfig(cfg, a)
				emit(Event{Account: a.Name, Stage: "start", Message: acfg.Backup.Root})
				results <- fn(ctx, acfg, a.Name, emit)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, a := range cfg.Fleet.Accounts {
			select {
			case jobs <- a:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	var out []Result
	failed := 0
	for r := range results {
		out = append(out, r)
		if r.Err != nil {
			failed++
		}
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d fleet accounts failed", failed, len(out))
	}
	return out, nil
}

func ResolveAccountConfig(base config.Config, a config.FleetAccountConfig) config.Config {
	cfg := base
	if strings.TrimSpace(a.APIURL) != "" {
		cfg.GitHub.APIURL = a.APIURL
	}
	if strings.TrimSpace(a.GraphQLURL) != "" {
		cfg.GitHub.GraphQLURL = a.GraphQLURL
	}
	cfg.GitHub.TokenEnv = a.TokenEnv
	if a.CloneProtocol != "" {
		cfg.GitHub.CloneProtocol = a.CloneProtocol
	}
	root := strings.TrimSpace(a.BackupRoot)
	if root == "" {
		root = filepath.Join(base.Backup.Root, "fleet", safe(a.Name))
	}
	cfg.Backup.Root = root
	// Fleet cycles back up GitHub sources only. Shared GitLab/offsite actions run
	// once at the orchestration layer, never once per account.
	cfg.GitLab.Enabled = false
	cfg.Offsite.Enabled = false
	return cfg
}

func safe(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "account"
	}
	return b.String()
}
