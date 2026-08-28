package offsite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/state"
)

func Sync(ctx context.Context, cfg config.Config) (err error) {
	started := time.Now().UTC()
	defer func() {
		rec := state.Record{Kind: "offsite-sync", OK: err == nil, StartedAt: started, EndedAt: time.Now().UTC(), Data: map[string]any{"backend": cfg.Offsite.Backend}}
		if err != nil {
			rec.Detail = err.Error()
		}
		_ = state.Write(filepath.Join(cfg.Backup.Root, "state", "offsite.json"), rec)
	}()
	if !cfg.Offsite.Enabled {
		return errors.New("offsite backup is disabled in config")
	}
	switch cfg.Offsite.Backend {
	case "", "restic":
		err = syncRestic(ctx, cfg)
	case "rclone":
		err = syncRclone(ctx, cfg)
	default:
		err = fmt.Errorf("unsupported offsite backend %q", cfg.Offsite.Backend)
	}
	if err != nil {
		return err
	}
	if cfg.Offsite.ObjectLock.Enabled {
		return SyncObjectLock(ctx, cfg)
	}
	return nil
}

func syncRestic(ctx context.Context, cfg config.Config) error {
	if !execx.Exists("restic") {
		return errors.New("restic executable not found")
	}
	if os.Getenv(cfg.Offsite.RepositoryEnv) == "" {
		return fmt.Errorf("%s is not set", cfg.Offsite.RepositoryEnv)
	}
	if cfg.Offsite.PasswordEnv != "" && os.Getenv(cfg.Offsite.PasswordEnv) == "" && os.Getenv("RESTIC_PASSWORD_FILE") == "" && os.Getenv("RESTIC_PASSWORD_COMMAND") == "" {
		return fmt.Errorf("%s (or RESTIC_PASSWORD_FILE/RESTIC_PASSWORD_COMMAND) is not set", cfg.Offsite.PasswordEnv)
	}

	paths := backupPaths(cfg)
	args := append([]string{"backup", "--tag", "repoark"}, paths...)
	if _, err := execx.Run(ctx, "", nil, "restic", args...); err != nil {
		return err
	}
	if !cfg.Offsite.Prune {
		return nil
	}
	forget := []string{"forget", "--tag", "repoark", "--prune"}
	if cfg.Offsite.KeepDaily > 0 {
		forget = append(forget, "--keep-daily", strconv.Itoa(cfg.Offsite.KeepDaily))
	}
	if cfg.Offsite.KeepWeekly > 0 {
		forget = append(forget, "--keep-weekly", strconv.Itoa(cfg.Offsite.KeepWeekly))
	}
	if cfg.Offsite.KeepMonthly > 0 {
		forget = append(forget, "--keep-monthly", strconv.Itoa(cfg.Offsite.KeepMonthly))
	}
	_, err := execx.Run(ctx, "", nil, "restic", forget...)
	return err
}

func syncRclone(ctx context.Context, cfg config.Config) error {
	if !execx.Exists("rclone") {
		return errors.New("rclone executable not found")
	}
	remote := strings.TrimSpace(cfg.Offsite.RcloneRemote)
	if remote == "" {
		return errors.New("offsite.rclone_remote is empty")
	}
	// Keep backup-root and GitLab exports in separate remote prefixes to avoid
	// accidental overlap while remaining compatible with S3, MinIO and any
	// rclone-supported object store.
	if _, err := execx.Run(ctx, "", nil, "rclone", "sync", cfg.Backup.Root, joinRemote(remote, "backups"), "--checksum", "--fast-list", "--create-empty-src-dirs"); err != nil {
		return err
	}
	if cfg.GitLab.Enabled {
		exports := filepath.Join(cfg.GitLab.DataDir, "exports")
		if info, err := os.Stat(exports); err == nil && info.IsDir() {
			if _, err := execx.Run(ctx, "", nil, "rclone", "sync", exports, joinRemote(remote, "gitlab-exports"), "--checksum", "--fast-list", "--create-empty-src-dirs"); err != nil {
				return err
			}
		}
	}
	return nil
}

func backupPaths(cfg config.Config) []string {
	paths := []string{cfg.Backup.Root}
	if cfg.GitLab.Enabled {
		exports := filepath.Join(cfg.GitLab.DataDir, "exports")
		if info, err := os.Stat(exports); err == nil && info.IsDir() {
			paths = append(paths, exports)
		}
	}
	return paths
}

func joinRemote(remote, child string) string {
	remote = strings.TrimRight(remote, "/")
	return remote + "/" + child
}

// SyncObjectLock replicates RepoArk data into an S3 bucket configured with
// bucket-level Object Lock default retention. RepoArk intentionally never
// sends --delete: a new upload creates a new S3 object version while retained
// versions remain WORM-protected by the bucket policy.
func SyncObjectLock(ctx context.Context, cfg config.Config) error {
	ol := cfg.Offsite.ObjectLock
	if !ol.Enabled {
		return errors.New("offsite.object_lock is disabled")
	}
	if !execx.Exists("aws") {
		return errors.New("aws CLI not found; immutable S3 replication requires AWS CLI v2")
	}
	if err := VerifyObjectLock(ctx, cfg); err != nil {
		return err
	}
	base := "s3://" + strings.TrimSpace(ol.Bucket)
	prefix := strings.Trim(strings.TrimSpace(ol.Prefix), "/")
	if prefix != "" {
		base += "/" + prefix
	}
	args := []string{"s3", "sync", cfg.Backup.Root, base + "/backups", "--only-show-errors", "--no-follow-symlinks"}
	args = append(args, awsCommonArgs(ol)...)
	if _, err := execx.Run(ctx, "", nil, "aws", args...); err != nil {
		return err
	}
	if cfg.GitLab.Enabled {
		exports := filepath.Join(cfg.GitLab.DataDir, "exports")
		if info, err := os.Stat(exports); err == nil && info.IsDir() {
			args := []string{"s3", "sync", exports, base + "/gitlab-exports", "--only-show-errors", "--no-follow-symlinks"}
			args = append(args, awsCommonArgs(ol)...)
			if _, err := execx.Run(ctx, "", nil, "aws", args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func VerifyObjectLock(ctx context.Context, cfg config.Config) error {
	ol := cfg.Offsite.ObjectLock
	args := []string{"s3api", "get-object-lock-configuration", "--bucket", ol.Bucket, "--output", "json"}
	args = append(args, awsCommonArgs(ol)...)
	res, err := execx.Run(ctx, "", nil, "aws", args...)
	if err != nil {
		return fmt.Errorf("read S3 Object Lock configuration: %w", err)
	}
	var lockCfg struct {
		ObjectLockEnabled string `json:"ObjectLockEnabled"`
		Rule              *struct {
			DefaultRetention struct {
				Mode  string `json:"Mode"`
				Days  int    `json:"Days"`
				Years int    `json:"Years"`
			} `json:"DefaultRetention"`
		} `json:"Rule"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &lockCfg); err != nil {
		return fmt.Errorf("decode S3 Object Lock configuration: %w", err)
	}
	if !strings.EqualFold(lockCfg.ObjectLockEnabled, "Enabled") && ol.RequireEnabled {
		return errors.New("S3 bucket does not report Object Lock as Enabled")
	}
	if want := strings.ToUpper(strings.TrimSpace(ol.ExpectedMode)); want != "" {
		if lockCfg.Rule == nil || !strings.EqualFold(lockCfg.Rule.DefaultRetention.Mode, want) {
			return fmt.Errorf("S3 default retention mode is %q, policy requires %s", func() string {
				if lockCfg.Rule == nil {
					return ""
				}
				return lockCfg.Rule.DefaultRetention.Mode
			}(), want)
		}
	}
	if ol.MinRetentionDays > 0 {
		days := 0
		if lockCfg.Rule != nil {
			days = lockCfg.Rule.DefaultRetention.Days + lockCfg.Rule.DefaultRetention.Years*365
		}
		if days < ol.MinRetentionDays {
			return fmt.Errorf("S3 default retention is ~%d days, policy requires at least %d", days, ol.MinRetentionDays)
		}
	}
	args = []string{"s3api", "get-bucket-versioning", "--bucket", ol.Bucket, "--output", "json"}
	args = append(args, awsCommonArgs(ol)...)
	res, err = execx.Run(ctx, "", nil, "aws", args...)
	if err != nil {
		return fmt.Errorf("read S3 bucket versioning: %w", err)
	}
	var versioning struct {
		Status string `json:"Status"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &versioning); err != nil {
		return fmt.Errorf("decode S3 bucket versioning: %w", err)
	}
	if ol.RequireEnabled && !strings.EqualFold(versioning.Status, "Enabled") {
		return errors.New("S3 Object Lock requires bucket versioning to be Enabled")
	}
	return nil
}

func awsCommonArgs(ol config.ObjectLockConfig) []string {
	var args []string
	if strings.TrimSpace(ol.Region) != "" {
		args = append(args, "--region", ol.Region)
	}
	if strings.TrimSpace(ol.Profile) != "" {
		args = append(args, "--profile", ol.Profile)
	}
	if strings.TrimSpace(ol.EndpointURL) != "" {
		args = append(args, "--endpoint-url", ol.EndpointURL)
	}
	return args
}

// ConfigureObjectLockDefaultRetention explicitly configures bucket versioning
// and Object Lock default retention from RepoArk policy. COMPLIANCE mode is
// rejected unless allowCompliance is true because retained object versions
// cannot be shortened or deleted even by the root account until expiry.
func ConfigureObjectLockDefaultRetention(ctx context.Context, cfg config.Config, allowCompliance bool) error {
	ol := cfg.Offsite.ObjectLock
	if !ol.Enabled {
		return errors.New("offsite.object_lock is disabled")
	}
	mode := strings.ToUpper(strings.TrimSpace(ol.ExpectedMode))
	if mode != "GOVERNANCE" && mode != "COMPLIANCE" {
		return errors.New("object_lock.expected_mode must be GOVERNANCE or COMPLIANCE")
	}
	if ol.MinRetentionDays <= 0 {
		return errors.New("object_lock.min_retention_days must be > 0 to configure default retention")
	}
	if mode == "COMPLIANCE" && !allowCompliance {
		return errors.New("refusing COMPLIANCE retention without --allow-compliance; this is intentionally irreversible for retained versions")
	}
	if !execx.Exists("aws") {
		return errors.New("aws CLI not found")
	}
	args := []string{"s3api", "put-bucket-versioning", "--bucket", ol.Bucket, "--versioning-configuration", "Status=Enabled"}
	args = append(args, awsCommonArgs(ol)...)
	if _, err := execx.Run(ctx, "", nil, "aws", args...); err != nil {
		return fmt.Errorf("enable S3 versioning: %w", err)
	}

	payload := struct {
		ObjectLockEnabled string `json:"ObjectLockEnabled"`
		Rule              struct {
			DefaultRetention struct {
				Mode string `json:"Mode"`
				Days int    `json:"Days"`
			} `json:"DefaultRetention"`
		} `json:"Rule"`
	}{ObjectLockEnabled: "Enabled"}
	payload.Rule.DefaultRetention.Mode = mode
	payload.Rule.DefaultRetention.Days = ol.MinRetentionDays
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "repoark-object-lock-*.json")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args = []string{"s3api", "put-object-lock-configuration", "--bucket", ol.Bucket, "--object-lock-configuration", "file://" + path}
	args = append(args, awsCommonArgs(ol)...)
	if _, err := execx.Run(ctx, "", nil, "aws", args...); err != nil {
		return fmt.Errorf("configure S3 Object Lock: %w", err)
	}
	return VerifyObjectLock(ctx, cfg)
}
