package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Homiakus/repoark/internal/backup"
	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/controlplane"
	"github.com/Homiakus/repoark/internal/execx"
	"github.com/Homiakus/repoark/internal/fleet"
	"github.com/Homiakus/repoark/internal/githubapi"
	"github.com/Homiakus/repoark/internal/gitlab"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/offsite"
	"github.com/Homiakus/repoark/internal/policy"
)

type jobEvent struct {
	Line string
	Done bool
	Err  error
}

type jobStarted struct {
	ch     <-chan jobEvent
	cancel context.CancelFunc
}
type tickMsg time.Time

type Model struct {
	cfg          config.Config
	width        int
	height       int
	running      bool
	jobName      string
	events       <-chan jobEvent
	cancel       context.CancelFunc
	logs         []string
	last         manifest.Manifest
	lastErr      error
	controlStats controlplane.Stats
	controlReady bool
}

var (
	accent     = lipgloss.Color("#7D56F4")
	muted      = lipgloss.Color("#7A7A85")
	good       = lipgloss.Color("#36D399")
	bad        = lipgloss.Color("#F87272")
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	keyStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	mutedStyle = lipgloss.NewStyle().Foreground(muted)
	goodStyle  = lipgloss.NewStyle().Foreground(good)
	badStyle   = lipgloss.NewStyle().Foreground(bad)
	cardStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

func New(cfg config.Config) Model {
	m := Model{cfg: cfg, logs: []string{"RepoArk ready"}}
	m.reloadManifest()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		switch strings.ToLower(msg.String()) {
		case "q":
			if !m.running {
				return m, tea.Quit
			}
		case "ctrl+c", "esc":
			if m.running && m.cancel != nil {
				m.cancel()
				m.logs = appendLog(m.logs, "cancellation requested")
				return m, nil
			}
			return m, tea.Quit
		case "b":
			if !m.running {
				return m, startBackup(m.cfg)
			}
		case "v":
			if !m.running {
				return m, startVerify(m.cfg)
			}
		case "d":
			if !m.running {
				return m, startGitLabDeploy(m.cfg)
			}
		case "m":
			if !m.running {
				return m, startGitLabMigrate(m.cfg)
			}
		case "g":
			if !m.running {
				return m, startGitLabBackup(m.cfg)
			}
		case "o":
			if !m.running {
				return m, startOffsite(m.cfg)
			}
		case "t":
			if !m.running {
				return m, startDrill(m.cfg)
			}
		case "f":
			if !m.running {
				return m, startFleetBackup(m.cfg)
			}
		case "x":
			if !m.running {
				return m, startGitLabRestoreDrill(m.cfg)
			}
		case "c":
			if !m.running {
				return m, startCASCompact(m.cfg)
			}
		case "p":
			if !m.running {
				return m, startPolicyCheck(m.cfg)
			}
		case "r":
			m.reloadManifest()
			m.logs = appendLog(m.logs, "state refreshed")
		}
	case jobStarted:
		m.running = true
		m.events = msg.ch
		m.cancel = msg.cancel
		m.lastErr = nil
		return m, waitEvent(msg.ch)
	case jobEvent:
		if msg.Line != "" {
			m.logs = appendLog(m.logs, msg.Line)
		}
		if msg.Done {
			m.running = false
			m.cancel = nil
			m.lastErr = msg.Err
			m.reloadManifest()
			return m, nil
		}
		return m, waitEvent(m.events)
	case tickMsg:
		return m, nil
	}
	return m, nil
}

func (m Model) View() tea.View {
	w := m.width
	if w <= 0 {
		w = 100
	}
	content := m.render(w)
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "RepoArk — Git backup center"
	return v
}

func (m Model) render(width int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("RepoArk"))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("GitHub backup + GitLab disaster recovery"))
	b.WriteString("\n\n")

	token := githubapi.ResolveToken(m.cfg.GitHub.TokenEnv)
	auth := badStyle.Render("missing")
	if token != "" {
		auth = goodStyle.Render("ready")
	}
	git := badStyle.Render("missing")
	if execx.Exists("git") {
		git = goodStyle.Render("ready")
	}
	lfs := mutedStyle.Render("optional/missing")
	if execx.Exists("git-lfs") {
		lfs = goodStyle.Render("ready")
	}
	docker := mutedStyle.Render("not detected")
	if execx.Exists("docker") {
		docker = goodStyle.Render("ready")
	}

	left := fmt.Sprintf("GitHub auth  %s\nGit           %s\nGit LFS       %s\nDocker        %s", auth, git, lfs, docker)
	signed := mutedStyle.Render("no")
	if m.last.SignatureType != "" {
		signed = goodStyle.Render("Ed25519")
	}
	policyState := mutedStyle.Render("disabled")
	if m.cfg.Policy.Enabled {
		pr := policy.Evaluate(context.Background(), m.cfg, time.Now())
		if pr.Healthy {
			policyState = goodStyle.Render("healthy")
		} else {
			policyState = badStyle.Render(fmt.Sprintf("%d violations", len(pr.Violations)))
		}
	}
	controlLine := mutedStyle.Render("disabled")
	if m.cfg.ControlPlane.Enabled {
		if m.controlReady {
			controlLine = goodStyle.Render(fmt.Sprintf("q=%d run=%d fail=%d stranded=%d agents=%d storage=%d/%d replicas=%d xfer=%d approvals=%d", m.controlStats.QueuedJobs, m.controlStats.RunningJobs, m.controlStats.FailedJobs, m.controlStats.StrandedJobs, m.controlStats.ConnectedAgents, m.controlStats.DegradedStorageAgents, m.controlStats.UnhealthyStorageAgents, m.controlStats.ReadyReplicas, m.controlStats.ActiveTransfers, m.controlStats.PendingApprovals))
		} else {
			controlLine = badStyle.Render("unavailable")
		}
	}
	right := fmt.Sprintf("Backup root   %s\nRepos latest  %d\nLast success  %d\nLast failed   %d\nWarnings      %d\nProjects v2   %d owners\nFleet         %d accounts\nCAS objects   %d\nControl       %s\nPolicy        %s\nManifest      %s", m.cfg.Backup.Root, len(m.last.Repositories), m.last.Succeeded, m.last.Failed, m.last.WarningCount, m.last.ProjectsV2OwnersBackedUp, len(m.cfg.Fleet.Accounts), m.last.CAS.Objects, controlLine, policyState, signed)
	cardWidth := max(30, (width-5)/2)
	leftCard := cardStyle.Width(cardWidth).Render(left)
	rightCard := cardStyle.Width(cardWidth).Render(right)
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftCard, " ", rightCard))
	b.WriteString("\n\n")

	state := goodStyle.Render("IDLE")
	if m.running {
		state = titleStyle.Render("RUNNING")
	}
	if m.lastErr != nil {
		state = badStyle.Render("ERROR: " + m.lastErr.Error())
	}
	b.WriteString("State: " + state + "\n")
	b.WriteString(mutedStyle.Render("Activity"))
	b.WriteString("\n")
	logLines := m.logs
	maxLines := 12
	if m.height > 0 {
		maxLines = max(5, min(16, m.height-15))
	}
	if len(logLines) > maxLines {
		logLines = logLines[len(logLines)-maxLines:]
	}
	for _, line := range logLines {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(keyStyle.Render("b") + " backup   " + keyStyle.Render("f") + " fleet   " + keyStyle.Render("v") + " verify   " + keyStyle.Render("p") + " policy   " + keyStyle.Render("c") + " CAS   " + keyStyle.Render("t") + " repo drill   " + keyStyle.Render("x") + " GitLab drill   " + keyStyle.Render("d") + " deploy   " + keyStyle.Render("m") + " migrate   " + keyStyle.Render("g") + " GitLab backup   " + keyStyle.Render("o") + " offsite   " + keyStyle.Render("r") + " refresh   " + keyStyle.Render("q") + " quit")
	return b.String()
}

func startBackup(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 128)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			mgr := backup.New(cfg)
			_, err := mgr.Run(ctx, func(e backup.Event) {
				line := fmt.Sprintf("[%s] %-10s %s", e.Repo, e.Stage, e.Message)
				if e.Repo == "" {
					line = fmt.Sprintf("%-10s %s", e.Stage, e.Message)
				}
				ch <- jobEvent{Line: line}
			})
			ch <- jobEvent{Line: finishLine("backup", err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startFleetBackup(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 128)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			results, err := fleet.RunBackup(ctx, cfg, func(e fleet.Event) { ch <- jobEvent{Line: fmt.Sprintf("[%s] %-10s %s", e.Account, e.Stage, e.Message)} })
			ch <- jobEvent{Line: fmt.Sprintf("fleet finished: %d accounts; %v", len(results), err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startGitLabRestoreDrill(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 128)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			report, err := gitlab.New(cfg).RestoreDrill(ctx, "", func(msg string) { ch <- jobEvent{Line: "[gitlab-drill] " + msg} })
			ch <- jobEvent{Line: fmt.Sprintf("GitLab drill: backup=%s healthy=%t; %v", report.BackupID, report.Healthy, err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startVerify(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 64)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			mgr := backup.New(cfg)
			n, err := mgr.Verify(ctx, func(e backup.Event) { ch <- jobEvent{Line: fmt.Sprintf("[%s] verify %s", e.Repo, e.Message)} })
			ch <- jobEvent{Line: fmt.Sprintf("verify finished: %d repositories; %v", n, err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startDrill(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 128)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			report, err := backup.New(cfg).Drill(ctx, cfg.RecoveryDrill.SampleSize, func(e backup.Event) {
				ch <- jobEvent{Line: fmt.Sprintf("[%s] drill %s", e.Repo, e.Message)}
			})
			ch <- jobEvent{Line: fmt.Sprintf("drill finished: %d OK, %d failed; %v", report.Succeeded, report.Failed, err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startGitLabDeploy(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 8)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			ch <- jobEvent{Line: "GitLab: generating Compose and deploying"}
			err := gitlab.New(cfg).Deploy(ctx, cfg.GitLab.RemoteHost)
			ch <- jobEvent{Line: finishLine("GitLab deploy", err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startGitLabMigrate(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 128)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			err := gitlab.New(cfg).MigrateLatest(ctx, func(e gitlab.Event) { ch <- jobEvent{Line: fmt.Sprintf("[%s] %s", e.Repo, e.Message)} })
			ch <- jobEvent{Line: finishLine("GitLab migration", err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startGitLabBackup(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 8)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			path, err := gitlab.New(cfg).Backup(ctx, cfg.GitLab.RemoteHost)
			line := finishLine("GitLab backup", err)
			if err == nil {
				line += ": " + path
			}
			ch <- jobEvent{Line: line, Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startOffsite(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 8)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			ch <- jobEvent{Line: "offsite: replicating backup set with configured backend"}
			err := offsite.Sync(ctx, cfg)
			ch <- jobEvent{Line: finishLine("offsite sync", err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startCASCompact(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 8)
		ctx, cancel := context.WithCancel(context.Background())
		_ = ctx
		go func() {
			defer close(ch)
			if !cfg.CAS.Enabled {
				ch <- jobEvent{Line: "CAS disabled", Done: true, Err: fmt.Errorf("CAS disabled")}
				return
			}
			store := cas.New(cfg.CAS.Root, cfg.CAS.MinFileSize)
			paths := []string{filepath.Join(cfg.Backup.Root, "release-assets"), filepath.Join(cfg.Backup.Root, "actions-artifacts"), filepath.Join(cfg.Backup.Root, "oci"), filepath.Join(cfg.Backup.Root, "packages"), filepath.Join(cfg.Backup.Root, "official-exports"), filepath.Join(cfg.Backup.Root, "lfs"), filepath.Join(cfg.Backup.Root, "bundles")}
			st, err := store.Compact(paths)
			ch <- jobEvent{Line: fmt.Sprintf("CAS: objects=%d reclaimed=%d bytes; %v", st.Objects, st.Reclaimed, err), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func startPolicyCheck(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan jobEvent, 8)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer close(ch)
			r := policy.Evaluate(ctx, cfg, time.Now())
			err := policy.Error(r)
			ch <- jobEvent{Line: fmt.Sprintf("policy: healthy=%t violations=%d", r.Healthy, len(r.Violations)), Done: true, Err: err}
		}()
		return jobStarted{ch: ch, cancel: cancel}
	}
}

func waitEvent(ch <-chan jobEvent) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return jobEvent{Done: true}
		}
		return e
	}
}

func finishLine(name string, err error) string {
	if err != nil {
		return name + " failed: " + err.Error()
	}
	return name + " completed successfully"
}

func appendLog(lines []string, s string) []string {
	stamp := time.Now().Format("15:04:05")
	lines = append(lines, stamp+"  "+s)
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	return lines
}

func (m *Model) reloadManifest() {
	man, err := manifest.ReadLatest(m.cfg.Backup.Root)
	if err == nil {
		m.last = man
	}
	m.controlReady = false
	if m.cfg.ControlPlane.Enabled {
		if st, err := controlplane.OpenStore(m.cfg.ControlPlane.Store); err == nil {
			if stats, err := st.Stats(context.Background(), time.Now().UTC()); err == nil {
				m.controlStats = stats
				m.controlReady = true
			}
			_ = st.Close()
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
