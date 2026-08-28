package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/githubapi"
)

type packagePayloadRecord struct {
	Type    string `json:"type"`
	Package string `json:"package"`
	Version string `json:"version"`
	Path    string `json:"path,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size_bytes,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (m *Manager) backupPackagePayloads(ctx context.Context, repo githubapi.Repository, owner, name string, pkgs []githubapi.Package) (int, []string) {
	if !m.Config.Packages.Enabled || m.Token == "" {
		return 0, nil
	}
	var records []packagePayloadRecord
	var warnings []string
	count := 0
	for _, pkg := range pkgs {
		if pkg.PackageType == "container" || pkg.PackageType == "docker" {
			continue
		}
		versions := packageVersions(pkg)
		if len(versions) == 0 {
			warnings = append(warnings, fmt.Sprintf("package payload %s/%s: no versions returned by GitHub API", pkg.PackageType, pkg.Name))
			continue
		}
		switch pkg.PackageType {
		case "npm":
			if !m.Config.Packages.NPM {
				continue
			}
			n, rs, ws := m.archiveNPM(ctx, owner, name, pkg, versions)
			count += n
			records = append(records, rs...)
			warnings = append(warnings, ws...)
		case "nuget":
			if !m.Config.Packages.NuGet {
				continue
			}
			n, rs, ws := m.archiveNuGet(ctx, owner, name, pkg, versions)
			count += n
			records = append(records, rs...)
			warnings = append(warnings, ws...)
		case "maven":
			if !m.Config.Packages.Maven {
				continue
			}
			n, rs, ws := m.archiveMaven(ctx, repo, owner, name, pkg, versions)
			count += n
			records = append(records, rs...)
			warnings = append(warnings, ws...)
		case "rubygems":
			if !m.Config.Packages.RubyGems {
				continue
			}
			n, rs, ws := m.archiveRubyGems(ctx, owner, name, pkg, versions)
			count += n
			records = append(records, rs...)
			warnings = append(warnings, ws...)
		}
	}
	if len(records) > 0 {
		sort.Slice(records, func(i, j int) bool {
			if records[i].Type != records[j].Type {
				return records[i].Type < records[j].Type
			}
			if records[i].Package != records[j].Package {
				return records[i].Package < records[j].Package
			}
			return records[i].Version < records[j].Version
		})
		dir := filepath.Join(m.Config.Backup.Root, "metadata", owner, name)
		if err := os.MkdirAll(dir, 0o700); err == nil {
			if b, err := json.MarshalIndent(records, "", "  "); err == nil {
				if err := os.WriteFile(filepath.Join(dir, "package_payloads.json"), b, 0o600); err != nil {
					warnings = append(warnings, "package payload index: "+err.Error())
				}
			}
		}
	}
	return count, warnings
}

func packageVersions(pkg githubapi.Package) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range pkg.Versions {
		var v struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &v) == nil && strings.TrimSpace(v.Name) != "" && !seen[v.Name] {
			seen[v.Name] = true
			out = append(out, v.Name)
		}
	}
	sort.Strings(out)
	return out
}

func (m *Manager) archiveNPM(ctx context.Context, owner, repoName string, pkg githubapi.Package, versions []string) (int, []packagePayloadRecord, []string) {
	base := strings.TrimRight(m.Config.Packages.NPMRegistry, "/")
	npmName := pkg.Name
	if !strings.HasPrefix(npmName, "@") {
		npmName = "@" + owner + "/" + npmName
	}
	endpoint := base + "/" + strings.ReplaceAll(url.PathEscape(npmName), "%2F", "%2f")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.GitHub.HTTP.Do(req)
	if err != nil {
		return 0, nil, []string{"npm registry: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, nil, []string{fmt.Sprintf("npm registry %s returned %d", pkg.Name, resp.StatusCode)}
	}
	var meta struct {
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&meta); err != nil {
		return 0, nil, []string{"npm metadata: " + err.Error()}
	}
	var records []packagePayloadRecord
	var warnings []string
	count := 0
	for _, ver := range versions {
		r := packagePayloadRecord{Type: "npm", Package: pkg.Name, Version: ver}
		vm, ok := meta.Versions[ver]
		if !ok || vm.Dist.Tarball == "" {
			r.Error = "version missing from npm registry metadata"
			records = append(records, r)
			warnings = append(warnings, fmt.Sprintf("npm %s@%s: %s", pkg.Name, ver, r.Error))
			continue
		}
		rel := filepath.Join("packages", owner, repoName, "npm", safe(pkg.Name), safe(ver)+".tgz")
		dst := filepath.Join(m.Config.Backup.Root, rel)
		size, sum, err := m.downloadRegistry(ctx, vm.Dist.Tarball, dst, "bearer", m.User)
		if err != nil {
			r.Error = err.Error()
			warnings = append(warnings, fmt.Sprintf("npm %s@%s: %v", pkg.Name, ver, err))
		} else {
			r.Path = filepath.ToSlash(rel)
			r.Size = size
			r.SHA256 = sum
			count++
		}
		records = append(records, r)
	}
	return count, records, warnings
}

func (m *Manager) archiveNuGet(ctx context.Context, owner, repoName string, pkg githubapi.Package, versions []string) (int, []packagePayloadRecord, []string) {
	service := strings.TrimRight(m.Config.Packages.NuGetRegistry, "/") + "/" + url.PathEscape(owner) + "/index.json"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, service, nil)
	req.SetBasicAuth(m.User, m.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.GitHub.HTTP.Do(req)
	if err != nil {
		return 0, nil, []string{"nuget service index: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, nil, []string{fmt.Sprintf("nuget service index returned %d", resp.StatusCode)}
	}
	var idx struct {
		Resources []struct {
			ID   string `json:"@id"`
			Type any    `json:"@type"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&idx); err != nil {
		return 0, nil, []string{"nuget service index: " + err.Error()}
	}
	flat := ""
	for _, r := range idx.Resources {
		b, _ := json.Marshal(r.Type)
		if strings.Contains(strings.ToLower(string(b)), "packagebaseaddress") {
			flat = strings.TrimRight(r.ID, "/")
			break
		}
	}
	if flat == "" {
		return 0, nil, []string{"nuget service index has no PackageBaseAddress resource"}
	}
	id := strings.ToLower(pkg.Name)
	var records []packagePayloadRecord
	var warnings []string
	count := 0
	for _, ver := range versions {
		low := strings.ToLower(ver)
		u := flat + "/" + url.PathEscape(id) + "/" + url.PathEscape(low) + "/" + url.PathEscape(id) + "." + url.PathEscape(low) + ".nupkg"
		r := packagePayloadRecord{Type: "nuget", Package: pkg.Name, Version: ver}
		rel := filepath.Join("packages", owner, repoName, "nuget", safe(pkg.Name), safe(ver)+".nupkg")
		dst := filepath.Join(m.Config.Backup.Root, rel)
		size, sum, err := m.downloadRegistry(ctx, u, dst, "basic", m.User)
		if err != nil {
			r.Error = err.Error()
			warnings = append(warnings, fmt.Sprintf("nuget %s@%s: %v", pkg.Name, ver, err))
		} else {
			r.Path = filepath.ToSlash(rel)
			r.Size = size
			r.SHA256 = sum
			count++
		}
		records = append(records, r)
	}
	return count, records, warnings
}

func (m *Manager) archiveMaven(ctx context.Context, repo githubapi.Repository, owner, repoName string, pkg githubapi.Package, versions []string) (int, []packagePayloadRecord, []string) {
	if !execx.Exists("mvn") {
		return 0, nil, []string{"Maven payload backup requested but mvn is not installed"}
	}
	coord := strings.TrimSpace(pkg.Name)
	if strings.Count(coord, ":") != 1 {
		return 0, nil, []string{fmt.Sprintf("maven %s: cannot infer groupId:artifactId from package name; metadata preserved", pkg.Name)}
	}
	tmpRoot := filepath.Join(m.Config.Backup.Root, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return 0, nil, []string{"maven temp: " + err.Error()}
	}
	temp, err := os.MkdirTemp(tmpRoot, "maven-*")
	if err != nil {
		return 0, nil, []string{"maven temp: " + err.Error()}
	}
	defer os.RemoveAll(temp)
	settings := filepath.Join(temp, "settings.xml")
	xml := fmt.Sprintf(`<settings><servers><server><id>github</id><username>${env.REPOARK_PACKAGE_USER}</username><password>${env.REPOARK_PACKAGE_TOKEN}</password></server></servers></settings>`)
	if err := os.WriteFile(settings, []byte(xml), 0o600); err != nil {
		return 0, nil, []string{"maven settings: " + err.Error()}
	}
	remote := strings.TrimRight(m.Config.Packages.MavenRegistry, "/") + "/" + owner + "/" + repoName
	env := []string{"REPOARK_PACKAGE_USER=" + m.User, "REPOARK_PACKAGE_TOKEN=" + m.Token}
	var records []packagePayloadRecord
	var warnings []string
	count := 0
	for _, ver := range versions {
		r := packagePayloadRecord{Type: "maven", Package: pkg.Name, Version: ver}
		local := filepath.Join(temp, "repo")
		_, err := execx.Run(ctx, temp, env, "mvn", "-q", "-s", settings, "-Dmaven.repo.local="+local, "dependency:get", "-Dtransitive=false", "-Dartifact="+coord+":"+ver, "-DremoteRepositories=github::default::"+remote)
		if err != nil {
			r.Error = redactSecret(err.Error(), m.Token)
			warnings = append(warnings, fmt.Sprintf("maven %s:%s: %s", coord, ver, r.Error))
			records = append(records, r)
			continue
		}
		files, _ := collectMavenVersionFiles(local, coord, ver)
		if len(files) == 0 {
			r.Error = "mvn completed but no artifact files were found"
			warnings = append(warnings, "maven "+coord+":"+ver+": "+r.Error)
			records = append(records, r)
			continue
		}
		destDir := filepath.Join(m.Config.Backup.Root, "packages", owner, repo.Name, "maven", safe(strings.ReplaceAll(coord, ":", "--")), safe(ver))
		if err := os.MkdirAll(destDir, 0o700); err != nil {
			r.Error = err.Error()
			records = append(records, r)
			continue
		}
		var total int64
		var hashes []string
		for _, src := range files {
			dst := filepath.Join(destDir, filepath.Base(src))
			if err := copyFile(src, dst); err != nil {
				r.Error = err.Error()
				break
			}
			sum, size, err := hashAndSidecar(dst)
			if err != nil {
				r.Error = err.Error()
				break
			}
			total += size
			if m.Config.Packages.MaxBytes > 0 && total > m.Config.Packages.MaxBytes {
				r.Error = fmt.Sprintf("package version exceeds limit %d bytes", m.Config.Packages.MaxBytes)
				_ = os.RemoveAll(destDir)
				break
			}
			hashes = append(hashes, filepath.Base(dst)+":"+sum)
		}
		if r.Error == "" {
			rel, _ := filepath.Rel(m.Config.Backup.Root, destDir)
			r.Path = filepath.ToSlash(rel)
			r.Size = total
			r.SHA256 = strings.Join(hashes, ",")
			count++
		}
		records = append(records, r)
	}
	return count, records, warnings
}

func (m *Manager) archiveRubyGems(ctx context.Context, owner, repoName string, pkg githubapi.Package, versions []string) (int, []packagePayloadRecord, []string) {
	if !execx.Exists("gem") {
		return 0, nil, []string{"RubyGems payload backup requested but gem is not installed"}
	}
	tmpRoot := filepath.Join(m.Config.Backup.Root, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return 0, nil, []string{"rubygems temp: " + err.Error()}
	}
	home, err := os.MkdirTemp(tmpRoot, "gem-home-*")
	if err != nil {
		return 0, nil, []string{"rubygems temp: " + err.Error()}
	}
	defer os.RemoveAll(home)
	if err := os.MkdirAll(filepath.Join(home, ".gem"), 0o700); err != nil {
		return 0, nil, []string{err.Error()}
	}
	source := strings.TrimRight(m.Config.Packages.RubyGemsRegistry, "/") + "/" + owner + "/"
	// Token is stored only in a mode-0600 ephemeral config under the temporary HOME.
	authSource := strings.Replace(source, "://", "://"+url.QueryEscape(m.User)+":"+url.QueryEscape(m.Token)+"@", 1)
	gemrc := fmt.Sprintf("---\n:sources:\n- %s\n", authSource)
	if err := os.WriteFile(filepath.Join(home, ".gemrc"), []byte(gemrc), 0o600); err != nil {
		return 0, nil, []string{err.Error()}
	}
	env := []string{"HOME=" + home, "GEM_HOME=" + filepath.Join(home, "gems")}
	var records []packagePayloadRecord
	var warnings []string
	count := 0
	for _, ver := range versions {
		r := packagePayloadRecord{Type: "rubygems", Package: pkg.Name, Version: ver}
		work := filepath.Join(home, "fetch")
		_ = os.MkdirAll(work, 0o700)
		_, err := execx.Run(ctx, work, env, "gem", "fetch", pkg.Name, "--version", ver)
		if err != nil {
			r.Error = redactSecret(err.Error(), m.Token)
			warnings = append(warnings, fmt.Sprintf("rubygems %s@%s: %s", pkg.Name, ver, r.Error))
			records = append(records, r)
			continue
		}
		src := filepath.Join(work, pkg.Name+"-"+ver+".gem")
		rel := filepath.Join("packages", owner, repoName, "rubygems", safe(pkg.Name), safe(ver)+".gem")
		dst := filepath.Join(m.Config.Backup.Root, rel)
		if err := copyFile(src, dst); err != nil {
			r.Error = err.Error()
			records = append(records, r)
			continue
		}
		sum, size, err := hashAndSidecar(dst)
		if err != nil {
			r.Error = err.Error()
		} else if m.Config.Packages.MaxBytes > 0 && size > m.Config.Packages.MaxBytes {
			r.Error = fmt.Sprintf("package version exceeds limit %d bytes", m.Config.Packages.MaxBytes)
			_ = os.Remove(dst)
			_ = os.Remove(dst + ".sha256")
		} else {
			r.Path = filepath.ToSlash(rel)
			r.SHA256 = sum
			r.Size = size
			count++
		}
		records = append(records, r)
	}
	return count, records, warnings
}

func (m *Manager) downloadRegistry(ctx context.Context, rawURL, dst, auth, user string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, "", err
	}
	if auth == "basic" {
		req.SetBasicAuth(user, m.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+m.Token)
	}
	resp, err := m.GitHub.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, "", fmt.Errorf("registry download returned %d", resp.StatusCode)
	}
	limit := m.Config.Packages.MaxBytes
	if limit <= 0 {
		limit = 2 << 30
	}
	if resp.ContentLength > limit {
		return 0, "", fmt.Errorf("payload is %d bytes and exceeds limit %d", resp.ContentLength, limit)
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", err
	}
	n, cpErr := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if cpErr == nil {
		cpErr = closeErr
	}
	if cpErr != nil {
		_ = os.Remove(tmp)
		return 0, "", cpErr
	}
	if n > limit {
		_ = os.Remove(tmp)
		return 0, "", fmt.Errorf("payload exceeds limit %d", limit)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return 0, "", err
	}
	sum, size, err := hashAndSidecar(dst)
	return size, sum, err
}

func hashAndSidecar(path string) (string, int64, error) {
	sum, size, err := hashFileSize(path)
	if err != nil {
		return "", 0, err
	}
	_ = os.WriteFile(path+".sha256", []byte(sum+"  "+filepath.Base(path)+"\n"), 0o600)
	return sum, size, nil
}
func hashFileSize(path string) (string, int64, error) {
	sum, err := sha256File(path)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return sum, info.Size(), nil
}
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, cp := io.Copy(out, in)
	cl := out.Close()
	if cp != nil {
		return cp
	}
	return cl
}
func collectMavenVersionFiles(local, coord, version string) ([]string, error) {
	parts := strings.Split(coord, ":")
	if len(parts) != 2 {
		return nil, errors.New("invalid Maven coordinate")
	}
	dir := filepath.Join(append([]string{local}, strings.Split(parts[0], ".")...)...)
	dir = filepath.Join(dir, parts[1], version)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".lastUpdated") || strings.HasPrefix(e.Name(), "_remote.repositories") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

func redactSecret(s string, secrets ...string) string {
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	return s
}
