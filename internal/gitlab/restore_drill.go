package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/state"
)

type RestoreDrillReport struct {
	Archive  string
	WorkDir  string
	Image    string
	BackupID string
	Duration time.Duration
	Healthy  bool
}

// RestoreDrill performs a destructive GitLab restore only inside a disposable
// container and isolated bind-mount directory. Production GitLab containers or
// volumes are never targeted by this method.
func (m *Manager) RestoreDrill(ctx context.Context, archive string, emit func(string)) (report RestoreDrillReport, err error) {
	if emit == nil {
		emit = func(string) {}
	}
	g := m.Config.GitLab
	d := g.RestoreDrill
	operationStarted := time.Now().UTC()
	defer func() {
		rec := state.Record{Kind: "gitlab-restore-drill", OK: err == nil && report.Healthy, StartedAt: operationStarted, EndedAt: time.Now().UTC(), Data: map[string]any{"archive": report.Archive, "backup_id": report.BackupID, "image": report.Image}}
		if err != nil {
			rec.Detail = err.Error()
		}
		_ = state.Write(filepath.Join(g.DataDir, "state", "restore-drill.json"), rec)
	}()
	if !execx.Exists("docker") {
		return report, errors.New("docker is required for GitLab restore drill")
	}
	if !execx.Exists("tar") {
		return report, errors.New("tar is required for GitLab restore drill")
	}
	if archive == "" {
		archive, err = latestGitLabArchive(filepath.Join(g.DataDir, "exports"))
		if err != nil {
			return report, err
		}
	}
	if _, err := os.Stat(archive); err != nil {
		return report, err
	}
	if meta, metaErr := readBackupMeta(archive + ".meta.json"); metaErr == nil {
		if meta.Image != "" && meta.Image != g.Image {
			return report, fmt.Errorf("GitLab restore drill image mismatch: backup=%s configured=%s", meta.Image, g.Image)
		}
		if meta.SHA256 != "" {
			sum, sumErr := gitlabFileSHA256(archive)
			if sumErr != nil {
				return report, sumErr
			}
			if !strings.EqualFold(sum, meta.SHA256) {
				return report, errors.New("GitLab backup archive checksum mismatch")
			}
		}
	}
	if err := os.MkdirAll(d.WorkDir, 0o700); err != nil {
		return report, err
	}
	work, err := os.MkdirTemp(d.WorkDir, "repoark-gitlab-drill-*")
	if err != nil {
		return report, err
	}
	report = RestoreDrillReport{Archive: archive, WorkDir: work, Image: g.Image}
	started := time.Now()
	container := "repoark-gitlab-drill-" + fmt.Sprintf("%d", started.UnixNano())
	compose := filepath.Join(work, "compose.yml")
	cleanup := func(success bool) {
		_, _ = execx.Run(context.Background(), "", nil, "docker", "compose", "-f", compose, "down", "--remove-orphans")
		if success || !d.KeepOnFailure {
			_ = os.RemoveAll(work)
		}
	}
	defer func() { cleanup(err == nil) }()

	emit("extracting GitLab application backup + configuration")
	if _, err = execx.Run(ctx, "", nil, "tar", "-xzf", archive, "-C", work); err != nil {
		return report, err
	}
	backupDir := filepath.Join(work, "data", "backups")
	backupFile, err := latestBackupTar(backupDir)
	if err != nil {
		return report, err
	}
	id := strings.TrimSuffix(filepath.Base(backupFile), "_gitlab_backup.tar")
	if id == filepath.Base(backupFile) {
		return report, fmt.Errorf("unexpected GitLab backup filename %s", filepath.Base(backupFile))
	}
	report.BackupID = id
	if err := os.MkdirAll(filepath.Join(work, "logs"), 0o700); err != nil {
		return report, err
	}
	composeBody := fmt.Sprintf(`services:
  gitlab:
    image: %s
    container_name: %s
    hostname: gitlab-drill.local
    shm_size: "256m"
    environment:
      GITLAB_OMNIBUS_CONFIG: |
        external_url 'http://gitlab-drill.local'
        gitlab_rails['gitlab_shell_ssh_port'] = %d
    ports:
      - "%d:80"
      - "%d:22"
    volumes:
      - %q
      - %q
      - %q
`, g.Image, container, d.SSHPort, d.HTTPPort, d.SSHPort,
		filepath.ToSlash(filepath.Join(work, "config"))+":/etc/gitlab", filepath.ToSlash(filepath.Join(work, "logs"))+":/var/log/gitlab", filepath.ToSlash(filepath.Join(work, "data"))+":/var/opt/gitlab")
	if err := os.WriteFile(compose, []byte(composeBody), 0o600); err != nil {
		return report, err
	}

	emit("starting disposable GitLab with exact configured image")
	if _, err = execx.Run(ctx, "", nil, "docker", "compose", "-f", compose, "up", "-d"); err != nil {
		return report, err
	}
	timeout, _ := time.ParseDuration(d.Timeout)
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err = waitGitLabHTTP(waitCtx, d.HTTPPort); err != nil {
		return report, err
	}

	emit("preparing backup ownership and stopping database clients")
	if _, err = execx.Run(ctx, "", nil, "docker", "exec", container, "sh", "-lc", "chown git:git /var/opt/gitlab/backups/*_gitlab_backup.tar"); err != nil {
		return report, err
	}
	if _, err = execx.Run(ctx, "", nil, "docker", "exec", container, "gitlab-ctl", "stop", "puma"); err != nil {
		return report, err
	}
	if _, err = execx.Run(ctx, "", nil, "docker", "exec", container, "gitlab-ctl", "stop", "sidekiq"); err != nil {
		return report, err
	}

	emit("restoring GitLab application backup in disposable instance")
	env := []string{"GITLAB_ASSUME_YES=1"}
	if _, err = execx.Run(ctx, "", env, "docker", "exec", "-e", "GITLAB_ASSUME_YES=1", container, "gitlab-backup", "restore", "BACKUP="+id); err != nil {
		return report, err
	}
	emit("restarting disposable GitLab")
	if _, err = execx.Run(ctx, "", nil, "docker", "restart", container); err != nil {
		return report, err
	}
	waitCtx2, cancel2 := context.WithTimeout(ctx, timeout)
	defer cancel2()
	if err = waitGitLabHTTP(waitCtx2, d.HTTPPort); err != nil {
		return report, err
	}
	emit("running gitlab:check SANITIZE=true")
	if _, err = execx.Run(ctx, "", nil, "docker", "exec", container, "gitlab-rake", "gitlab:check", "SANITIZE=true"); err != nil {
		return report, err
	}
	report.Healthy = true
	report.Duration = time.Since(started)
	emit("GitLab restore drill passed")
	return report, nil
}

func waitGitLabHTTP(ctx context.Context, port int) error {
	client := &http.Client{Timeout: 5 * time.Second}
	// Monitoring endpoints such as /-/health are allowlisted to localhost by
	// default. A request through a Docker-published port arrives from the bridge
	// address and can therefore return 404 even when GitLab is fully healthy.
	// The sign-in page is an external user-facing readiness signal and works
	// through the same network path that operators and restore clients use.
	url := fmt.Sprintf("http://127.0.0.1:%d/users/sign_in", port)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("GitLab health wait: %w", ctx.Err())
		case <-t.C:
		}
	}
}

func latestGitLabArchive(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type item struct {
		path string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "repoark-gitlab-") || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(items) == 0 {
		return "", errors.New("no RepoArk GitLab backup archive found")
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	return items[0].path, nil
}

func latestBackupTar(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_gitlab_backup.tar") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", errors.New("archive contains no *_gitlab_backup.tar")
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

func readBackupMeta(path string) (BackupMeta, error) {
	var meta BackupMeta
	b, err := os.ReadFile(path)
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}
