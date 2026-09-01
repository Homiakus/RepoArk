package gitlab

import (
	"os"
	"strings"
	"testing"

	"github.com/Homiakus/repoark/internal/config"
)

func TestComposePinsImageAndMatchesCustomPorts(t *testing.T) {
	cfg := config.Default()
	cfg.GitLab.Enabled = true
	cfg.GitLab.URL = "http://git.example.test:8929"
	cfg.GitLab.Hostname = "git.example.test"
	cfg.GitLab.HTTPPort = 8929
	cfg.GitLab.HTTPSPort = 8443
	cfg.GitLab.SSHPort = 2424
	got := New(cfg).Compose()
	for _, want := range []string{
		cfg.GitLab.Image,
		`external_url "http://git.example.test:8929"`,
		`gitlab_rails['gitlab_shell_ssh_port'] = 2424`,
		`"8929:8929"`,
		`"2424:22"`,
		`"./config:/etc/gitlab"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compose missing %q:\n%s", want, got)
		}
	}
}

func TestBackupMetaIncludesArchiveChecksum(t *testing.T) {
	cfg := config.Default()
	cfg.GitLab.DataDir = t.TempDir()
	archive := cfg.GitLab.DataDir + "/backup.tar.gz"
	if err := os.WriteFile(archive, []byte("gitlab backup payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(cfg)
	if err := m.writeBackupMeta(archive); err != nil {
		t.Fatal(err)
	}
	meta, err := readBackupMeta(archive + ".meta.json")
	if err != nil {
		t.Fatal(err)
	}
	if meta.SHA256 == "" {
		t.Fatal("missing SHA256")
	}
	got, err := gitlabFileSHA256(archive)
	if err != nil {
		t.Fatal(err)
	}
	if got != meta.SHA256 {
		t.Fatalf("checksum mismatch: %s != %s", got, meta.SHA256)
	}
	if _, err := os.Stat(archive + ".sha256"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteBackupCommandExportsRestrictedContainerFilesViaDockerCP(t *testing.T) {
	cmd := remoteBackupCommand("repoark-gitlab", "/tmp/repoark backup.tar.gz", "/tmp/repoark stage")
	for _, want := range []string{
		"docker exec repoark-gitlab gitlab-backup create",
		"docker cp repoark-gitlab:/etc/gitlab/.",
		"docker cp repoark-gitlab:/var/opt/gitlab/backups/.",
		"chmod 600 '/tmp/repoark backup.tar.gz'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("remote backup command missing %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "cd ~/repoark-gitlab && tar") {
		t.Fatalf("remote backup command still archives bind mounts directly:\n%s", cmd)
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote("a'b c")
	want := `'a'"'"'b c'`
	if got != want {
		t.Fatalf("shellQuote = %q, want %q", got, want)
	}
}
