package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/githubapi"
	"github.com/Homiakus/repoark/internal/kmsattest"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/state"
)

type Event struct {
	Time    time.Time
	Repo    string
	Stage   string
	Message string
	Done    int
	Total   int
	Err     error
}

type Manager struct {
	Config config.Config
	GitHub *githubapi.Client
	Token  string
	User   string
}

func New(cfg config.Config) *Manager {
	token := githubapi.ResolveToken(cfg.GitHub.TokenEnv)
	client := githubapi.New(cfg.GitHub.APIURL, token)
	client.GraphQLURL = cfg.GitHub.GraphQLURL
	client.MaxPages = cfg.GitHub.MaxMetadataPages
	return &Manager{Config: cfg, GitHub: client, Token: token}
}

func (m *Manager) Run(ctx context.Context, emit func(Event)) (manifest.Manifest, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	if !execx.Exists("git") {
		return manifest.Manifest{}, errors.New("git executable not found")
	}
	if err := os.MkdirAll(m.Config.Backup.Root, 0o700); err != nil {
		return manifest.Manifest{}, err
	}

	user, err := m.GitHub.User(ctx)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("GitHub authentication failed: %w", err)
	}
	m.User = user.Login
	repos, err := m.GitHub.Repositories(ctx)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("list repositories: %w", err)
	}

	filtered := repos[:0]
	for _, r := range repos {
		if r.Fork && !m.Config.GitHub.IncludeForks {
			continue
		}
		if r.Archived && !m.Config.GitHub.IncludeArchived {
			continue
		}
		filtered = append(filtered, r)
	}
	repos = filtered

	man := manifest.Manifest{Version: 4, StartedAt: time.Now().UTC(), GitHubUser: user.Login}
	emit(Event{Time: time.Now(), Stage: "discover", Message: fmt.Sprintf("found %d repositories for %s", len(repos), user.Login), Total: len(repos)})

	jobs := make(chan githubapi.Repository)
	results := make(chan manifest.RepoResult, len(repos))
	workers := m.Config.Backup.Concurrency
	if workers > len(repos) && len(repos) > 0 {
		workers = len(repos)
	}
	if workers == 0 {
		workers = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range jobs {
				results <- m.backupRepo(ctx, repo, emit)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, repo := range repos {
			select {
			case jobs <- repo:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	done := 0
	for r := range results {
		done++
		man.Repositories = append(man.Repositories, r)
		if r.Error == "" {
			man.Succeeded++
		} else {
			man.Failed++
		}
		emit(Event{Time: time.Now(), Repo: r.FullName, Stage: "complete", Message: statusMessage(r), Done: done, Total: len(repos)})
	}
	if m.Config.GitHub.ProjectsV2 {
		owners := map[string]struct{}{user.Login: {}}
		for _, repo := range repos {
			if owner, _, ok := strings.Cut(repo.FullName, "/"); ok {
				owners[owner] = struct{}{}
			}
		}
		for owner := range owners {
			emit(Event{Time: time.Now(), Stage: "projects", Message: "saving Projects v2 for " + owner})
			if err := m.backupProjectsV2(ctx, owner); err != nil {
				man.Warnings = append(man.Warnings, "Projects v2 "+owner+": "+err.Error())
			} else {
				man.ProjectsV2OwnersBackedUp++
			}
		}
	}
	if m.Config.CAS.Enabled && m.Config.CAS.AutoCompact {
		emit(Event{Time: time.Now(), Stage: "cas", Message: "deduplicating immutable payloads"})
		store := cas.New(m.Config.CAS.Root, m.Config.CAS.MinFileSize)
		paths := []string{
			filepath.Join(m.Config.Backup.Root, "release-assets"),
			filepath.Join(m.Config.Backup.Root, "actions-artifacts"),
			filepath.Join(m.Config.Backup.Root, "oci"),
			filepath.Join(m.Config.Backup.Root, "packages"),
			filepath.Join(m.Config.Backup.Root, "official-exports"),
			filepath.Join(m.Config.Backup.Root, "lfs"),
			filepath.Join(m.Config.Backup.Root, "bundles"),
		}
		st, err := store.Compact(paths)
		if err != nil {
			man.Warnings = append(man.Warnings, "CAS compaction: "+err.Error())
		} else {
			man.CAS = manifest.CASStats{Objects: st.Objects, PhysicalBytes: st.PhysicalBytes, LogicalFiles: st.LogicalFiles, LogicalBytes: st.LogicalBytes, ReclaimedBytes: st.Reclaimed}
		}
	}
	man.EndedAt = time.Now().UTC()
	man.WarningCount += len(man.Warnings)
	for _, r := range man.Repositories {
		man.WarningCount += len(r.Warnings)
	}
	var writeErr error
	var manifestPath string
	if m.Config.Security.SignManifests {
		manifestPath, writeErr = manifest.WriteSigned(m.Config.Backup.Root, man, m.Config.Security.SigningKeyPath)
	} else {
		manifestPath, writeErr = manifest.Write(m.Config.Backup.Root, man)
	}
	_ = manifest.Prune(m.Config.Backup.Root, m.Config.Backup.KeepManifests)
	if writeErr != nil {
		return man, writeErr
	}
	if m.Config.Security.KMSAttestation.Enabled {
		latest := filepath.Join(m.Config.Backup.Root, "manifests", "latest.json")
		if _, err := kmsattest.SignFile(ctx, latest, m.Config.Security.KMSAttestation); err != nil {
			if m.Config.Security.KMSAttestation.RequireValid {
				return man, fmt.Errorf("KMS manifest attestation: %w", err)
			}
			emit(Event{Time: time.Now(), Stage: "kms", Message: "KMS attestation warning: " + err.Error(), Err: err})
		} else if manifestPath != "" {
			_, _ = kmsattest.SignFile(ctx, manifestPath, m.Config.Security.KMSAttestation)
		}
	}
	if ctx.Err() != nil {
		return man, ctx.Err()
	}
	if man.Failed > 0 {
		return man, fmt.Errorf("%d of %d repositories failed", man.Failed, len(repos))
	}
	return man, nil
}

func statusMessage(r manifest.RepoResult) string {
	if r.Error != "" {
		return "failed: " + r.Error
	}
	msg := "backup complete"
	if r.Verified {
		msg += ", verified"
	}
	if r.LFSFetched {
		msg += ", LFS"
	}
	if r.WikiBackedUp {
		msg += ", wiki"
	}
	if r.ReleaseAssetsBackedUp > 0 {
		msg += fmt.Sprintf(", %d assets", r.ReleaseAssetsBackedUp)
	}
	if r.DiscussionsBackedUp {
		msg += ", discussions"
	}
	if r.PackagesBackedUp > 0 {
		msg += fmt.Sprintf(", %d packages", r.PackagesBackedUp)
	}
	if r.PackagePayloadsBackedUp > 0 {
		msg += fmt.Sprintf(", %d package payloads", r.PackagePayloadsBackedUp)
	}
	if r.ActionArtifactsBackedUp > 0 {
		msg += fmt.Sprintf(", %d action artifacts", r.ActionArtifactsBackedUp)
	}
	if len(r.Warnings) > 0 {
		msg += fmt.Sprintf(", %d warnings", len(r.Warnings))
	}
	return msg
}

func (m *Manager) backupRepo(ctx context.Context, repo githubapi.Repository, emit func(Event)) manifest.RepoResult {
	start := time.Now()
	res := manifest.RepoResult{FullName: repo.FullName, SourceVisibility: repo.Visibility, UpdatedAt: repo.UpdatedAt}
	owner, name := splitRepo(repo.FullName)
	mirrorRel := filepath.Join("mirrors", owner, name+".git")
	mirror := filepath.Join(m.Config.Backup.Root, mirrorRel)
	res.MirrorPath = filepath.ToSlash(mirrorRel)
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		res.Error = err.Error()
		return res
	}

	cloneURL := repo.CloneURL
	env := []string(nil)
	if m.Config.GitHub.CloneProtocol == "ssh" {
		cloneURL = repo.SSHURL
	} else if m.Token != "" {
		cloneURL = withUsername(repo.CloneURL, "x-access-token")
		env, _ = execx.AskPassEnv(m.Token, "x-access-token")
	}

	emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "git", Message: "syncing mirror"})
	if _, err := os.Stat(mirror); errors.Is(err, os.ErrNotExist) {
		if _, err := execx.Run(ctx, "", env, "git", "clone", "--mirror", cloneURL, mirror); err != nil {
			res.Error = err.Error()
			return res
		}
	} else {
		if _, err := execx.Run(ctx, mirror, env, "git", "remote", "set-url", "origin", cloneURL); err != nil {
			res.Error = err.Error()
			return res
		}
		if _, err := execx.Run(ctx, mirror, env, "git", "remote", "update", "--prune"); err != nil {
			res.Error = err.Error()
			return res
		}
	}

	if m.Config.Backup.FetchLFS && execx.Exists("git-lfs") {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "lfs", Message: "fetching LFS objects"})
		if _, err := execx.Run(ctx, mirror, env, "git", "lfs", "fetch", "--all", "origin"); err == nil {
			res.LFSFetched = true
		}
	}
	if res.LFSFetched {
		lfsDir := filepath.Join(mirror, "lfs")
		if info, err := os.Stat(lfsDir); err == nil && info.IsDir() {
			lfsRel := filepath.Join("lfs", owner, name+".lfs.tar.gz")
			lfsArchive := filepath.Join(m.Config.Backup.Root, lfsRel)
			if err := os.MkdirAll(filepath.Dir(lfsArchive), 0o700); err != nil {
				res.Error = err.Error()
				return res
			}
			emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "lfs", Message: "archiving LFS object store"})
			if err := archiveDirGzip(lfsDir, lfsArchive); err != nil {
				res.Error = err.Error()
				return res
			}
			res.LFSArchivePath = filepath.ToSlash(lfsRel)
			sum, err := sha256File(lfsArchive)
			if err != nil {
				res.Error = err.Error()
				return res
			}
			res.LFSArchiveSHA256 = sum
			_ = os.WriteFile(lfsArchive+".sha256", []byte(sum+"  "+filepath.Base(lfsArchive)+"\n"), 0o600)
		}
	}

	if repo.HasWiki {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "wiki", Message: "syncing wiki"})
		if m.backupWiki(ctx, repo, owner, name, env) == nil {
			res.WikiBackedUp = true
		}
	}

	if m.Config.GitHub.Metadata != "none" {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "metadata", Message: "saving GitHub metadata"})
		warnings, err := m.backupMetadata(ctx, repo, owner, name)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.Warnings = append(res.Warnings, warnings...)
		res.MetadataBackedUp = true
	}

	if m.Config.GitHub.ReleaseAssets {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "assets", Message: "saving release assets"})
		n, warnings := m.backupReleaseAssets(ctx, repo, owner, name)
		res.ReleaseAssetsBackedUp = n
		res.Warnings = append(res.Warnings, warnings...)
	}

	if m.Config.GitHub.Discussions && repo.HasDiscussions {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "discuss", Message: "saving GitHub Discussions"})
		truncated, err := m.backupDiscussions(ctx, repo, owner, name)
		if err != nil {
			res.Warnings = append(res.Warnings, "discussions: "+err.Error())
		} else {
			res.DiscussionsBackedUp = true
			if truncated {
				res.Warnings = append(res.Warnings, "discussions: nested comments/replies exceeded bounded capture window and were marked truncated")
			}
		}
	}

	if m.Config.GitHub.Packages {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "packages", Message: "saving GitHub Packages metadata"})
		n, oci, payloads, warnings := m.backupPackages(ctx, repo, owner, name)
		res.PackagesBackedUp = n
		res.OCIArtifactsBackedUp = oci
		res.PackagePayloadsBackedUp = payloads
		res.Warnings = append(res.Warnings, warnings...)
	}

	if m.Config.GitHub.ActionsArtifacts {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "actions", Message: "saving GitHub Actions artifacts"})
		n, warnings := m.backupActionArtifacts(ctx, repo, owner, name)
		res.ActionArtifactsBackedUp = n
		res.Warnings = append(res.Warnings, warnings...)
	}

	if m.Config.Backup.CreateBundles {
		refs, _ := execx.Run(ctx, mirror, nil, "git", "for-each-ref", "--format=%(refname)", "refs/")
		if strings.TrimSpace(refs.Stdout) == "" {
			emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "bundle", Message: "empty repository; bundle skipped"})
		} else {
			bundleRel := filepath.Join("bundles", owner, name+".bundle")
			bundle := filepath.Join(m.Config.Backup.Root, bundleRel)
			if err := os.MkdirAll(filepath.Dir(bundle), 0o700); err != nil {
				res.Error = err.Error()
				return res
			}
			tmp := bundle + ".tmp"
			_ = os.Remove(tmp)
			emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "bundle", Message: "creating portable bundle"})
			if _, err := execx.Run(ctx, mirror, nil, "git", "bundle", "create", tmp, "--all"); err != nil {
				res.Error = err.Error()
				return res
			}
			if err := os.Rename(tmp, bundle); err != nil {
				res.Error = err.Error()
				return res
			}
			res.BundlePath = filepath.ToSlash(bundleRel)
			sum, err := sha256File(bundle)
			if err != nil {
				res.Error = err.Error()
				return res
			}
			res.BundleSHA256 = sum
			_ = os.WriteFile(bundle+".sha256", []byte(sum+"  "+filepath.Base(bundle)+"\n"), 0o600)
		}
	}

	if m.Config.Backup.VerifyAfterBackup {
		emit(Event{Time: time.Now(), Repo: repo.FullName, Stage: "verify", Message: "running git fsck"})
		if _, err := execx.Run(ctx, mirror, nil, "git", "fsck", "--full"); err != nil {
			res.Error = err.Error()
			return res
		}
		if res.BundlePath != "" {
			bundlePath := resolveArtifactPath(m.Config.Backup.Root, res.BundlePath)
			if _, err := execx.Run(ctx, mirror, nil, "git", "bundle", "verify", bundlePath); err != nil {
				res.Error = err.Error()
				return res
			}
		}
		res.Verified = true
	}
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

func (m *Manager) backupWiki(ctx context.Context, repo githubapi.Repository, owner, name string, env []string) error {
	wikiPath := filepath.Join(m.Config.Backup.Root, "wikis", owner, name+".wiki.git")
	if err := os.MkdirAll(filepath.Dir(wikiPath), 0o700); err != nil {
		return err
	}
	wikiURL := strings.TrimSuffix(repo.CloneURL, ".git") + ".wiki.git"
	if m.Config.GitHub.CloneProtocol == "ssh" {
		wikiURL = strings.TrimSuffix(repo.SSHURL, ".git") + ".wiki.git"
	} else if m.Token != "" {
		wikiURL = withUsername(wikiURL, "x-access-token")
	}
	if _, err := os.Stat(wikiPath); errors.Is(err, os.ErrNotExist) {
		_, err = execx.Run(ctx, "", env, "git", "clone", "--mirror", wikiURL, wikiPath)
		return err
	}
	_, err := execx.Run(ctx, wikiPath, env, "git", "remote", "update", "--prune")
	return err
}

func (m *Manager) backupMetadata(ctx context.Context, repo githubapi.Repository, owner, name string) ([]string, error) {
	data, err := m.GitHub.Metadata(ctx, repo, m.Config.GitHub.Metadata)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(m.Config.Backup.Root, "metadata", owner, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var warnings []string
	for key, raw := range data {
		var pretty []byte
		var v any
		if json.Unmarshal(raw, &v) == nil {
			pretty, _ = json.MarshalIndent(v, "", "  ")
			if obj, ok := v.(map[string]any); ok {
				if warning, ok := obj["warning"].(string); ok && warning != "" {
					warnings = append(warnings, key+": "+warning)
				}
			}
		} else {
			pretty = raw
		}
		if err := os.WriteFile(filepath.Join(dir, key+".json"), pretty, 0o600); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

func (m *Manager) backupReleaseAssets(ctx context.Context, repo githubapi.Repository, owner, name string) (int, []string) {
	releases, err := m.GitHub.Releases(ctx, repo.FullName)
	if err != nil {
		return 0, []string{"release assets: " + err.Error()}
	}
	type assetRecord struct {
		ReleaseTag string `json:"release_tag"`
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		Path       string `json:"path,omitempty"`
		SHA256     string `json:"sha256,omitempty"`
		Error      string `json:"error,omitempty"`
	}
	var records []assetRecord
	var warnings []string
	count := 0
	for _, release := range releases {
		tag := fmt.Sprintf("release-%d", release.ID)
		if strings.TrimSpace(release.TagName) != "" {
			tag = safe(release.TagName)
		}
		for _, asset := range release.Assets {
			rel := filepath.Join("release-assets", owner, name, tag, safe(asset.Name))
			dst := filepath.Join(m.Config.Backup.Root, rel)
			rec := assetRecord{ReleaseTag: release.TagName, Name: asset.Name, Size: asset.Size, Path: filepath.ToSlash(rel)}
			if err := m.GitHub.DownloadReleaseAsset(ctx, asset, dst, m.Config.GitHub.MaxAssetBytes); err != nil {
				rec.Error = err.Error()
				warnings = append(warnings, fmt.Sprintf("release asset %s/%s: %v", release.TagName, asset.Name, err))
				records = append(records, rec)
				continue
			}
			sum, err := sha256File(dst)
			if err != nil {
				rec.Error = err.Error()
				warnings = append(warnings, fmt.Sprintf("release asset checksum %s/%s: %v", release.TagName, asset.Name, err))
			} else {
				rec.SHA256 = sum
				_ = os.WriteFile(dst+".sha256", []byte(sum+"  "+filepath.Base(dst)+"\n"), 0o600)
				count++
			}
			records = append(records, rec)
		}
	}
	if len(records) > 0 {
		dir := filepath.Join(m.Config.Backup.Root, "metadata", owner, name)
		if err := os.MkdirAll(dir, 0o700); err == nil {
			if b, err := json.MarshalIndent(records, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "release_assets.json"), b, 0o600)
			}
		}
	}
	return count, warnings
}

func (m *Manager) backupDiscussions(ctx context.Context, repo githubapi.Repository, owner, name string) (bool, error) {
	raw, err := m.GitHub.Discussions(ctx, repo)
	if err != nil {
		return false, err
	}
	dir := filepath.Join(m.Config.Backup.Root, "metadata", owner, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(dir, "discussions.json"), raw, 0o600); err != nil {
		return false, err
	}
	var marker struct {
		NestedCommentsTruncated bool `json:"nested_comments_truncated"`
	}
	_ = json.Unmarshal(raw, &marker)
	return marker.NestedCommentsTruncated, nil
}

func (m *Manager) backupPackages(ctx context.Context, repo githubapi.Repository, owner, name string) (int, int, int, []string) {
	pkgs, apiWarnings, err := m.GitHub.PackagesForRepository(ctx, repo, m.User)
	if err != nil {
		return 0, 0, 0, []string{"packages: " + err.Error()}
	}
	dir := filepath.Join(m.Config.Backup.Root, "metadata", owner, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, 0, 0, []string{"packages metadata: " + err.Error()}
	}
	if b, err := json.MarshalIndent(pkgs, "", "  "); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "packages.json"), b, 0o600); err != nil {
			return 0, 0, 0, []string{"packages metadata: " + err.Error()}
		}
	}
	payloadCount, payloadWarnings := m.backupPackagePayloads(ctx, repo, owner, name, pkgs)
	apiWarnings = append(apiWarnings, payloadWarnings...)
	if !m.Config.GitHub.OCIExport || len(pkgs) == 0 {
		return len(pkgs), 0, payloadCount, apiWarnings
	}
	if !execx.Exists("skopeo") {
		return len(pkgs), 0, payloadCount, append(apiWarnings, "OCI export requested but skopeo is not installed")
	}
	if m.Token == "" {
		return len(pkgs), 0, payloadCount, append(apiWarnings, "OCI export requested but no GitHub token is available")
	}
	authFile, err := m.registryAuthFile(m.User)
	if err != nil {
		return len(pkgs), 0, payloadCount, append(apiWarnings, "OCI auth: "+err.Error())
	}
	defer os.Remove(authFile)
	env := []string{"REGISTRY_AUTH_FILE=" + authFile}
	count := 0
	warnings := append([]string(nil), apiWarnings...)
	type ociRecord struct {
		Image  string `json:"image"`
		Tag    string `json:"tag"`
		Path   string `json:"path,omitempty"`
		SHA256 string `json:"sha256,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	var ociRecords []ociRecord
	for _, pkg := range pkgs {
		if pkg.PackageType != "container" {
			continue
		}
		image := containerImage(owner, pkg.Name)
		list, err := execx.Run(ctx, "", env, "skopeo", "list-tags", "docker://"+image)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("OCI %s tags: %v", image, err))
			continue
		}
		var payload struct {
			Tags []string `json:"Tags"`
		}
		if err := json.Unmarshal([]byte(list.Stdout), &payload); err != nil {
			warnings = append(warnings, fmt.Sprintf("OCI %s tags parse: %v", image, err))
			continue
		}
		for _, tag := range payload.Tags {
			rel := filepath.Join("oci", owner, name, safe(pkg.Name), safe(tag)+".tar")
			dst := filepath.Join(m.Config.Backup.Root, rel)
			rec := ociRecord{Image: image, Tag: tag, Path: filepath.ToSlash(rel)}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				rec.Error = err.Error()
				warnings = append(warnings, fmt.Sprintf("OCI %s:%s: %v", image, tag, err))
				ociRecords = append(ociRecords, rec)
				continue
			}
			tmp := dst + ".tmp"
			_ = os.Remove(tmp)
			if _, err := execx.Run(ctx, "", env, "skopeo", "copy", "--all", "docker://"+image+":"+tag, "oci-archive:"+tmp+":"+tag); err != nil {
				rec.Error = err.Error()
				warnings = append(warnings, fmt.Sprintf("OCI %s:%s: %v", image, tag, err))
				_ = os.Remove(tmp)
				ociRecords = append(ociRecords, rec)
				continue
			}
			if err := os.Rename(tmp, dst); err != nil {
				rec.Error = err.Error()
				warnings = append(warnings, fmt.Sprintf("OCI %s:%s rename: %v", image, tag, err))
				ociRecords = append(ociRecords, rec)
				continue
			}
			sum, err := sha256File(dst)
			if err != nil {
				rec.Error = err.Error()
				warnings = append(warnings, fmt.Sprintf("OCI %s:%s checksum: %v", image, tag, err))
			} else {
				rec.SHA256 = sum
				_ = os.WriteFile(dst+".sha256", []byte(sum+"  "+filepath.Base(dst)+"\n"), 0o600)
				count++
			}
			ociRecords = append(ociRecords, rec)
		}
	}
	if len(ociRecords) > 0 {
		if b, err := json.MarshalIndent(ociRecords, "", "  "); err == nil {
			if err := os.WriteFile(filepath.Join(dir, "oci_assets.json"), b, 0o600); err != nil {
				warnings = append(warnings, "OCI index: "+err.Error())
			}
		}
	}
	return len(pkgs), count, payloadCount, warnings
}

func (m *Manager) registryAuthFile(username string) (string, error) {
	dir := filepath.Join(m.Config.Backup.Root, ".tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "registry-auth-*.json")
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer f.Close()
	_ = f.Chmod(0o600)
	encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + m.Token))
	payload := map[string]any{"auths": map[string]any{"ghcr.io": map[string]string{"auth": encoded}}}
	b, _ := json.Marshal(payload)
	if _, err := f.Write(b); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func containerImage(owner, packageName string) string {
	p := strings.TrimPrefix(packageName, "/")
	if strings.HasPrefix(strings.ToLower(p), strings.ToLower(owner)+"/") {
		return "ghcr.io/" + p
	}
	return "ghcr.io/" + owner + "/" + p
}

func (m *Manager) Verify(ctx context.Context, emit func(Event)) (int, error) {
	man, err := manifest.ReadLatest(m.Config.Backup.Root)
	if err != nil {
		return 0, err
	}
	sigPath := filepath.Join(m.Config.Backup.Root, "manifests", "latest.json.sig")
	_, sigErr := os.Stat(sigPath)
	if m.Config.Security.SignManifests || sigErr == nil {
		if err := manifest.VerifyLatestSignature(m.Config.Backup.Root, m.Config.Security.SigningKeyPath+".pub"); err != nil {
			return 0, fmt.Errorf("manifest signature: %w", err)
		}
	}
	if m.Config.Security.KMSAttestation.Enabled {
		if err := kmsattest.VerifyFile(ctx, filepath.Join(m.Config.Backup.Root, "manifests", "latest.json"), m.Config.Security.KMSAttestation); err != nil {
			return 0, fmt.Errorf("KMS attestation: %w", err)
		}
	}
	ok := 0
	for i, r := range man.Repositories {
		emit(Event{Time: time.Now(), Repo: r.FullName, Stage: "verify", Message: "checking mirror and bundle", Done: i, Total: len(man.Repositories)})
		mirrorPath := resolveArtifactPath(m.Config.Backup.Root, r.MirrorPath)
		if _, err := execx.Run(ctx, mirrorPath, nil, "git", "fsck", "--full"); err != nil {
			return ok, fmt.Errorf("%s: %w", r.FullName, err)
		}
		if r.BundlePath != "" {
			bundlePath := resolveArtifactPath(m.Config.Backup.Root, r.BundlePath)
			sum, err := sha256File(bundlePath)
			if err != nil {
				return ok, err
			}
			if sum != r.BundleSHA256 {
				return ok, fmt.Errorf("%s: bundle checksum mismatch", r.FullName)
			}
			if _, err := execx.Run(ctx, mirrorPath, nil, "git", "bundle", "verify", bundlePath); err != nil {
				return ok, err
			}
		}
		if r.LFSArchivePath != "" {
			lfsArchivePath := resolveArtifactPath(m.Config.Backup.Root, r.LFSArchivePath)
			sum, err := sha256File(lfsArchivePath)
			if err != nil {
				return ok, err
			}
			if sum != r.LFSArchiveSHA256 {
				return ok, fmt.Errorf("%s: LFS archive checksum mismatch", r.FullName)
			}
		}
		if r.ReleaseAssetsBackedUp > 0 {
			owner, name := splitRepo(r.FullName)
			index := filepath.Join(m.Config.Backup.Root, "metadata", owner, name, "release_assets.json")
			var assets []struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
				Error  string `json:"error"`
			}
			b, err := os.ReadFile(index)
			if err != nil {
				return ok, err
			}
			if err := json.Unmarshal(b, &assets); err != nil {
				return ok, err
			}
			for _, asset := range assets {
				if asset.Error != "" || asset.Path == "" || asset.SHA256 == "" {
					continue
				}
				sum, err := sha256File(resolveArtifactPath(m.Config.Backup.Root, asset.Path))
				if err != nil {
					return ok, err
				}
				if sum != asset.SHA256 {
					return ok, fmt.Errorf("%s: release asset checksum mismatch: %s", r.FullName, asset.Path)
				}
			}
		}
		if r.OCIArtifactsBackedUp > 0 {
			owner, name := splitRepo(r.FullName)
			index := filepath.Join(m.Config.Backup.Root, "metadata", owner, name, "oci_assets.json")
			var assets []struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
				Error  string `json:"error"`
			}
			b, err := os.ReadFile(index)
			if err != nil {
				return ok, err
			}
			if err := json.Unmarshal(b, &assets); err != nil {
				return ok, err
			}
			for _, asset := range assets {
				if asset.Error != "" || asset.Path == "" || asset.SHA256 == "" {
					continue
				}
				sum, err := sha256File(resolveArtifactPath(m.Config.Backup.Root, asset.Path))
				if err != nil {
					return ok, err
				}
				if sum != asset.SHA256 {
					return ok, fmt.Errorf("%s: OCI checksum mismatch: %s", r.FullName, asset.Path)
				}
			}
		}
		ok++
	}
	if err := verifySHA256Sidecars(filepath.Join(m.Config.Backup.Root, "official-exports")); err != nil {
		return ok, fmt.Errorf("official GitHub export: %w", err)
	}
	if err := verifySHA256Sidecars(filepath.Join(m.Config.Backup.Root, "actions-artifacts")); err != nil {
		return ok, fmt.Errorf("GitHub Actions artifacts: %w", err)
	}
	if err := verifySHA256Sidecars(filepath.Join(m.Config.Backup.Root, "packages")); err != nil {
		return ok, fmt.Errorf("GitHub package payloads: %w", err)
	}
	if m.Config.CAS.Enabled {
		if _, err := cas.New(m.Config.CAS.Root, m.Config.CAS.MinFileSize).Verify(); err != nil {
			return ok, fmt.Errorf("CAS: %w", err)
		}
	}
	return ok, nil
}

func sha256File(path string) (string, error) {
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

func verifySHA256Sidecars(root string) error {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".sha256") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fields := strings.Fields(string(b))
		if len(fields) < 1 || len(fields[0]) != 64 {
			return fmt.Errorf("invalid checksum sidecar %s", path)
		}
		target := strings.TrimSuffix(path, ".sha256")
		sum, err := sha256File(target)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, fields[0]) {
			return fmt.Errorf("checksum mismatch: %s", target)
		}
		return nil
	})
}

func archiveDirGzip(srcDir, dst string) error {
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	closeAll := func() error {
		if err := tw.Close(); err != nil {
			_ = gw.Close()
			_ = f.Close()
			return err
		}
		if err := gw.Close(); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}
	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		_ = tw.Close()
		_ = gw.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := closeAll(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func extractTarGzip(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	tr := tar.NewReader(gr)
	base, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(base, filepath.FromSlash(h.Name))
		clean := filepath.Clean(target)
		if clean != base && !strings.HasPrefix(clean, base+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in LFS archive: %s", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(clean, os.FileMode(h.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(clean, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, tr, h.Size)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

func resolveArtifactPath(root, stored string) string {
	if filepath.IsAbs(stored) {
		return stored
	}
	return filepath.Join(root, filepath.FromSlash(stored))
}

func splitRepo(full string) (string, string) {
	p := strings.SplitN(full, "/", 2)
	if len(p) != 2 {
		return "unknown", strings.ReplaceAll(full, "/", "_")
	}
	return safe(p[0]), safe(p[1])
}

func safe(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ". ")
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return out
}

func withUsername(rawURL, username string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = url.User(username)
	return u.String()
}

// Restore clones a verified portable bundle into target as a normal working repository.
func (m *Manager) Restore(ctx context.Context, fullName, target string) error {
	man, err := manifest.ReadLatest(m.Config.Backup.Root)
	if err != nil {
		return err
	}
	for _, r := range man.Repositories {
		if r.FullName != fullName {
			continue
		}
		mirrorPath := resolveArtifactPath(m.Config.Backup.Root, r.MirrorPath)
		cloneSource := mirrorPath
		if r.BundlePath != "" {
			bundlePath := resolveArtifactPath(m.Config.Backup.Root, r.BundlePath)
			sum, err := sha256File(bundlePath)
			if err != nil {
				return err
			}
			if r.BundleSHA256 != "" && sum != r.BundleSHA256 {
				return fmt.Errorf("%s bundle checksum mismatch", fullName)
			}
			cloneSource = bundlePath
		}
		if target == "" {
			_, name := splitRepo(fullName)
			target = name
		}
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("target %s already exists", target)
		}
		_, err = execx.Run(ctx, "", []string{"GIT_LFS_SKIP_SMUDGE=1"}, "git", "clone", cloneSource, target)
		if err != nil {
			return err
		}
		if r.LFSArchivePath != "" {
			lfsArchivePath := resolveArtifactPath(m.Config.Backup.Root, r.LFSArchivePath)
			sum, err := sha256File(lfsArchivePath)
			if err != nil {
				return err
			}
			if r.LFSArchiveSHA256 != "" && sum != r.LFSArchiveSHA256 {
				return fmt.Errorf("%s LFS archive checksum mismatch", fullName)
			}
			if err := extractTarGzip(lfsArchivePath, filepath.Join(target, ".git", "lfs")); err != nil {
				return err
			}
			if execx.Exists("git-lfs") {
				if _, err := execx.Run(ctx, target, nil, "git", "lfs", "checkout"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return fmt.Errorf("repository %s not found in latest manifest", fullName)
}

type DrillReport struct {
	StartedAt time.Time
	EndedAt   time.Time
	WorkDir   string
	Attempted int
	Succeeded int
	Failed    int
	Failures  map[string]string
}

// Drill performs an actual restore of a rotating sample from the latest backup set.
// It verifies the restored Git object database and compares branch/tag refs with the mirror.
func (m *Manager) Drill(ctx context.Context, sampleSize int, emit func(Event)) (DrillReport, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	man, err := manifest.ReadLatest(m.Config.Backup.Root)
	if err != nil {
		return DrillReport{}, err
	}
	if _, err := os.Stat(filepath.Join(m.Config.Backup.Root, "manifests", "latest.json.sig")); err == nil {
		if err := manifest.VerifyLatestSignature(m.Config.Backup.Root, m.Config.Security.SigningKeyPath+".pub"); err != nil {
			return DrillReport{}, fmt.Errorf("manifest signature: %w", err)
		}
	}
	if m.Config.Security.KMSAttestation.Enabled {
		if err := kmsattest.VerifyFile(ctx, filepath.Join(m.Config.Backup.Root, "manifests", "latest.json"), m.Config.Security.KMSAttestation); err != nil {
			return DrillReport{}, fmt.Errorf("KMS attestation: %w", err)
		}
	}
	var candidates []manifest.RepoResult
	for _, r := range man.Repositories {
		if r.Error == "" {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return DrillReport{}, errors.New("no successful repositories in latest manifest")
	}
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].FullName < candidates[i].FullName {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	if sampleSize <= 0 {
		sampleSize = m.Config.RecoveryDrill.SampleSize
	}
	if sampleSize <= 0 || sampleSize > len(candidates) {
		sampleSize = len(candidates)
	}
	startOffset := time.Now().UTC().YearDay() % len(candidates)
	selected := make([]manifest.RepoResult, 0, sampleSize)
	for i := 0; i < sampleSize; i++ {
		selected = append(selected, candidates[(startOffset+i)%len(candidates)])
	}
	base := m.Config.RecoveryDrill.WorkDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "repoark-drills")
	}
	runDir := filepath.Join(base, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return DrillReport{}, err
	}
	report := DrillReport{StartedAt: time.Now().UTC(), WorkDir: runDir, Failures: map[string]string{}}
	for i, r := range selected {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Attempted++
		owner, name := splitRepo(r.FullName)
		target := filepath.Join(runDir, owner+"--"+name)
		emit(Event{Time: time.Now(), Repo: r.FullName, Stage: "drill", Message: "restoring isolated copy", Done: i, Total: len(selected)})
		err := m.Restore(ctx, r.FullName, target)
		if err == nil {
			_, err = execx.Run(ctx, target, nil, "git", "fsck", "--full")
		}
		if err == nil && m.Config.RecoveryDrill.VerifyRefs {
			source := resolveArtifactPath(m.Config.Backup.Root, r.MirrorPath)
			want, werr := gitRefSnapshot(ctx, source)
			got, gerr := gitRefSnapshot(ctx, target)
			if werr != nil {
				err = werr
			} else if gerr != nil {
				err = gerr
			} else if want != got {
				err = errors.New("restored branch/tag refs differ from mirror")
			}
		}
		if err == nil && r.LFSArchivePath != "" && execx.Exists("git-lfs") {
			_, err = execx.Run(ctx, target, nil, "git", "lfs", "fsck")
		}
		if err != nil {
			report.Failed++
			report.Failures[r.FullName] = err.Error()
			emit(Event{Time: time.Now(), Repo: r.FullName, Stage: "drill", Message: "FAILED: " + err.Error(), Done: i + 1, Total: len(selected), Err: err})
			if !m.Config.RecoveryDrill.KeepOnFailure {
				_ = os.RemoveAll(target)
			}
			continue
		}
		report.Succeeded++
		emit(Event{Time: time.Now(), Repo: r.FullName, Stage: "drill", Message: "restore verified", Done: i + 1, Total: len(selected)})
		_ = os.RemoveAll(target)
	}
	report.EndedAt = time.Now().UTC()
	record := state.Record{Kind: "recovery-drill", OK: report.Failed == 0, StartedAt: report.StartedAt, EndedAt: report.EndedAt, Data: map[string]any{"attempted": report.Attempted, "succeeded": report.Succeeded, "failed": report.Failed}}
	if report.Failed > 0 {
		record.Detail = fmt.Sprintf("%d of %d repositories failed", report.Failed, report.Attempted)
	}
	_ = state.Write(filepath.Join(m.Config.Backup.Root, "state", "recovery-drill.json"), record)
	if report.Failed == 0 {
		_ = os.Remove(runDir)
		return report, nil
	}
	return report, fmt.Errorf("recovery drill failed for %d of %d repositories; artifacts: %s", report.Failed, report.Attempted, runDir)
}

func gitRefSnapshot(ctx context.Context, repo string) (string, error) {
	res, err := execx.Run(ctx, repo, nil, "git", "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags")
	if err != nil {
		return "", err
	}
	lines := strings.FieldsFunc(strings.TrimSpace(res.Stdout), func(r rune) bool { return r == '\n' || r == '\r' })
	for i := 0; i < len(lines); i++ {
		for j := i + 1; j < len(lines); j++ {
			if lines[j] < lines[i] {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}
