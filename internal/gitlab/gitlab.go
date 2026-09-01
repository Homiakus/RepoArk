package gitlab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/manifest"
)

type Manager struct{ Config config.Config }

type Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	FullPath string `json:"full_path"`
}

type Project struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	PathWithNamespace string `json:"path_with_namespace"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	WebURL            string `json:"web_url"`
}

type Event struct {
	Repo, Stage, Message string
	Done, Total          int
	Err                  error
}

func New(cfg config.Config) *Manager { return &Manager{Config: cfg} }

func (m *Manager) Compose() string {
	g := m.Config.GitLab
	external := g.URL
	if external == "" {
		external = fmt.Sprintf("http://%s:%d", g.Hostname, g.HTTPPort)
	}
	return fmt.Sprintf(`services:
  gitlab:
    image: %s
    container_name: %s
    restart: unless-stopped
    hostname: %q
    shm_size: "256m"
    environment:
      GITLAB_OMNIBUS_CONFIG: |
        external_url %q
        gitlab_rails['gitlab_shell_ssh_port'] = %d
    ports:
      - %q
      - %q
      - %q
    volumes:
      - %q
      - %q
      - %q
`, g.Image, g.Container, g.Hostname, external, g.SSHPort,
		fmt.Sprintf("%d:%d", g.HTTPPort, g.HTTPPort), fmt.Sprintf("%d:%d", g.HTTPSPort, g.HTTPSPort), fmt.Sprintf("%d:22", g.SSHPort),
		"./config:/etc/gitlab",
		"./logs:/var/log/gitlab",
		"./data:/var/opt/gitlab")
}

func (m *Manager) WriteCompose() (string, error) {
	g := m.Config.GitLab
	if err := os.MkdirAll(g.DataDir, 0o700); err != nil {
		return "", err
	}
	for _, d := range []string{"config", "logs", "data", "exports"} {
		if err := os.MkdirAll(filepath.Join(g.DataDir, d), 0o700); err != nil {
			return "", err
		}
	}
	path := filepath.Join(g.DataDir, "compose.yml")
	if err := os.WriteFile(path, []byte(m.Compose()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) Deploy(ctx context.Context, remote string) error {
	path, err := m.WriteCompose()
	if err != nil {
		return err
	}
	if remote == "" {
		remote = m.Config.GitLab.RemoteHost
	}
	if remote == "" {
		if !execx.Exists("docker") {
			return errors.New("docker not found; install Docker Engine/Desktop first")
		}
		if _, err := execx.Run(ctx, "", nil, "docker", "compose", "version"); err != nil {
			return fmt.Errorf("docker compose unavailable: %w", err)
		}
		_, err = execx.Run(ctx, "", nil, "docker", "compose", "-f", path, "up", "-d")
		return err
	}
	if !execx.Exists("ssh") || !execx.Exists("scp") {
		return errors.New("remote deploy requires ssh and scp")
	}
	remoteDir := "~/repoark-gitlab"
	if _, err := execx.Run(ctx, "", nil, "ssh", remote, "mkdir -p "+remoteDir); err != nil {
		return err
	}
	if _, err := execx.Run(ctx, "", nil, "scp", path, remote+":"+remoteDir+"/compose.yml"); err != nil {
		return err
	}
	_, err = execx.Run(ctx, "", nil, "ssh", remote, "docker compose -f "+remoteDir+"/compose.yml up -d")
	return err
}

func (m *Manager) InitialPassword(ctx context.Context, remote string) (string, error) {
	cmd := fmt.Sprintf("docker exec %s sh -lc \"grep '^Password:' /etc/gitlab/initial_root_password 2>/dev/null || true\"", shellSafe(m.Config.GitLab.Container))
	if remote == "" {
		remote = m.Config.GitLab.RemoteHost
	}
	if remote != "" {
		res, err := execx.Run(ctx, "", nil, "ssh", remote, cmd)
		return res.Stdout, err
	}
	res, err := execx.Run(ctx, "", nil, "docker", "exec", m.Config.GitLab.Container, "sh", "-lc", "grep '^Password:' /etc/gitlab/initial_root_password 2>/dev/null || true")
	return res.Stdout, err
}

func (m *Manager) Backup(ctx context.Context, remote string) (string, error) {
	g := m.Config.GitLab
	stamp := time.Now().UTC().Format("20060102T150405Z")
	if remote == "" {
		remote = g.RemoteHost
	}
	if remote != "" {
		remoteArchive := "/tmp/repoark-gitlab-" + stamp + ".tar.gz"
		remoteStage := "/tmp/repoark-gitlab-export-" + stamp
		backupCmd := remoteBackupCommand(g.Container, remoteArchive, remoteStage)
		if _, err := execx.Run(ctx, "", nil, "ssh", remote, backupCmd); err != nil {
			return "", err
		}
		outDir := filepath.Join(g.DataDir, "exports")
		if err := os.MkdirAll(outDir, 0o700); err != nil {
			return "", err
		}
		local := filepath.Join(outDir, filepath.Base(remoteArchive))
		if _, err := execx.Run(ctx, "", nil, "scp", remote+":"+remoteArchive, local); err != nil {
			return "", err
		}
		_, _ = execx.Run(ctx, "", nil, "ssh", remote, "rm -f "+shellQuote(remoteArchive))
		if err := os.Chmod(local, 0o600); err != nil {
			return "", err
		}
		if err := m.writeBackupMeta(local); err != nil {
			return "", err
		}
		return local, nil
	}
	if !execx.Exists("docker") {
		return "", errors.New("docker is required for GitLab backup")
	}
	if !execx.Exists("tar") {
		return "", errors.New("tar executable is required for GitLab config export")
	}
	if _, err := execx.Run(ctx, "", nil, "docker", "exec", g.Container, "gitlab-backup", "create"); err != nil {
		return "", err
	}
	outDir := filepath.Join(g.DataDir, "exports")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(outDir, ".repoark-gitlab-export-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	configDir := filepath.Join(stage, "config")
	backupDir := filepath.Join(stage, "data", "backups")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	// GitLab intentionally stores secrets and application backups with
	// restrictive ownership/modes inside the container. Reading the bind mounts
	// directly as the RepoArk host user is therefore unreliable. docker cp asks
	// the Docker daemon to export the files and materializes them as the invoking
	// host user, preserving the archive layout without weakening source modes.
	if _, err := execx.Run(ctx, "", nil, "docker", "cp", g.Container+":/etc/gitlab/.", configDir); err != nil {
		return "", err
	}
	if _, err := execx.Run(ctx, "", nil, "docker", "cp", g.Container+":/var/opt/gitlab/backups/.", backupDir); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, "repoark-gitlab-"+stamp+".tar.gz")
	if _, err := execx.Run(ctx, stage, nil, "tar", "-czf", out, "config", "data/backups"); err != nil {
		return out, err
	}
	if err := os.Chmod(out, 0o600); err != nil {
		return out, err
	}
	if err := m.writeBackupMeta(out); err != nil {
		return "", err
	}
	return out, nil
}

func remoteBackupCommand(container, archive, stage string) string {
	container = shellSafe(container)
	qArchive := shellQuote(archive)
	qStage := shellQuote(stage)
	return fmt.Sprintf(
		"set -eu; rm -rf %s; mkdir -p %s/config %s/data/backups; trap 'rm -rf %s' EXIT; "+
			"docker exec %s gitlab-backup create; "+
			"docker cp %s:/etc/gitlab/. %s/config; "+
			"docker cp %s:/var/opt/gitlab/backups/. %s/data/backups; "+
			"if [ -f ~/repoark-gitlab/compose.yml ]; then cp ~/repoark-gitlab/compose.yml %s/compose.yml; fi; "+
			"if [ -f %s/compose.yml ]; then tar -C %s -czf %s compose.yml config data/backups; else tar -C %s -czf %s config data/backups; fi; "+
			"chmod 600 %s",
		qStage, qStage, qStage, qStage,
		container,
		container, qStage,
		container, qStage,
		qStage,
		qStage, qStage, qArchive, qStage, qArchive,
		qArchive,
	)
}

type BackupMeta struct {
	CreatedAt time.Time `json:"created_at"`
	Image     string    `json:"image"`
	Edition   string    `json:"edition"`
	Archive   string    `json:"archive"`
	SHA256    string    `json:"sha256"`
}

func (m *Manager) writeBackupMeta(archive string) error {
	sum, err := gitlabFileSHA256(archive)
	if err != nil {
		return err
	}
	meta := BackupMeta{CreatedAt: time.Now().UTC(), Image: m.Config.GitLab.Image, Edition: "CE", Archive: filepath.Base(archive), SHA256: sum}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(archive+".meta.json", b, 0o600); err != nil {
		return err
	}
	return os.WriteFile(archive+".sha256", []byte(sum+"  "+filepath.Base(archive)+"\n"), 0o600)
}

func gitlabFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *Manager) MigrateLatest(ctx context.Context, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}
	token := strings.TrimSpace(os.Getenv(m.Config.GitLab.TokenEnv))
	if token == "" {
		return fmt.Errorf("%s is not set", m.Config.GitLab.TokenEnv)
	}
	man, err := manifest.ReadLatest(m.Config.Backup.Root)
	if err != nil {
		return err
	}
	client := apiClient{base: strings.TrimRight(m.Config.GitLab.URL, "/"), token: token, http: &http.Client{Timeout: 60 * time.Second}}
	results := append([]manifest.RepoResult(nil), man.Repositories...)
	sort.Slice(results, func(i, j int) bool { return results[i].FullName < results[j].FullName })
	for i, r := range results {
		if r.Error != "" {
			continue
		}
		emit(Event{Repo: r.FullName, Stage: "gitlab", Message: "creating/updating project", Done: i, Total: len(results)})
		var project Project
		var err error
		if m.Config.GitLab.PreserveNamespaces {
			project, err = client.ensureNamespacedProject(ctx, r.FullName, r.SourceVisibility)
		} else {
			project, err = client.ensureProject(ctx, r.FullName)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", r.FullName, err)
		}
		pushURL := withUsername(project.HTTPURLToRepo, "oauth2")
		env, _ := execx.AskPassEnv(token, "oauth2")
		mirrorPath := artifactPath(m.Config.Backup.Root, r.MirrorPath)
		if _, err := execx.Run(ctx, mirrorPath, env, "git", "push", "--mirror", pushURL); err != nil {
			return fmt.Errorf("%s: %w", r.FullName, err)
		}
		if r.LFSArchivePath != "" {
			if !execx.Exists("git-lfs") {
				return fmt.Errorf("%s: git-lfs is required to migrate backed-up LFS objects", r.FullName)
			}
			if _, err := os.Stat(filepath.Join(mirrorPath, "lfs")); err == nil {
				emit(Event{Repo: r.FullName, Stage: "gitlab-lfs", Message: "pushing LFS objects", Done: i, Total: len(results)})
				if _, err := execx.Run(ctx, mirrorPath, env, "git", "lfs", "push", "--all", pushURL); err != nil {
					return fmt.Errorf("%s LFS: %w", r.FullName, err)
				}
			}
		}
		emit(Event{Repo: r.FullName, Stage: "gitlab", Message: "migrated", Done: i + 1, Total: len(results)})
	}
	return nil
}

type apiClient struct {
	base, token string
	http        *http.Client
}

func (c apiClient) ensureNamespacedProject(ctx context.Context, fullName, sourceVisibility string) (Project, error) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok || strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return Project{}, fmt.Errorf("invalid repository name %q", fullName)
	}
	groupPath := gitlabPath(owner)
	projectPath := gitlabPath(repo)
	g, err := c.ensureGroup(ctx, groupPath, owner)
	if err != nil {
		return Project{}, err
	}
	fullPath := groupPath + "/" + projectPath
	if p, ok, err := c.findProjectByFullPath(ctx, fullPath); err != nil {
		return Project{}, err
	} else if ok {
		return p, nil
	}
	visibility := strings.ToLower(strings.TrimSpace(sourceVisibility))
	if visibility != "public" && visibility != "internal" {
		visibility = "private"
	}
	body, _ := json.Marshal(map[string]any{
		"name": repo, "path": projectPath, "namespace_id": g.ID,
		"visibility":  visibility,
		"description": "Mirror restored by RepoArk from GitHub repository " + fullName,
	})
	var p Project
	if err := c.doJSON(ctx, http.MethodPost, "/api/v4/projects", body, &p, 201); err != nil {
		return p, err
	}
	return p, nil
}

func (c apiClient) ensureGroup(ctx context.Context, path, displayName string) (Group, error) {
	var g Group
	encoded := url.PathEscape(path)
	if err := c.doJSON(ctx, http.MethodGet, "/api/v4/groups/"+encoded, nil, &g, 200); err == nil {
		return g, nil
	}
	body, _ := json.Marshal(map[string]any{"name": displayName, "path": path, "visibility": "private"})
	if err := c.doJSON(ctx, http.MethodPost, "/api/v4/groups", body, &g, 201); err != nil {
		return g, err
	}
	return g, nil
}

func (c apiClient) findProjectByFullPath(ctx context.Context, fullPath string) (Project, bool, error) {
	var p Project
	err := c.doJSON(ctx, http.MethodGet, "/api/v4/projects/"+url.PathEscape(fullPath), nil, &p, 200)
	if err == nil {
		return p, true, nil
	}
	// GET by full path returns 404 when absent. Avoid coupling doJSON to a
	// special status type by falling back to search only for this lookup.
	q := url.Values{"search": []string{filepath.Base(fullPath)}, "simple": []string{"true"}, "per_page": []string{"100"}}
	var list []Project
	if err2 := c.doJSON(ctx, http.MethodGet, "/api/v4/projects?"+q.Encode(), nil, &list, 200); err2 != nil {
		return p, false, err
	}
	for _, candidate := range list {
		if strings.EqualFold(candidate.PathWithNamespace, fullPath) {
			return candidate, true, nil
		}
	}
	return p, false, nil
}

func gitlabPath(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "repoark"
	}
	return out
}

func (c apiClient) ensureProject(ctx context.Context, fullName string) (Project, error) {
	path := strings.ReplaceAll(fullName, "/", "--")
	if p, ok, err := c.findProject(ctx, path); err != nil {
		return Project{}, err
	} else if ok {
		return p, nil
	}
	body, _ := json.Marshal(map[string]any{
		"name":        strings.ReplaceAll(fullName, "/", " · "),
		"path":        path,
		"visibility":  "private",
		"description": "Mirror restored by RepoArk from GitHub repository " + fullName,
	})
	var p Project
	if err := c.doJSON(ctx, http.MethodPost, "/api/v4/projects", body, &p, 201); err != nil {
		return p, err
	}
	return p, nil
}

func (c apiClient) findProject(ctx context.Context, path string) (Project, bool, error) {
	var p Project
	q := url.Values{"search": []string{path}, "simple": []string{"true"}, "per_page": []string{"100"}}
	var list []Project
	if err := c.doJSON(ctx, http.MethodGet, "/api/v4/projects?"+q.Encode(), nil, &list, 200); err != nil {
		return p, false, err
	}
	for _, candidate := range list {
		if candidate.Path == path {
			return candidate, true, nil
		}
	}
	return p, false, nil
}

func (c apiClient) doJSON(ctx context.Context, method, path string, body []byte, dst any, want int) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != want {
		return fmt.Errorf("GitLab API %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if dst != nil {
		return json.Unmarshal(b, dst)
	}
	return nil
}

func artifactPath(root, stored string) string {
	if filepath.IsAbs(stored) {
		return stored
	}
	return filepath.Join(root, filepath.FromSlash(stored))
}

func withUsername(rawURL, username string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = url.User(username)
	return u.String()
}

func shellSafe(s string) string {
	// Container names generated by RepoArk contain only conservative characters.
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.", r) {
			return r
		}
		return -1
	}, s)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func ParsePort(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
