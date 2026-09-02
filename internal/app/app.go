package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/audit"
	"github.com/Homiakus/repoark/internal/backup"
	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/controlplane"
	"github.com/Homiakus/repoark/internal/erasure"
	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/fleet"
	"github.com/Homiakus/repoark/internal/generation"
	"github.com/Homiakus/repoark/internal/githubapi"
	"github.com/Homiakus/repoark/internal/gitlab"
	"github.com/Homiakus/repoark/internal/kmsattest"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/notify"
	"github.com/Homiakus/repoark/internal/observability"
	"github.com/Homiakus/repoark/internal/offsite"
	"github.com/Homiakus/repoark/internal/policy"
	"github.com/Homiakus/repoark/internal/scrub"
	"github.com/Homiakus/repoark/internal/signing"
	"github.com/Homiakus/repoark/internal/storagehealth"
	"github.com/Homiakus/repoark/internal/tiering"
)

const Version = "0.8.0"

func Run(ctx context.Context, args []string) error {
	if os.Getenv("REPOARK_ASKPASS") == "1" {
		return askPass(args)
	}
	cfgPath, args := extractConfig(args)
	if len(args) == 0 {
		return errors.New("no CLI command; start the interactive web console through the repoark entrypoint")
	}

	cmd := args[0]
	if cmd == "version" || cmd == "--version" || cmd == "-v" {
		fmt.Println("RepoArk", Version)
		return nil
	}
	if cmd == "help" || cmd == "--help" || cmd == "-h" {
		printHelp()
		return nil
	}
	if cmd == "init" {
		return initConfig(cfgPath, contains(args[1:], "--force"))
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	mgr := backup.New(cfg)

	switch cmd {
	case "backup":
		man, err := mgr.Run(ctx, printBackupEvent)
		fmt.Printf("\nResult: %d succeeded, %d failed, %d warnings\n", man.Succeeded, man.Failed, man.WarningCount)
		msg := fmt.Sprintf("RepoArk backup: %d succeeded, %d failed, %d warnings", man.Succeeded, man.Failed, man.WarningCount)
		if nerr := notify.Send(ctx, cfg, msg, err == nil); nerr != nil {
			fmt.Fprintln(os.Stderr, "notification warning:", nerr)
		}
		return auditResult(cfg, "backup", man.GitHubUser, err, map[string]any{"succeeded": man.Succeeded, "failed": man.Failed, "warnings": man.WarningCount})
	case "verify":
		n, err := mgr.Verify(ctx, printBackupEvent)
		if err == nil {
			fmt.Printf("Verified %d repositories\n", n)
		}
		return auditResult(cfg, "verify", cfg.Backup.Root, err, map[string]any{"repositories": n})
	case "restore":
		if len(args) < 2 {
			return errors.New("usage: repoark restore OWNER/REPO [TARGET] [--generation ID]")
		}
		genID := valueAfter(args[2:], "--generation")
		target := ""
		for _, a := range args[2:] {
			if a != "--generation" && a != genID {
				target = a
				break
			}
		}
		if genID != "" {
			if cfg.ControlPlane.RestoreAuth.Enabled {
				return errors.New("approval-gated restore is enabled; use: repoark control restore-request ...")
			}
			if !cfg.ControlPlane.Generations.Enabled {
				return errors.New("control_plane.generations is disabled")
			}
			if cfg.Security.SignManifests {
				return generation.RestoreVerified(ctx, cfg.ControlPlane.Generations.Root, args[1], genID, target, cfg.Security.SigningKeyPath+".pub")
			}
			return generation.Restore(ctx, cfg.ControlPlane.Generations.Root, args[1], genID, target)
		}
		return mgr.Restore(ctx, args[1], target)
	case "drill":
		sample := 0
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 0 {
				return errors.New("usage: repoark drill [SAMPLE_SIZE]")
			}
			sample = n
		}
		report, err := mgr.Drill(ctx, sample, printBackupEvent)
		fmt.Printf("Recovery drill: %d succeeded, %d failed\n", report.Succeeded, report.Failed)
		if err != nil {
			fmt.Println("Drill artifacts:", report.WorkDir)
		}
		return auditResult(cfg, "restore-drill", cfg.Backup.Root, err, map[string]any{"succeeded": report.Succeeded, "failed": report.Failed})
	case "serve":
		if !cfg.Observability.Enabled {
			return errors.New("observability is disabled in config")
		}
		fmt.Println("RepoArk health/metrics listening on", cfg.Observability.Listen)
		return observability.New(cfg).Run(ctx)
	case "keys":
		return runKeys(cfg, args[1:])
	case "doctor":
		return doctor(ctx, cfg)
	case "daemon":
		return daemon(ctx, cfg)
	case "fleet":
		return runFleet(ctx, cfg, args[1:])
	case "audit":
		return runAudit(cfg, args[1:])
	case "cas":
		return runCAS(cfg, args[1:])
	case "storage":
		return runStorage(ctx, cfg, args[1:])
	case "policy":
		return runPolicy(ctx, cfg, args[1:])
	case "offsite":
		if len(args) > 1 && args[1] == "verify-lock" {
			err := offsite.VerifyObjectLock(ctx, cfg)
			if err == nil {
				fmt.Println("S3 Object Lock, retention policy and versioning satisfy configuration")
			}
			return auditResult(cfg, "object-lock-verify", cfg.Offsite.ObjectLock.Bucket, err, nil)
		}
		if len(args) > 1 && args[1] == "configure-lock" {
			err := offsite.ConfigureObjectLockDefaultRetention(ctx, cfg, contains(args[2:], "--allow-compliance"))
			if err == nil {
				fmt.Println("S3 Object Lock default retention configured and verified")
			}
			return auditResult(cfg, "object-lock-configure", cfg.Offsite.ObjectLock.Bucket, err, map[string]any{"mode": cfg.Offsite.ObjectLock.ExpectedMode, "days": cfg.Offsite.ObjectLock.MinRetentionDays})
		}
		err := offsite.Sync(ctx, cfg)
		return auditResult(cfg, "offsite-sync", cfg.Backup.Root, err, nil)
	case "gitlab":
		return runGitLab(ctx, cfg, args[1:])
	case "github":
		return runGitHub(ctx, cfg, args[1:])
	case "control":
		return runControl(ctx, cfg, args[1:])
	case "generations":
		return runGenerations(ctx, cfg, args[1:])
	case "agent":
		return runAgent(ctx, cfg, args[1:])
	case "agents":
		return runAgents(ctx, cfg, args[1:])
	default:
		return fmt.Errorf("unknown command %q; run repoark help", cmd)
	}
}

func extractConfig(args []string) (string, []string) {
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

func initConfig(path string, force bool) error {
	if path == "" {
		path = config.DefaultPath()
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists: %s (use --force to overwrite)", path)
		}
	}
	cfg := config.Default()
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Println("Created", path)
	fmt.Println("Set GITHUB_TOKEN (or authenticate with gh), then run: repoark doctor")
	return nil
}

func doctor(ctx context.Context, cfg config.Config) error {
	type check struct {
		name   string
		ok     bool
		detail string
	}
	checks := []check{
		{"git", execx.Exists("git"), "required"},
		{"git-lfs", execx.Exists("git-lfs"), "recommended when repositories use LFS"},
		{"gh", execx.Exists("gh"), "optional token source"},
		{"docker", execx.Exists("docker"), "required only for local GitLab deployment"},
		{"ssh", execx.Exists("ssh"), "required for SSH cloning/remote GitLab"},
		{"scp", execx.Exists("scp"), "required for remote GitLab deployment"},
		{"restic", execx.Exists("restic"), "offsite backend"},
		{"rclone", execx.Exists("rclone"), "S3/MinIO/offsite backend"},
		{"skopeo", execx.Exists("skopeo"), "optional OCI export"},
		{"aws", execx.Exists("aws"), "optional S3 Object Lock replication"},
		{"npm", execx.Exists("npm"), "optional npm package payload restore/archive tooling"},
		{"dotnet", execx.Exists("dotnet"), "optional NuGet restore tooling"},
		{"mvn", execx.Exists("mvn"), "optional Maven package payload archive"},
		{"gem", execx.Exists("gem"), "optional RubyGems package payload archive"},
	}
	failed := false
	for _, c := range checks {
		mark := "OK"
		if !c.ok {
			mark = "--"
			if c.detail == "required" {
				failed = true
			}
		}
		fmt.Printf("%-10s %-3s %s\n", c.name, mark, c.detail)
	}
	token := githubapi.ResolveToken(cfg.GitHub.TokenEnv)
	if token == "" {
		fmt.Printf("%-10s --  set %s/GH_TOKEN/GITHUB_TOKEN or use gh auth login\n", "GitHub", cfg.GitHub.TokenEnv)
		failed = true
	} else {
		client := githubapi.New(cfg.GitHub.APIURL, token)
		u, err := client.User(ctx)
		if err != nil {
			fmt.Printf("%-10s ERR %v\n", "GitHub", err)
			failed = true
		} else {
			fmt.Printf("%-10s OK  authenticated as %s\n", "GitHub", u.Login)
		}
	}
	fmt.Printf("%-10s %s\n", "backup", cfg.Backup.Root)
	if cfg.Security.SignManifests {
		if _, _, err := signing.EnsureKey(cfg.Security.SigningKeyPath); err != nil {
			fmt.Printf("%-10s ERR %v\n", "signing", err)
			failed = true
		} else {
			fmt.Printf("%-10s OK  Ed25519 key: %s\n", "signing", cfg.Security.SigningKeyPath)
		}
	}
	if cfg.Security.KMSAttestation.Enabled {
		if !execx.Exists("aws") {
			fmt.Printf("%-10s ERR aws CLI required for KMS attestation\n", "KMS")
			failed = true
		} else {
			fmt.Printf("%-10s OK  key=%s alg=%s\n", "KMS", cfg.Security.KMSAttestation.KeyID, cfg.Security.KMSAttestation.SigningAlgorithm)
		}
	}
	if cfg.Offsite.Enabled {
		fmt.Printf("%-10s %s\n", "offsite", cfg.Offsite.Backend)
	}
	if cfg.CAS.Enabled {
		fmt.Printf("%-10s %s (min=%d bytes)\n", "CAS", cfg.CAS.Root, cfg.CAS.MinFileSize)
	}
	if cfg.Policy.Enabled {
		fmt.Printf("%-10s RPO=%s drill=%s\n", "policy", cfg.Policy.MaxBackupAge, cfg.Policy.MaxRecoveryDrillAge)
	}
	if cfg.Observability.Enabled {
		fmt.Printf("%-10s %s\n", "metrics", cfg.Observability.Listen)
	}
	if cfg.ControlPlane.Enabled {
		st, err := controlplane.OpenStore(cfg.ControlPlane.Store)
		if err != nil {
			fmt.Printf("%-10s ERR %v\n", "control", err)
			failed = true
		} else {
			_ = st.Close()
			fmt.Printf("%-10s OK  %s\n", "control", cfg.ControlPlane.Store.Driver)
		}
	}
	if cfg.GitLab.Enabled {
		fmt.Printf("%-10s %s (%s)\n", "GitLab", cfg.GitLab.URL, cfg.GitLab.Image)
	}
	if failed {
		return errors.New("doctor found missing required prerequisites")
	}
	return nil
}

func daemon(ctx context.Context, cfg config.Config) error {
	interval, err := time.ParseDuration(cfg.Daemon.Interval)
	if err != nil {
		return err
	}
	if cfg.Observability.Enabled {
		go func() {
			if err := observability.New(cfg).Run(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "observability server:", err)
			}
		}()
		fmt.Printf("[%s] health/metrics listening on %s\n", time.Now().Format(time.RFC3339), cfg.Observability.Listen)
	}
	run := func() {
		started := time.Now()
		fmt.Printf("[%s] backup cycle started\n", started.Format(time.RFC3339))
		mgr := backup.New(cfg)
		man, cycleErr := mgr.Run(ctx, printBackupEvent)
		fmt.Printf("[%s] backup cycle finished: ok=%d failed=%d warnings=%d err=%v\n", time.Now().Format(time.RFC3339), man.Succeeded, man.Failed, man.WarningCount, cycleErr)
		if cycleErr == nil && cfg.RecoveryDrill.Enabled {
			report, drillErr := mgr.Drill(ctx, cfg.RecoveryDrill.SampleSize, printBackupEvent)
			fmt.Printf("[%s] recovery drill: ok=%d failed=%d err=%v\n", time.Now().Format(time.RFC3339), report.Succeeded, report.Failed, drillErr)
			if drillErr != nil {
				cycleErr = drillErr
			}
		}
		if cycleErr == nil && cfg.Offsite.Enabled {
			if offErr := offsite.Sync(ctx, cfg); offErr != nil {
				fmt.Printf("[%s] offsite sync failed: %v\n", time.Now().Format(time.RFC3339), offErr)
				cycleErr = offErr
			} else {
				fmt.Printf("[%s] offsite sync complete\n", time.Now().Format(time.RFC3339))
			}
		}
		msg := fmt.Sprintf("RepoArk cycle: %d repos OK, %d failed, %d warnings, duration %s", man.Succeeded, man.Failed, man.WarningCount, time.Since(started).Round(time.Second))
		if cycleErr != nil {
			msg += "; error: " + cycleErr.Error()
		}
		if nerr := notify.Send(ctx, cfg, msg, cycleErr == nil); nerr != nil {
			fmt.Fprintln(os.Stderr, "notification warning:", nerr)
		}
	}
	if cfg.Daemon.RunOnStart {
		run()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			run()
		}
	}
}

func runGitHub(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) < 2 || args[0] != "export" {
		return errors.New("usage: repoark github export <user|org NAME>")
	}
	token := githubapi.ResolveToken(cfg.GitHub.TokenEnv)
	if token == "" {
		return fmt.Errorf("%s/GH_TOKEN/GITHUB_TOKEN is not set", cfg.GitHub.TokenEnv)
	}
	client := githubapi.New(cfg.GitHub.APIURL, token)
	client.MaxPages = cfg.GitHub.MaxMetadataPages
	user, err := client.User(ctx)
	if err != nil {
		return err
	}
	repos, err := client.Repositories(ctx)
	if err != nil {
		return err
	}
	var scope, label string
	var selected []string
	switch args[1] {
	case "user":
		scope = "user"
		label = "user-" + user.Login
		prefix := strings.ToLower(user.Login) + "/"
		for _, r := range repos {
			if strings.HasPrefix(strings.ToLower(r.FullName), prefix) {
				selected = append(selected, r.FullName)
			}
		}
	case "org":
		if len(args) < 3 {
			return errors.New("usage: repoark github export org ORG")
		}
		org := strings.TrimSpace(args[2])
		if org == "" {
			return errors.New("organization is empty")
		}
		scope = "org:" + org
		label = "org-" + strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
				return r
			}
			return '_'
		}, org)
		prefix := strings.ToLower(org) + "/"
		for _, r := range repos {
			if strings.HasPrefix(strings.ToLower(r.FullName), prefix) {
				selected = append(selected, r.FullName)
			}
		}
	default:
		return errors.New("usage: repoark github export <user|org NAME>")
	}
	if len(selected) == 0 {
		return fmt.Errorf("no repositories found for %s", label)
	}
	dir := filepath.Join(cfg.Backup.Root, "official-exports", label)
	name := time.Now().UTC().Format("20060102T150405Z") + ".tar.gz"
	dst := filepath.Join(dir, name)
	fmt.Printf("Starting official GitHub migration export for %d repositories\n", len(selected))
	if err := client.ExportMigration(ctx, scope, selected, dst, func(msg string) { fmt.Println("  " + msg) }); err != nil {
		return err
	}
	if cfg.CAS.Enabled {
		if r, cerr := cas.New(cfg.CAS.Root, cfg.CAS.MinFileSize).Ingest(dst); cerr != nil {
			fmt.Fprintln(os.Stderr, "CAS warning:", cerr)
		} else if r.SHA256 != "" {
			fmt.Printf("CAS: sha256:%s hardlink=%t reused=%t\n", r.SHA256, r.HardLinked, r.Existing)
		}
	}
	fmt.Println("Archive:", dst)
	fmt.Println("Checksum:", dst+".sha256")
	return nil
}

func runKeys(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: repoark keys <generate|verify>")
	}
	switch args[0] {
	case "generate":
		_, _, err := signing.EnsureKey(cfg.Security.SigningKeyPath)
		if err != nil {
			return err
		}
		fmt.Println("Private key:", cfg.Security.SigningKeyPath)
		fmt.Println("Public key: ", cfg.Security.SigningKeyPath+".pub")
		return nil
	case "verify":
		if err := manifest.VerifyLatestSignature(cfg.Backup.Root, cfg.Security.SigningKeyPath+".pub"); err != nil {
			return err
		}
		if cfg.Security.KMSAttestation.Enabled {
			if err := kmsattest.VerifyFile(context.Background(), filepath.Join(cfg.Backup.Root, "manifests", "latest.json"), cfg.Security.KMSAttestation); err != nil {
				return err
			}
			fmt.Println("Latest manifest local Ed25519 + AWS KMS attestations are valid")
		} else {
			fmt.Println("Latest manifest signature is valid")
		}
		return nil
	default:
		return fmt.Errorf("unknown keys command %q", args[0])
	}
}

func runGitLab(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: repoark gitlab <compose|deploy|password|backup|migrate|drill>")
	}
	g := gitlab.New(cfg)
	switch args[0] {
	case "compose":
		path, err := g.WriteCompose()
		if err == nil {
			fmt.Println(path)
		}
		return err
	case "deploy":
		remote := valueAfter(args[1:], "--remote")
		if remote == "" {
			remote = cfg.GitLab.RemoteHost
		}
		if err := g.Deploy(ctx, remote); err != nil {
			return err
		}
		fmt.Println("GitLab deployment started. Initial startup can take several minutes; check container health with Docker.")
		return nil
	case "password":
		remote := valueAfter(args[1:], "--remote")
		p, err := g.InitialPassword(ctx, remote)
		if err != nil {
			return err
		}
		if strings.TrimSpace(p) == "" {
			fmt.Println("Initial password file is unavailable or has already expired; reset the root password in GitLab.")
		} else {
			fmt.Println(p)
		}
		return nil
	case "backup":
		remote := valueAfter(args[1:], "--remote")
		path, err := g.Backup(ctx, remote)
		if err == nil && cfg.CAS.Enabled {
			if _, casErr := cas.New(cfg.CAS.Root, cfg.CAS.MinFileSize).Ingest(path); casErr != nil {
				err = fmt.Errorf("GitLab backup CAS ingest: %w", casErr)
			}
		}
		if err == nil {
			fmt.Println(path)
		}
		return err
	case "migrate":
		err := g.MigrateLatest(ctx, func(e gitlab.Event) { fmt.Printf("[%d/%d] %-30s %s\n", e.Done, e.Total, e.Repo, e.Message) })
		return auditResult(cfg, "gitlab-migrate", cfg.GitLab.URL, err, nil)
	case "drill":
		archive := ""
		if len(args) > 1 {
			archive = args[1]
		}
		report, err := g.RestoreDrill(ctx, archive, func(msg string) { fmt.Println("  " + msg) })
		if err == nil {
			fmt.Printf("GitLab restore drill passed: backup=%s duration=%s\n", report.BackupID, report.Duration.Round(time.Second))
		} else if report.WorkDir != "" {
			fmt.Println("Drill workdir:", report.WorkDir)
		}
		return auditResult(cfg, "gitlab-restore-drill", report.Archive, err, map[string]any{"backup_id": report.BackupID, "healthy": report.Healthy})
	default:
		return fmt.Errorf("unknown gitlab command %q", args[0])
	}
}

func runFleet(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: repoark fleet <backup|verify>")
	}
	emit := func(e fleet.Event) { fmt.Printf("[%-16s] %-10s %s\n", e.Account, e.Stage, e.Message) }
	var results []fleet.Result
	var err error
	switch args[0] {
	case "backup":
		results, err = fleet.RunBackup(ctx, cfg, emit)
	case "verify":
		results, err = fleet.RunVerify(ctx, cfg, emit)
	default:
		return fmt.Errorf("unknown fleet command %q", args[0])
	}
	for _, r := range results {
		fmt.Printf("%-16s root=%s ok=%d failed=%d warnings=%d err=%v\n", r.Account, r.Root, r.Succeeded, r.Failed, r.Warnings, r.Err)
	}
	return auditResult(cfg, "fleet-"+args[0], "fleet", err, map[string]any{"accounts": len(results)})
}

func runAudit(cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return errors.New("usage: repoark audit verify")
	}
	n, err := audit.Verify(cfg.Audit.Path)
	if err == nil {
		if _, statErr := os.Stat(cfg.Audit.Path + ".checkpoint.json"); statErr == nil {
			err = audit.VerifyCheckpoint(cfg.Audit.Path, cfg.Security.SigningKeyPath+".pub")
		}
	}
	if err == nil {
		fmt.Printf("Audit ledger valid: %d records; signed checkpoint valid\n", n)
	}
	return err
}

func runCAS(cfg config.Config, args []string) error {
	if !cfg.CAS.Enabled {
		return errors.New("CAS is disabled in config")
	}
	if len(args) == 0 {
		return errors.New("usage: repoark cas <stats|verify|compact|gc|erasure-protect|erasure-verify|erasure-reconstruct>")
	}
	store := cas.New(cfg.CAS.Root, cfg.CAS.MinFileSize)
	switch args[0] {
	case "stats":
		st, err := store.Stats()
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(b))
		return nil
	case "verify":
		st, err := store.Verify()
		if err != nil {
			return err
		}
		fmt.Printf("CAS valid: %d objects, %d physical bytes\n", st.Objects, st.PhysicalBytes)
		return nil
	case "compact":
		paths := casCompactPaths(cfg)
		st, err := store.Compact(paths)
		if err != nil {
			return err
		}
		fmt.Printf("CAS compacted: logical=%d files/%d bytes objects=%d/%d bytes reclaimed=%d bytes\n", st.LogicalFiles, st.LogicalBytes, st.Objects, st.PhysicalBytes, st.Reclaimed)
		return nil
	case "gc":
		dryRun := contains(args[1:], "--dry-run")
		protected := map[string]struct{}{}
		if cfg.ControlPlane.Enabled {
			if cp, e := controlplane.OpenStore(cfg.ControlPlane.Store); e == nil {
				defer cp.Close()
				if roots, e := cp.ProtectedObjectDigests(context.Background(), time.Now().UTC()); e == nil {
					protected = roots
				}
			}
		}
		gc, err := store.GCProtected(casReachabilityRoots(cfg), dryRun, protected)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(gc, "", "  ")
		fmt.Println(string(b))
		return nil
	case "erasure-protect":
		ec := cfg.ControlPlane.Storage.Erasure
		if !ec.Enabled {
			return errors.New("control_plane.storage.erasure is disabled")
		}
		n, err := erasure.ProtectPaths(cfg.CAS.Root, []string{filepath.Join(cfg.CAS.Root, "sha256")}, ec.MinObjectBytes, erasure.Config{DataShards: ec.DataShards, ParityShards: ec.ParityShards, BlockBytes: ec.BlockBytes})
		if err == nil {
			fmt.Printf("erasure-protected objects=%d\n", n)
		}
		return err
	case "erasure-verify":
		if len(args) < 2 {
			return errors.New("usage: repoark cas erasure-verify SHA256")
		}
		d := strings.ToLower(strings.TrimSpace(args[1]))
		if len(d) != 64 {
			return errors.New("SHA256 digest required")
		}
		m, err := erasure.Verify(filepath.Join(cfg.CAS.Root, "erasure", d[:2], d))
		if err == nil {
			b, _ := json.MarshalIndent(m, "", "  ")
			fmt.Println(string(b))
		}
		return err
	case "erasure-reconstruct":
		if len(args) < 3 {
			return errors.New("usage: repoark cas erasure-reconstruct SHA256 OUTPUT")
		}
		d := strings.ToLower(strings.TrimSpace(args[1]))
		if len(d) != 64 {
			return errors.New("SHA256 digest required")
		}
		return erasure.Reconstruct(filepath.Join(cfg.CAS.Root, "erasure", d[:2], d), args[2])
	default:
		return fmt.Errorf("unknown cas command %q", args[0])
	}
}

func runStorage(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: repoark storage <disk-health|scrub|tier>")
	}
	switch args[0] {
	case "disk-health":
		r := storagehealth.ProbeDisk(ctx, cfg.ControlPlane.Storage.DiskTelemetry)
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
		if r.Error != "" {
			return errors.New(r.Error)
		}
		return nil
	case "scrub":
		if !cfg.CAS.Enabled {
			return errors.New("CAS is disabled")
		}
		repair := scrub.RepairFunc(nil)
		if cfg.ControlPlane.Storage.Scrub.Repair {
			repair = scrub.LocalErasureRepair(cfg.CAS.Root)
		}
		r, err := (scrub.Scrubber{CAS: cas.New(cfg.CAS.Root, 0), SampleObjects: cfg.ControlPlane.Storage.Scrub.SampleObjects, SeedSalt: cfg.ControlPlane.Storage.Scrub.SeedSalt, Repair: repair}).Run(ctx)
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
		return err
	case "tier":
		if !cfg.CAS.Enabled {
			return errors.New("CAS is disabled")
		}
		age, _ := time.ParseDuration(cfg.ControlPlane.Storage.Tiering.MinAge)
		r, err := tiering.CopyTier(ctx, cas.New(cfg.CAS.Root, 0), tiering.Config{ColdRoot: cfg.ControlPlane.Storage.Tiering.ColdRoot, MinAge: age, MinBytes: cfg.ControlPlane.Storage.Tiering.MinBytes, RcloneRemote: cfg.ControlPlane.Storage.Tiering.RcloneRemote}, time.Now().UTC())
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
		return err
	default:
		return fmt.Errorf("unknown storage command %q", args[0])
	}
}

func casCompactPaths(cfg config.Config) []string {
	paths := []string{
		filepath.Join(cfg.Backup.Root, "release-assets"), filepath.Join(cfg.Backup.Root, "actions-artifacts"),
		filepath.Join(cfg.Backup.Root, "oci"), filepath.Join(cfg.Backup.Root, "packages"),
		filepath.Join(cfg.Backup.Root, "official-exports"), filepath.Join(cfg.Backup.Root, "lfs"),
		filepath.Join(cfg.Backup.Root, "bundles"),
	}
	for _, a := range cfg.Fleet.Accounts {
		if a.BackupRoot == "" {
			continue
		}
		paths = append(paths,
			filepath.Join(a.BackupRoot, "release-assets"), filepath.Join(a.BackupRoot, "actions-artifacts"),
			filepath.Join(a.BackupRoot, "oci"), filepath.Join(a.BackupRoot, "packages"),
			filepath.Join(a.BackupRoot, "official-exports"), filepath.Join(a.BackupRoot, "lfs"), filepath.Join(a.BackupRoot, "bundles"))
	}
	if cfg.GitLab.Enabled {
		paths = append(paths, filepath.Join(cfg.GitLab.DataDir, "exports"))
	}
	return paths
}

func casReachabilityRoots(cfg config.Config) []string {
	roots := []string{cfg.Backup.Root}
	for _, a := range cfg.Fleet.Accounts {
		if a.BackupRoot != "" {
			roots = append(roots, a.BackupRoot)
		}
	}
	if cfg.GitLab.Enabled {
		roots = append(roots, filepath.Join(cfg.GitLab.DataDir, "exports"))
	}
	return roots
}

func runPolicy(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("usage: repoark policy check")
	}
	r := policy.Evaluate(ctx, cfg, time.Now())
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	return policy.Error(r)
}

func auditResult(cfg config.Config, action, target string, opErr error, fields map[string]any) error {
	if !cfg.Audit.Enabled {
		return opErr
	}
	status, detail := "ok", ""
	if opErr != nil {
		status, detail = "error", opErr.Error()
	}
	_, aerr := audit.Append(cfg.Audit.Path, action, target, status, detail, fields)
	if aerr == nil && cfg.Security.SignManifests {
		aerr = audit.WriteCheckpoint(cfg.Audit.Path, cfg.Security.SigningKeyPath)
	}
	if aerr != nil {
		fmt.Fprintln(os.Stderr, "audit warning:", aerr)
		if cfg.Audit.Required && opErr == nil {
			return fmt.Errorf("audit append: %w", aerr)
		}
	}
	return opErr
}

func printBackupEvent(e backup.Event) {
	if e.Repo != "" {
		fmt.Printf("[%d/%d] %-35s %-10s %s\n", e.Done, e.Total, e.Repo, e.Stage, e.Message)
	} else {
		fmt.Printf("%-10s %s\n", e.Stage, e.Message)
	}
}

func askPass(args []string) error {
	prompt := ""
	if len(args) > 0 {
		prompt = strings.ToLower(args[0])
	}
	if strings.Contains(prompt, "username") {
		fmt.Print(os.Getenv("REPOARK_GIT_USERNAME"))
		return nil
	}
	fmt.Print(os.Getenv("REPOARK_GIT_TOKEN"))
	return nil
}

func printHelp() {
	fmt.Print(`RepoArk — GitHub backup + GitLab disaster recovery center

Usage:
  repoark                         Start the browser console (default)
  repoark init [--force]          Create configuration
  repoark doctor                  Check Git/GitHub/Docker prerequisites
  repoark backup                  Backup all accessible GitHub repositories
  repoark verify                  Verify signature, mirrors, bundles, assets and checksums
  repoark restore OWNER/REPO [DIR] Restore a repository from its bundle
  repoark drill [N]               Perform real restore drills for N repositories
  repoark keys generate           Create/ensure Ed25519 manifest signing key
  repoark keys verify             Verify latest detached manifest signature
  repoark serve                   Compatibility alias for the browser console
  repoark daemon                  Run scheduled backups/drills/offsite replication
  repoark fleet backup            Back up all configured GitHub accounts
  repoark fleet verify            Verify every configured account backup
  repoark audit verify            Verify tamper-evident audit hash chain
  repoark cas stats               Show content-addressed storage statistics
  repoark cas verify              Verify every CAS object by SHA-256
  repoark cas compact             Deduplicate immutable backup payloads
  repoark cas gc [--dry-run]      Garbage-collect unreachable CAS objects
  repoark policy check            Evaluate RPO/RTO and recovery-readiness policy
  repoark offsite                 Replicate with restic/rclone + optional immutable S3
  repoark offsite verify-lock     Verify S3 Object Lock + retention + versioning
  repoark offsite configure-lock  Apply configured default retention (explicit)
  repoark gitlab compose          Generate Docker Compose for GitLab CE
  repoark gitlab deploy [--remote user@host]
  repoark gitlab password [--remote user@host]
  repoark gitlab backup [--remote user@host]
  repoark gitlab migrate          Push mirrors preserving GitHub owner namespaces
  repoark gitlab drill [ARCHIVE]  Full disposable GitLab application restore drill
  repoark github export user      Official GitHub migration archive for owned repos
  repoark github export org ORG   Official GitHub migration archive for an organization
  repoark control serve           Run durable scheduler + local workers + optional mTLS API
  repoark control sync            Discover repositories into the control-plane store
  repoark control jobs            List durable queued/running/completed jobs
  repoark control retry JOB_ID    Retry a terminal failed job
  repoark control replicas        Show HA generation replica placement/health
  repoark control replicate       Run generation + optional CAS reconciliation
  repoark control inventory [A B] List/compare compact CAS Merkle inventories
  repoark control restore-request REPO GENERATION  Create approval-gated restore request
  repoark control approvals       List restore approval workflow state
  repoark control approve ID      Approve a restore request (two-person optional)
  repoark control restore-approved ID Execute an approved point-in-time restore
  repoark control enqueue REPO    Queue one repository backup
  repoark generations list REPO   List point-in-time backup generations
  repoark restore REPO DIR --generation ID  Restore a selected immutable generation
  repoark agents pki-init         Create local CA + server certificate
  repoark agents issue NAME       Issue an mTLS client certificate for an agent
  repoark agent run               Run a remote mTLS worker agent
  repoark version

Global:
  --config PATH                   Override config path

Secrets are read from environment variables, never written to config.
`)
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
func valueAfter(xs []string, key string) string {
	for i := 0; i+1 < len(xs); i++ {
		if xs[i] == key {
			return xs[i+1]
		}
	}
	return ""
}

func localActor() string {
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.Username) != "" {
		return strings.TrimSpace(u.Username)
	}
	return "local-user"
}
func identityAllowed(actor string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, x := range allow {
		if strings.EqualFold(strings.TrimSpace(x), actor) {
			return true
		}
	}
	return false
}

func restoreJobTarget(repo, generation, target string) string {
	sum := sha256.Sum256([]byte(repo + "\x00" + generation + "\x00" + target))
	return fmt.Sprintf("restore:%s@%s@%x", repo, generation, sum[:6])
}

func runControl(ctx context.Context, cfg config.Config, args []string) error {
	if !cfg.ControlPlane.Enabled {
		return errors.New("control_plane is disabled in config")
	}
	store, err := controlplane.OpenStore(cfg.ControlPlane.Store)
	if err != nil {
		return err
	}
	defer store.Close()
	if len(args) == 0 {
		return errors.New("usage: repoark control <serve|sync|jobs|retry|enqueue|repos|stats|replicas|replicate|erasure|restore-request|approvals|approve|restore-approved|inventory>")
	}
	sched := controlplane.Scheduler{Store: store, Config: cfg}
	switch args[0] {
	case "sync":
		n, err := sched.SyncRepositories(ctx)
		if err == nil {
			fmt.Printf("Discovered %d repositories\n", n)
		}
		return err
	case "jobs":
		jobs, err := store.ListJobs(ctx, 200)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(jobs, "", "  ")
		fmt.Println(string(b))
		return nil
	case "retry":
		if len(args) < 2 {
			return errors.New("usage: repoark control retry JOB_ID")
		}
		return store.RetryJob(ctx, args[1])
	case "enqueue":
		if len(args) < 2 {
			return errors.New("usage: repoark control enqueue OWNER/REPO")
		}
		repos, err := store.ListRepositories(ctx)
		if err != nil {
			return err
		}
		var rec *controlplane.Repository
		for i := range repos {
			if strings.EqualFold(repos[i].FullName, args[1]) {
				rec = &repos[i]
				break
			}
		}
		if rec == nil {
			return fmt.Errorf("repository %s is not in control-plane store; run control sync", args[1])
		}
		payload := fmt.Sprintf(`{"repository_id":%q,"full_name":%q}`, rec.ID, rec.FullName)
		j, created, err := store.Enqueue(ctx, controlplane.Job{Kind: "backup-repo", Target: rec.FullName, Payload: payload, Priority: rec.Priority, MaxAttempts: cfg.ControlPlane.Workers.MaxAttempts})
		if err == nil {
			fmt.Printf("job=%s created=%t\n", j.ID, created)
		}
		return err
	case "restore":
		if cfg.ControlPlane.RestoreAuth.Enabled {
			return errors.New("approval-gated restore is enabled; use control restore-request/approve/restore-approved")
		}
		if len(args) < 3 {
			return errors.New("usage: repoark control restore OWNER/REPO GENERATION_ID [--target PATH]")
		}
		fullName, generationID := args[1], args[2]
		target := valueAfter(args[3:], "--target")
		repos, err := store.ListRepositories(ctx)
		if err != nil {
			return err
		}
		var rec *controlplane.Repository
		for i := range repos {
			if strings.EqualFold(repos[i].FullName, fullName) {
				rec = &repos[i]
				break
			}
		}
		if rec == nil {
			return fmt.Errorf("repository %s is not in control-plane store", fullName)
		}
		gens, err := store.ListGenerations(ctx, rec.ID, 10000)
		if err != nil {
			return err
		}
		var selected *controlplane.Generation
		for i := range gens {
			if gens[i].ID == generationID {
				selected = &gens[i]
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("generation %s for %s not found", generationID, fullName)
		}
		affinity := controlplane.AffinityFromMetaPath(selected.MetaPath)
		if cfg.ControlPlane.Replication.Enabled {
			affinity, err = controlplane.SelectRestoreAffinityWithStorage(ctx, store, selected.ID, selected.MetaPath, cfg.ControlPlane.Replication, cfg.ControlPlane.Storage)
			if err != nil {
				return err
			}
		}
		payload, _ := json.Marshal(map[string]string{"repository": rec.FullName, "generation_id": generationID, "target": target})
		j, created, err := store.Enqueue(ctx, controlplane.Job{Kind: "restore-generation", Target: restoreJobTarget(rec.FullName, generationID, target), Payload: string(payload), Affinity: affinity, Priority: 200, MaxAttempts: cfg.ControlPlane.Workers.MaxAttempts})
		if err == nil {
			fmt.Printf("restore job=%s created=%t affinity=%q\n", j.ID, created, j.Affinity)
		}
		return err
	case "replicas":
		reps, err := store.ListAllReplicas(ctx, 50000)
		if err != nil {
			return err
		}
		health, herr := controlplane.ReplicationHealthWithStorage(ctx, store, cfg.ControlPlane.Replication, cfg.ControlPlane.Storage)
		if herr != nil {
			return herr
		}
		b, _ := json.MarshalIndent(map[string]any{"health": health, "replicas": reps}, "", "  ")
		fmt.Println(string(b))
		return nil
	case "replicate":
		n, err := (controlplane.ReplicationReconciler{Store: store, Config: cfg, Emit: func(m string) { fmt.Println(m) }}).Reconcile(ctx)
		if err == nil {
			fmt.Printf("queued=%d\n", n)
		}
		return err
	case "restore-request":
		if len(args) < 3 {
			return errors.New("usage: repoark control restore-request OWNER/REPO GENERATION_ID [--target PATH]")
		}
		actor := localActor()
		if !identityAllowed(actor, cfg.ControlPlane.RestoreAuth.Requesters) {
			return fmt.Errorf("actor %q is not allowed to request restores", actor)
		}
		ttl, _ := time.ParseDuration(cfg.ControlPlane.RestoreAuth.ApprovalTTL)
		if ttl <= 0 {
			ttl = 30 * time.Minute
		}
		a := controlplane.RestoreApproval{ID: "restore-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36), Repository: args[1], GenerationID: args[2], Target: valueAfter(args[3:], "--target"), RequestedBy: actor, Status: controlplane.ApprovalPending, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(ttl)}
		if err := store.CreateRestoreApproval(ctx, a); err != nil {
			return err
		}
		fmt.Printf("restore request=%s expires=%s\n", a.ID, a.ExpiresAt.Format(time.RFC3339))
		return nil
	case "approvals":
		xs, err := store.ListRestoreApprovals(ctx, 200)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(xs, "", "  ")
		fmt.Println(string(b))
		return nil
	case "approve":
		if len(args) < 2 {
			return errors.New("usage: repoark control approve REQUEST_ID")
		}
		actor := localActor()
		if !identityAllowed(actor, cfg.ControlPlane.RestoreAuth.Approvers) {
			return fmt.Errorf("actor %q is not allowed to approve restores", actor)
		}
		if err := store.ApproveRestore(ctx, args[1], actor, cfg.ControlPlane.RestoreAuth.RequireDistinctApprover); err != nil {
			return err
		}
		fmt.Println("approved", args[1], "by", actor)
		return nil
	case "restore-approved":
		if len(args) < 2 {
			return errors.New("usage: repoark control restore-approved REQUEST_ID")
		}
		a, err := store.GetRestoreApproval(ctx, args[1])
		if err != nil {
			return err
		}
		if a.Status != controlplane.ApprovalApproved {
			return fmt.Errorf("restore request is %s", a.Status)
		}
		repos, err := store.ListRepositories(ctx)
		if err != nil {
			return err
		}
		var rec *controlplane.Repository
		for i := range repos {
			if strings.EqualFold(repos[i].FullName, a.Repository) {
				rec = &repos[i]
				break
			}
		}
		if rec == nil {
			return fmt.Errorf("repository %s is not in control-plane store", a.Repository)
		}
		gens, err := store.ListGenerations(ctx, rec.ID, 10000)
		if err != nil {
			return err
		}
		var selected *controlplane.Generation
		for i := range gens {
			if gens[i].ID == a.GenerationID {
				selected = &gens[i]
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("generation %s not found", a.GenerationID)
		}
		affinity := controlplane.AffinityFromMetaPath(selected.MetaPath)
		if cfg.ControlPlane.Replication.Enabled {
			affinity, err = controlplane.SelectRestoreAffinityWithStorage(ctx, store, selected.ID, selected.MetaPath, cfg.ControlPlane.Replication, cfg.ControlPlane.Storage)
			if err != nil {
				return err
			}
		}
		if err := store.ScheduleRestore(ctx, a.ID); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"repository": rec.FullName, "generation_id": a.GenerationID, "target": a.Target, "approval_id": a.ID})
		j, created, err := store.Enqueue(ctx, controlplane.Job{Kind: "restore-generation", Target: restoreJobTarget(rec.FullName, a.GenerationID, a.Target) + "@" + a.ID, Payload: string(payload), Affinity: affinity, Priority: 250, MaxAttempts: cfg.ControlPlane.Workers.MaxAttempts})
		if err != nil {
			_ = store.ReleaseRestoreSchedule(ctx, a.ID)
			return err
		}
		fmt.Printf("restore job=%s created=%t approval=%s status=scheduled\n", j.ID, created, a.ID)
		return nil
	case "repos":
		repos, err := store.ListRepositories(ctx)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(repos, "", "  ")
		fmt.Println(string(b))
		return nil
	case "inventory":
		if len(args) >= 3 {
			cmp, err := controlplane.CompareInventories(ctx, store, args[1], args[2])
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(cmp, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		xs, err := controlplane.AgentInventories(ctx, store)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(xs, "", "  ")
		fmt.Println(string(b))
		return nil
	case "erasure":
		h, err := controlplane.EvaluateDistributedErasureHealth(ctx, store, cfg, time.Now().UTC())
		if err != nil {
			return err
		}
		queued := 0
		if contains(args[1:], "--reconcile") {
			queued, err = (controlplane.ErasureReconciler{Store: store, Config: cfg, Emit: func(m string) { fmt.Println(m) }}).Reconcile(ctx)
			if err != nil {
				return err
			}
		}
		b, _ := json.MarshalIndent(map[string]any{"health": h, "queued": queued}, "", "  ")
		fmt.Println(string(b))
		return nil
	case "stats":
		st, err := store.Stats(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		var repl any = map[string]any{"enabled": false}
		if cfg.ControlPlane.Replication.Enabled {
			if rh, e := controlplane.ReplicationHealthWithStorage(ctx, store, cfg.ControlPlane.Replication, cfg.ControlPlane.Storage); e == nil {
				repl = rh
			}
		}
		b, _ := json.MarshalIndent(map[string]any{"stats": st, "replication": repl}, "", "  ")
		fmt.Println(string(b))
		return nil
	case "serve":
		return serveControl(ctx, cfg, store, sched)
	default:
		return fmt.Errorf("unknown control command %q", args[0])
	}
}

func serveControl(ctx context.Context, cfg config.Config, store controlplane.Store, sched controlplane.Scheduler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 6)
	runner := controlplane.Runner{Store: store, Config: cfg, Emit: func(s string) { fmt.Println("[worker]", s) }}
	workers := controlplane.WorkerPool{Store: store, Runner: runner, Config: cfg.ControlPlane.Workers, Owner: controlplane.LocalWorkerAffinity}
	if cfg.ControlPlane.Scheduler.Enabled {
		go func() { errCh <- sched.Run(ctx) }()
	}
	go func() { errCh <- workers.Run(ctx) }()
	if cfg.ControlPlane.Replication.Enabled {
		go func() {
			errCh <- (controlplane.ReplicationReconciler{Store: store, Config: cfg, Emit: func(m string) { fmt.Println("[replication]", m) }}).Run(ctx)
		}()
	}
	if cfg.ControlPlane.Agents.Enabled {
		go func() {
			fmt.Println("mTLS agent API listening on", cfg.ControlPlane.Agents.Listen)
			errCh <- controlplane.AgentServer{Store: store, Config: cfg.ControlPlane.Agents, Worker: cfg.ControlPlane.Workers, Generations: cfg.ControlPlane.Generations, Replication: cfg.ControlPlane.Replication, Storage: cfg.ControlPlane.Storage}.Run(ctx)
		}()
	}
	if cfg.Observability.Enabled {
		go func() { errCh <- observability.New(cfg).Run(ctx) }()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				cancel()
				return err
			}
		}
	}
}

func runGenerations(ctx context.Context, cfg config.Config, args []string) error {
	if !cfg.ControlPlane.Generations.Enabled {
		return errors.New("control_plane.generations is disabled")
	}
	if len(args) < 2 || args[0] != "list" {
		return errors.New("usage: repoark generations list OWNER/REPO")
	}
	xs, err := generation.List(cfg.ControlPlane.Generations.Root, args[1])
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(xs, "", "  ")
	fmt.Println(string(b))
	return nil
}
func runAgent(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "run" {
		return errors.New("usage: repoark agent run")
	}
	runner := controlplane.Runner{Store: controlplane.NewMemoryStore(), Config: cfg, Emit: func(s string) { fmt.Println("[agent]", s) }}
	return controlplane.AgentClient{Config: cfg, Runner: runner}.Run(ctx)
}
func runAgents(ctx context.Context, cfg config.Config, args []string) error {
	_ = ctx
	if len(args) == 0 {
		return errors.New("usage: repoark agents <pki-init|issue|list>")
	}
	switch args[0] {
	case "pki-init":
		if err := controlplane.InitPKI(cfg.ControlPlane.Agents); err != nil {
			return err
		}
		fmt.Println(controlplane.PKISummary(cfg.ControlPlane.Agents))
		return nil
	case "issue":
		if len(args) < 2 {
			return errors.New("usage: repoark agents issue NAME")
		}
		dir := filepath.Join(filepath.Dir(cfg.ControlPlane.Agents.CAPath), "agents", args[1])
		cert := dir + ".pem"
		key := dir + "-key.pem"
		if err := controlplane.IssueAgent(cfg.ControlPlane.Agents, args[1], cert, key); err != nil {
			return err
		}
		fmt.Println("certificate:", cert)
		fmt.Println("private key:", key)
		return nil
	case "list":
		if !cfg.ControlPlane.Enabled {
			return errors.New("control_plane disabled")
		}
		st, err := controlplane.OpenStore(cfg.ControlPlane.Store)
		if err != nil {
			return err
		}
		defer st.Close()
		xs, err := st.ListAgents(context.Background())
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(xs, "", "  ")
		fmt.Println(string(b))
		return nil
	default:
		return fmt.Errorf("unknown agents command %q", args[0])
	}
}
