package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version       int                  `yaml:"version"`
	GitHub        GitHubConfig         `yaml:"github"`
	Backup        BackupConfig         `yaml:"backup"`
	GitLab        GitLabConfig         `yaml:"gitlab"`
	Daemon        DaemonConfig         `yaml:"daemon"`
	Security      SecurityConfig       `yaml:"security"`
	Offsite       OffsiteConfig        `yaml:"offsite"`
	RecoveryDrill RecoveryDrillConfig  `yaml:"recovery_drill"`
	Observability ObservabilityConfig  `yaml:"observability"`
	Notifications NotificationConfig   `yaml:"notifications"`
	Fleet         FleetConfig          `yaml:"fleet"`
	Audit         AuditConfig          `yaml:"audit"`
	CAS           CASConfig            `yaml:"cas"`
	Policy        PolicyConfig         `yaml:"policy"`
	Packages      PackagePayloadConfig `yaml:"package_payloads"`
	ControlPlane  ControlPlaneConfig   `yaml:"control_plane"`
}

type GitHubConfig struct {
	APIURL           string `yaml:"api_url"`
	GraphQLURL       string `yaml:"graphql_url"`
	TokenEnv         string `yaml:"token_env"`
	CloneProtocol    string `yaml:"clone_protocol"`
	Metadata         string `yaml:"metadata"`
	IncludeForks     bool   `yaml:"include_forks"`
	IncludeArchived  bool   `yaml:"include_archived"`
	ReleaseAssets    bool   `yaml:"release_assets"`
	Discussions      bool   `yaml:"discussions"`
	Packages         bool   `yaml:"packages"`
	OCIExport        bool   `yaml:"oci_export"`
	MaxAssetBytes    int64  `yaml:"max_asset_bytes"`
	MaxArtifactBytes int64  `yaml:"max_artifact_bytes"`
	MaxMetadataPages int    `yaml:"max_metadata_pages"`
	ActionsArtifacts bool   `yaml:"actions_artifacts"`
	ProjectsV2       bool   `yaml:"projects_v2"`
}

type BackupConfig struct {
	Root              string `yaml:"root"`
	Concurrency       int    `yaml:"concurrency"`
	CreateBundles     bool   `yaml:"create_bundles"`
	FetchLFS          bool   `yaml:"fetch_lfs"`
	VerifyAfterBackup bool   `yaml:"verify_after_backup"`
	KeepManifests     int    `yaml:"keep_manifests"`
}

type GitLabConfig struct {
	Enabled            bool                     `yaml:"enabled"`
	URL                string                   `yaml:"url"`
	TokenEnv           string                   `yaml:"token_env"`
	Image              string                   `yaml:"image"`
	Hostname           string                   `yaml:"hostname"`
	HTTPPort           int                      `yaml:"http_port"`
	HTTPSPort          int                      `yaml:"https_port"`
	SSHPort            int                      `yaml:"ssh_port"`
	DataDir            string                   `yaml:"data_dir"`
	Container          string                   `yaml:"container_name"`
	RemoteHost         string                   `yaml:"remote_host"`
	PreserveNamespaces bool                     `yaml:"preserve_namespaces"`
	RestoreDrill       GitLabRestoreDrillConfig `yaml:"restore_drill"`
}

type DaemonConfig struct {
	Interval   string `yaml:"interval"`
	RunOnStart bool   `yaml:"run_on_start"`
}

type SecurityConfig struct {
	RequireVerification bool                 `yaml:"require_verification"`
	SignManifests       bool                 `yaml:"sign_manifests"`
	SigningKeyPath      string               `yaml:"signing_key_path"`
	KMSAttestation      KMSAttestationConfig `yaml:"kms_attestation"`
}

type KMSAttestationConfig struct {
	Enabled          bool   `yaml:"enabled"`
	KeyID            string `yaml:"key_id"`
	SigningAlgorithm string `yaml:"signing_algorithm"`
	Region           string `yaml:"region"`
	Profile          string `yaml:"profile"`
	EndpointURL      string `yaml:"endpoint_url"`
	RequireValid     bool   `yaml:"require_valid"`
}

type OffsiteConfig struct {
	Enabled       bool             `yaml:"enabled"`
	Backend       string           `yaml:"backend"` // restic or rclone
	RepositoryEnv string           `yaml:"repository_env"`
	PasswordEnv   string           `yaml:"password_env"`
	RcloneRemote  string           `yaml:"rclone_remote"`
	KeepDaily     int              `yaml:"keep_daily"`
	KeepWeekly    int              `yaml:"keep_weekly"`
	KeepMonthly   int              `yaml:"keep_monthly"`
	Prune         bool             `yaml:"prune"`
	ObjectLock    ObjectLockConfig `yaml:"object_lock"`
}

type GitLabRestoreDrillConfig struct {
	Enabled       bool   `yaml:"enabled"`
	WorkDir       string `yaml:"work_dir"`
	HTTPPort      int    `yaml:"http_port"`
	SSHPort       int    `yaml:"ssh_port"`
	Timeout       string `yaml:"timeout"`
	KeepOnFailure bool   `yaml:"keep_on_failure"`
}

type ObjectLockConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Bucket           string `yaml:"bucket"`
	Prefix           string `yaml:"prefix"`
	Region           string `yaml:"region"`
	EndpointURL      string `yaml:"endpoint_url"`
	Profile          string `yaml:"profile"`
	RequireEnabled   bool   `yaml:"require_enabled"`
	ExpectedMode     string `yaml:"expected_mode"`
	MinRetentionDays int    `yaml:"min_retention_days"`
}

type FleetAccountConfig struct {
	Name          string `yaml:"name"`
	APIURL        string `yaml:"api_url"`
	GraphQLURL    string `yaml:"graphql_url"`
	TokenEnv      string `yaml:"token_env"`
	BackupRoot    string `yaml:"backup_root"`
	CloneProtocol string `yaml:"clone_protocol"`
}

type FleetConfig struct {
	Enabled     bool                 `yaml:"enabled"`
	Concurrency int                  `yaml:"concurrency"`
	Accounts    []FleetAccountConfig `yaml:"accounts"`
}

type AuditConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Required bool   `yaml:"required"`
	Path     string `yaml:"path"`
}

type CASConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Root        string `yaml:"root"`
	MinFileSize int64  `yaml:"min_file_size"`
	AutoCompact bool   `yaml:"auto_compact"`
}

type PolicyConfig struct {
	Enabled                  bool   `yaml:"enabled"`
	EnforceInHealth          bool   `yaml:"enforce_in_health"`
	MaxBackupAge             string `yaml:"max_backup_age"`
	MaxFailedRepositories    int    `yaml:"max_failed_repositories"`
	MaxWarnings              int    `yaml:"max_warnings"`
	RequireSignedManifest    bool   `yaml:"require_signed_manifest"`
	RequireAudit             bool   `yaml:"require_audit"`
	MaxRecoveryDrillAge      string `yaml:"max_recovery_drill_age"`
	MaxGitLabDrillAge        string `yaml:"max_gitlab_drill_age"`
	MaxOffsiteAge            string `yaml:"max_offsite_age"`
	MaxRecoveryDrillDuration string `yaml:"max_recovery_drill_duration"`
	MaxGitLabDrillDuration   string `yaml:"max_gitlab_drill_duration"`
}

type PackagePayloadConfig struct {
	Enabled          bool   `yaml:"enabled"`
	NPM              bool   `yaml:"npm"`
	NuGet            bool   `yaml:"nuget"`
	Maven            bool   `yaml:"maven"`
	RubyGems         bool   `yaml:"rubygems"`
	MaxBytes         int64  `yaml:"max_bytes"`
	NPMRegistry      string `yaml:"npm_registry"`
	NuGetRegistry    string `yaml:"nuget_registry"`
	MavenRegistry    string `yaml:"maven_registry"`
	RubyGemsRegistry string `yaml:"rubygems_registry"`
}

type ControlPlaneConfig struct {
	Enabled     bool                  `yaml:"enabled"`
	Store       StoreConfig           `yaml:"store"`
	Workers     WorkerConfig          `yaml:"workers"`
	Scheduler   SchedulerConfig       `yaml:"scheduler"`
	Generations GenerationConfig      `yaml:"generations"`
	Mirroring   MirroringConfig       `yaml:"mirroring"`
	Agents      AgentConfig           `yaml:"agents"`
	Replication ReplicationConfig     `yaml:"replication"`
	RestoreAuth RestoreApprovalConfig `yaml:"restore_approval"`
	Storage     StorageDataConfig     `yaml:"storage"`
	WebAuth     WebAuthConfig         `yaml:"web_auth"`
}

type StoreConfig struct {
	Driver     string `yaml:"driver"`
	SQLitePath string `yaml:"sqlite_path"`
	DSNEnv     string `yaml:"dsn_env"`
}

type WorkerConfig struct {
	Concurrency  int    `yaml:"concurrency"`
	PollInterval string `yaml:"poll_interval"`
	Lease        string `yaml:"lease"`
	MaxAttempts  int    `yaml:"max_attempts"`
}

type RepoSchedulePolicy struct {
	Pattern      string `yaml:"pattern"`
	Interval     string `yaml:"interval"`
	Priority     int    `yaml:"priority"`
	MirrorGitLab bool   `yaml:"mirror_gitlab"`
}

type SchedulerConfig struct {
	Enabled           bool                 `yaml:"enabled"`
	Tick              string               `yaml:"tick"`
	DiscoveryInterval string               `yaml:"discovery_interval"`
	DefaultInterval   string               `yaml:"default_interval"`
	Policies          []RepoSchedulePolicy `yaml:"policies"`
}

type GenerationConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Root        string `yaml:"root"`
	RestoreRoot string `yaml:"restore_root"`
	KeepPerRepo int    `yaml:"keep_per_repo"`
}

type MirroringConfig struct {
	Enabled     bool `yaml:"enabled"`
	AfterBackup bool `yaml:"after_backup"`
}

type AgentConfig struct {
	Enabled            bool              `yaml:"enabled"`
	Listen             string            `yaml:"listen"`
	ServerURL          string            `yaml:"server_url"`
	CAPath             string            `yaml:"ca_path"`
	CAKeyPath          string            `yaml:"ca_key_path"`
	ServerCertPath     string            `yaml:"server_cert_path"`
	ServerKeyPath      string            `yaml:"server_key_path"`
	ClientCertPath     string            `yaml:"client_cert_path"`
	ClientKeyPath      string            `yaml:"client_key_path"`
	AgentName          string            `yaml:"agent_name"`
	Heartbeat          string            `yaml:"heartbeat"`
	ReplicationKeyPath string            `yaml:"replication_key_path"`
	Labels             map[string]string `yaml:"labels,omitempty"`
}

type ReplicationConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Factor             int      `yaml:"factor"`
	MinHealthy         int      `yaml:"min_healthy"`
	ReconcileInterval  string   `yaml:"reconcile_interval"`
	AgentTimeout       string   `yaml:"agent_timeout"`
	TransferTTL        string   `yaml:"transfer_ttl"`
	SpoolRoot          string   `yaml:"spool_root"`
	LocalKeyPath       string   `yaml:"local_key_path"`
	IncludeLocal       bool     `yaml:"include_local"`
	MaxTransferBytes   int64    `yaml:"max_transfer_bytes"`
	FailureDomainLabel string   `yaml:"failure_domain_label,omitempty"`
	MinFailureDomains  int      `yaml:"min_failure_domains,omitempty"`
	AllowedAgents      []string `yaml:"allowed_agents,omitempty"`
	ExcludedAgents     []string `yaml:"excluded_agents,omitempty"`
}

type StorageDataConfig struct {
	Enabled                 bool                `yaml:"enabled"`
	MinFreeBytes            int64               `yaml:"min_free_bytes"`
	MinFreePercent          float64             `yaml:"min_free_percent"`
	MaxProbe                string              `yaml:"max_probe"`
	EvacuateDegraded        bool                `yaml:"evacuate_degraded"`
	InventoryEnabled        bool                `yaml:"inventory_enabled"`
	InventoryInterval       string              `yaml:"inventory_interval"`
	ChunkBytes              int64               `yaml:"chunk_bytes"`
	ChunkRetries            int                 `yaml:"chunk_retries"`
	BandwidthLimitMbps      int                 `yaml:"bandwidth_limit_mbps"`
	ObjectReplicationFactor int                 `yaml:"object_replication_factor"`
	ObjectPoolLabel         string              `yaml:"object_pool_label"`
	DiskTelemetry           DiskTelemetryConfig `yaml:"disk_telemetry"`
	Scrub                   ScrubConfig         `yaml:"scrub"`
	Tiering                 TieringConfig       `yaml:"tiering"`
	Erasure                 ErasureConfig       `yaml:"erasure"`
}

type DiskTelemetryConfig struct {
	Enabled           bool    `yaml:"enabled"`
	Interval          string  `yaml:"interval"`
	SmartctlPath      string  `yaml:"smartctl_path"`
	NVMePath          string  `yaml:"nvme_path"`
	Device            string  `yaml:"device"`
	MaxTemperatureC   float64 `yaml:"max_temperature_c"`
	MaxPercentageUsed float64 `yaml:"max_percentage_used"`
	MaxMediaErrors    int64   `yaml:"max_media_errors"`
	RiskThreshold     int     `yaml:"risk_threshold"`
}

type ScrubConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Interval      string `yaml:"interval"`
	SampleObjects int    `yaml:"sample_objects"`
	Repair        bool   `yaml:"repair"`
	SeedSalt      string `yaml:"seed_salt"`
}

type TieringConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ColdRoot     string `yaml:"cold_root"`
	MinAge       string `yaml:"min_age"`
	MinBytes     int64  `yaml:"min_bytes"`
	RcloneRemote string `yaml:"rclone_remote"`
}

type ErasureConfig struct {
	Enabled            bool   `yaml:"enabled"`
	MinObjectBytes     int64  `yaml:"min_object_bytes"`
	DataShards         int    `yaml:"data_shards"`
	ParityShards       int    `yaml:"parity_shards"`
	BlockBytes         int    `yaml:"block_bytes"`
	Distributed        bool   `yaml:"distributed"`
	ShardReplication   int    `yaml:"shard_replication"`
	FailureDomainLabel string `yaml:"failure_domain_label"`
	MinFailureDomains  int    `yaml:"min_failure_domains"`
	ShardPoolLabel     string `yaml:"shard_pool_label"`
	ReconcileInterval  string `yaml:"reconcile_interval"`
}

type WebAuthConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Mode            string   `yaml:"mode"`
	Issuer          string   `yaml:"issuer"`
	ClientID        string   `yaml:"client_id"`
	ClientSecretEnv string   `yaml:"client_secret_env"`
	RedirectURL     string   `yaml:"redirect_url"`
	SessionKeyEnv   string   `yaml:"session_key_env"`
	GroupClaim      string   `yaml:"group_claim"`
	Scopes          []string `yaml:"scopes,omitempty"`
	StepUpACRValues []string `yaml:"step_up_acr_values,omitempty"`
	ViewerGroups    []string `yaml:"viewer_groups,omitempty"`
	OperatorGroups  []string `yaml:"operator_groups,omitempty"`
	AdminGroups     []string `yaml:"admin_groups,omitempty"`
	RequiredAMR     []string `yaml:"required_amr,omitempty"`
	SecureCookies   bool     `yaml:"secure_cookies"`
}

type RestoreApprovalConfig struct {
	Enabled                 bool     `yaml:"enabled"`
	ApprovalTTL             string   `yaml:"approval_ttl"`
	RequireDistinctApprover bool     `yaml:"require_distinct_approver"`
	Requesters              []string `yaml:"requesters,omitempty"`
	Approvers               []string `yaml:"approvers,omitempty"`
}

type RecoveryDrillConfig struct {
	Enabled       bool   `yaml:"enabled"`
	SampleSize    int    `yaml:"sample_size"`
	WorkDir       string `yaml:"work_dir"`
	VerifyRefs    bool   `yaml:"verify_refs"`
	KeepOnFailure bool   `yaml:"keep_on_failure"`
}

type ObservabilityConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

type NotificationConfig struct {
	Enabled        bool   `yaml:"enabled"`
	OnSuccess      bool   `yaml:"on_success"`
	OnFailure      bool   `yaml:"on_failure"`
	WebhookEnv     string `yaml:"webhook_env"`
	TelegramToken  string `yaml:"telegram_token_env"`
	TelegramChatID string `yaml:"telegram_chat_id_env"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Version: 8,
		GitHub: GitHubConfig{
			APIURL:           "https://api.github.com",
			GraphQLURL:       "https://api.github.com/graphql",
			TokenEnv:         "GITHUB_TOKEN",
			CloneProtocol:    "https",
			Metadata:         "full",
			IncludeForks:     true,
			IncludeArchived:  true,
			ReleaseAssets:    true,
			Discussions:      true,
			Packages:         true,
			OCIExport:        false,
			MaxAssetBytes:    2 << 30,
			MaxArtifactBytes: 2 << 30,
			MaxMetadataPages: 100,
			ActionsArtifacts: true,
			ProjectsV2:       true,
		},
		Backup: BackupConfig{
			Root:              filepath.Join(home, ".repoark", "backups"),
			Concurrency:       4,
			CreateBundles:     true,
			FetchLFS:          true,
			VerifyAfterBackup: true,
			KeepManifests:     30,
		},
		GitLab: GitLabConfig{
			Enabled:            false,
			URL:                "http://gitlab.local:8080",
			TokenEnv:           "GITLAB_TOKEN",
			Image:              "gitlab/gitlab-ce:19.2.4-ce.0",
			Hostname:           "gitlab.local",
			HTTPPort:           8080,
			HTTPSPort:          8443,
			SSHPort:            2222,
			DataDir:            filepath.Join(home, ".repoark", "gitlab"),
			Container:          "repoark-gitlab",
			PreserveNamespaces: true,
			RestoreDrill: GitLabRestoreDrillConfig{
				Enabled: false, WorkDir: filepath.Join(home, ".repoark", "gitlab-drills"), HTTPPort: 18080, SSHPort: 12222, Timeout: "20m", KeepOnFailure: true,
			},
		},
		Daemon:        DaemonConfig{Interval: "24h", RunOnStart: true},
		Security:      SecurityConfig{RequireVerification: true, SignManifests: true, SigningKeyPath: filepath.Join(home, ".config", "repoark", "manifest-ed25519.key"), KMSAttestation: KMSAttestationConfig{SigningAlgorithm: "ED25519_SHA_512", RequireValid: true}},
		Offsite:       OffsiteConfig{Backend: "restic", RepositoryEnv: "RESTIC_REPOSITORY", PasswordEnv: "RESTIC_PASSWORD", KeepDaily: 7, KeepWeekly: 8, KeepMonthly: 12, Prune: true},
		RecoveryDrill: RecoveryDrillConfig{Enabled: false, SampleSize: 3, WorkDir: filepath.Join(home, ".repoark", "drills"), VerifyRefs: true, KeepOnFailure: true},
		Observability: ObservabilityConfig{Enabled: false, Listen: "127.0.0.1:9787"},
		Notifications: NotificationConfig{Enabled: false, OnSuccess: false, OnFailure: true, WebhookEnv: "REPOARK_WEBHOOK_URL", TelegramToken: "REPOARK_TELEGRAM_TOKEN", TelegramChatID: "REPOARK_TELEGRAM_CHAT_ID"},
		Fleet:         FleetConfig{Enabled: false, Concurrency: 2},
		Audit:         AuditConfig{Enabled: true, Required: false, Path: filepath.Join(home, ".repoark", "audit", "ledger.jsonl")},
		CAS:           CASConfig{Enabled: true, Root: filepath.Join(home, ".repoark", "cas"), MinFileSize: 1 << 20, AutoCompact: true},
		Policy:        PolicyConfig{Enabled: true, EnforceInHealth: true, MaxBackupAge: "30h", MaxFailedRepositories: 0, MaxWarnings: -1, RequireSignedManifest: true, RequireAudit: false},
		Packages:      PackagePayloadConfig{Enabled: false, NPM: true, NuGet: true, Maven: true, RubyGems: true, MaxBytes: 2 << 30, NPMRegistry: "https://npm.pkg.github.com", NuGetRegistry: "https://nuget.pkg.github.com", MavenRegistry: "https://maven.pkg.github.com", RubyGemsRegistry: "https://rubygems.pkg.github.com"},
		ControlPlane: ControlPlaneConfig{
			Enabled:     false,
			Store:       StoreConfig{Driver: "sqlite", SQLitePath: filepath.Join(home, ".repoark", "control", "repoark.db"), DSNEnv: "REPOARK_DATABASE_URL"},
			Workers:     WorkerConfig{Concurrency: 2, PollInterval: "2s", Lease: "2m", MaxAttempts: 5},
			Scheduler:   SchedulerConfig{Enabled: true, Tick: "30s", DiscoveryInterval: "30m", DefaultInterval: "24h", Policies: []RepoSchedulePolicy{{Pattern: "*", Interval: "24h", Priority: 50, MirrorGitLab: true}}},
			Generations: GenerationConfig{Enabled: true, Root: filepath.Join(home, ".repoark", "generations"), RestoreRoot: filepath.Join(home, ".repoark", "recovery"), KeepPerRepo: 14},
			Mirroring:   MirroringConfig{Enabled: false, AfterBackup: true},
			Agents:      AgentConfig{Enabled: false, Listen: "127.0.0.1:9790", ServerURL: "https://127.0.0.1:9790", CAPath: filepath.Join(home, ".config", "repoark", "pki", "ca.pem"), CAKeyPath: filepath.Join(home, ".config", "repoark", "pki", "ca-key.pem"), ServerCertPath: filepath.Join(home, ".config", "repoark", "pki", "server.pem"), ServerKeyPath: filepath.Join(home, ".config", "repoark", "pki", "server-key.pem"), ClientCertPath: filepath.Join(home, ".config", "repoark", "pki", "agent.pem"), ClientKeyPath: filepath.Join(home, ".config", "repoark", "pki", "agent-key.pem"), AgentName: "local-agent", Heartbeat: "15s", Labels: map[string]string{"role": "backup-worker"}, ReplicationKeyPath: filepath.Join(home, ".config", "repoark", "pki", "replication-x25519.key")},
			Replication: ReplicationConfig{Enabled: false, Factor: 2, MinHealthy: 2, ReconcileInterval: "1m", AgentTimeout: "2m", TransferTTL: "2h", SpoolRoot: filepath.Join(home, ".repoark", "replication-spool"), LocalKeyPath: filepath.Join(home, ".config", "repoark", "pki", "replication-local-x25519.key"), IncludeLocal: true, MaxTransferBytes: 20 << 30},
			RestoreAuth: RestoreApprovalConfig{Enabled: false, ApprovalTTL: "30m", RequireDistinctApprover: true},
			Storage:     StorageDataConfig{Enabled: true, MinFreeBytes: 10 << 30, MinFreePercent: 10, MaxProbe: "3s", EvacuateDegraded: true, InventoryEnabled: true, InventoryInterval: "10m", ChunkBytes: 8 << 20, ChunkRetries: 5, BandwidthLimitMbps: 0, ObjectReplicationFactor: 0, ObjectPoolLabel: "cas_pool", DiskTelemetry: DiskTelemetryConfig{Enabled: false, Interval: "15m", SmartctlPath: "smartctl", NVMePath: "nvme", MaxTemperatureC: 70, MaxPercentageUsed: 90, MaxMediaErrors: 0, RiskThreshold: 60}, Scrub: ScrubConfig{Enabled: false, Interval: "24h", SampleObjects: 100, Repair: true}, Tiering: TieringConfig{Enabled: false, ColdRoot: filepath.Join(home, ".repoark", "cold-cas"), MinAge: "168h", MinBytes: 64 << 20}, Erasure: ErasureConfig{Enabled: false, MinObjectBytes: 4 << 30, DataShards: 6, ParityShards: 3, BlockBytes: 1 << 20, Distributed: false, ShardReplication: 1, FailureDomainLabel: "zone", MinFailureDomains: 2, ShardPoolLabel: "cas_pool", ReconcileInterval: "5m"}},
			WebAuth:     WebAuthConfig{Enabled: false, Mode: "oidc", ClientSecretEnv: "REPOARK_OIDC_CLIENT_SECRET", SessionKeyEnv: "REPOARK_SESSION_KEY", GroupClaim: "groups", Scopes: []string{"profile", "email", "groups"}, SecureCookies: true},
		},
	}
}

func DefaultPath() string {
	d, err := os.UserConfigDir()
	if err != nil || d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "repoark", "config.yml")
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg.ExpandPaths()
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	// Backward compatibility with v1/v2 configs. New fields keep safe defaults
	// because we unmarshal on top of Default().
	if cfg.Version < 8 {
		cfg.Version = 8
	}
	cfg.ExpandPaths()
	return cfg, cfg.Validate()
}

func Save(path string, cfg Config) error {
	if path == "" {
		path = DefaultPath()
	}
	cfg.ExpandPaths()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (c *Config) ExpandPaths() {
	c.Backup.Root = expand(c.Backup.Root)
	c.GitLab.DataDir = expand(c.GitLab.DataDir)
	c.Security.SigningKeyPath = expand(c.Security.SigningKeyPath)
	c.RecoveryDrill.WorkDir = expand(c.RecoveryDrill.WorkDir)
	c.GitLab.RestoreDrill.WorkDir = expand(c.GitLab.RestoreDrill.WorkDir)
	c.Audit.Path = expand(c.Audit.Path)
	c.CAS.Root = expand(c.CAS.Root)
	c.ControlPlane.Store.SQLitePath = expand(c.ControlPlane.Store.SQLitePath)
	c.ControlPlane.Generations.Root = expand(c.ControlPlane.Generations.Root)
	c.ControlPlane.Generations.RestoreRoot = expand(c.ControlPlane.Generations.RestoreRoot)
	c.ControlPlane.Agents.CAPath = expand(c.ControlPlane.Agents.CAPath)
	c.ControlPlane.Agents.CAKeyPath = expand(c.ControlPlane.Agents.CAKeyPath)
	c.ControlPlane.Agents.ServerCertPath = expand(c.ControlPlane.Agents.ServerCertPath)
	c.ControlPlane.Agents.ServerKeyPath = expand(c.ControlPlane.Agents.ServerKeyPath)
	c.ControlPlane.Agents.ClientCertPath = expand(c.ControlPlane.Agents.ClientCertPath)
	c.ControlPlane.Agents.ClientKeyPath = expand(c.ControlPlane.Agents.ClientKeyPath)
	c.ControlPlane.Agents.ReplicationKeyPath = expand(c.ControlPlane.Agents.ReplicationKeyPath)
	c.ControlPlane.Replication.SpoolRoot = expand(c.ControlPlane.Replication.SpoolRoot)
	c.ControlPlane.Replication.LocalKeyPath = expand(c.ControlPlane.Replication.LocalKeyPath)
	c.ControlPlane.Storage.Tiering.ColdRoot = expand(c.ControlPlane.Storage.Tiering.ColdRoot)
	for i := range c.Fleet.Accounts {
		c.Fleet.Accounts[i].BackupRoot = expand(c.Fleet.Accounts[i].BackupRoot)
	}
}

func expand(s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(s, `~\`)) {
		home, _ := os.UserHomeDir()
		if s == "~" {
			return home
		}
		return filepath.Join(home, s[2:])
	}
	return os.ExpandEnv(s)
}

func (c Config) Validate() error {
	if c.Backup.Root == "" {
		return errors.New("backup.root is empty")
	}
	if c.Backup.Concurrency < 1 || c.Backup.Concurrency > 32 {
		return errors.New("backup.concurrency must be 1..32")
	}
	if c.Backup.KeepManifests < 0 {
		return errors.New("backup.keep_manifests must be >= 0")
	}
	if c.GitHub.APIURL == "" || c.GitHub.GraphQLURL == "" {
		return errors.New("github api_url/graphql_url is empty")
	}
	if c.GitHub.CloneProtocol != "https" && c.GitHub.CloneProtocol != "ssh" {
		return errors.New("github.clone_protocol must be https or ssh")
	}
	if c.GitHub.Metadata != "none" && c.GitHub.Metadata != "basic" && c.GitHub.Metadata != "full" {
		return errors.New("github.metadata must be none, basic or full")
	}
	if c.GitHub.MaxAssetBytes < 0 {
		return errors.New("github.max_asset_bytes must be >= 0")
	}
	if c.GitHub.MaxArtifactBytes < 0 {
		return errors.New("github.max_artifact_bytes must be >= 0")
	}
	if c.GitHub.MaxMetadataPages < 1 || c.GitHub.MaxMetadataPages > 1000 {
		return errors.New("github.max_metadata_pages must be 1..1000")
	}
	if c.GitLab.Enabled {
		if c.GitLab.URL == "" || c.GitLab.Image == "" || c.GitLab.DataDir == "" {
			return errors.New("gitlab url/image/data_dir required")
		}
		for name, port := range map[string]int{"http_port": c.GitLab.HTTPPort, "https_port": c.GitLab.HTTPSPort, "ssh_port": c.GitLab.SSHPort} {
			if port < 1 || port > 65535 {
				return fmt.Errorf("gitlab.%s must be 1..65535", name)
			}
		}
	}
	if c.Security.RequireVerification && !c.Backup.VerifyAfterBackup {
		return errors.New("security.require_verification requires backup.verify_after_backup: true")
	}
	if c.Security.SignManifests && c.Security.SigningKeyPath == "" {
		return errors.New("security.signing_key_path is required when sign_manifests is enabled")
	}
	if c.Security.KMSAttestation.Enabled {
		if strings.TrimSpace(c.Security.KMSAttestation.KeyID) == "" {
			return errors.New("security.kms_attestation.key_id is required")
		}
		if strings.ToUpper(strings.TrimSpace(c.Security.KMSAttestation.SigningAlgorithm)) != "ED25519_SHA_512" {
			return errors.New("security.kms_attestation currently requires ED25519_SHA_512 (RAW message mode)")
		}
	}
	if c.Offsite.Enabled {
		switch c.Offsite.Backend {
		case "restic":
			if c.Offsite.RepositoryEnv == "" {
				return errors.New("offsite.repository_env is required for restic")
			}
		case "rclone":
			if strings.TrimSpace(c.Offsite.RcloneRemote) == "" {
				return errors.New("offsite.rclone_remote is required for rclone")
			}
		default:
			return errors.New("offsite.backend must be restic or rclone")
		}
	}
	if c.RecoveryDrill.SampleSize < 0 {
		return errors.New("recovery_drill.sample_size must be >= 0")
	}
	if c.Observability.Enabled && strings.TrimSpace(c.Observability.Listen) == "" {
		return errors.New("observability.listen is required when enabled")
	}
	if c.Audit.Enabled && strings.TrimSpace(c.Audit.Path) == "" {
		return errors.New("audit.path is required when audit is enabled")
	}
	if c.CAS.Enabled {
		if strings.TrimSpace(c.CAS.Root) == "" {
			return errors.New("cas.root is required when cas is enabled")
		}
		if c.CAS.MinFileSize < 0 {
			return errors.New("cas.min_file_size must be >= 0")
		}
	}
	if c.Packages.MaxBytes < 0 {
		return errors.New("package_payloads.max_bytes must be >= 0")
	}
	if c.Policy.Enabled {
		for name, value := range map[string]string{
			"max_backup_age":              c.Policy.MaxBackupAge,
			"max_recovery_drill_age":      c.Policy.MaxRecoveryDrillAge,
			"max_gitlab_drill_age":        c.Policy.MaxGitLabDrillAge,
			"max_offsite_age":             c.Policy.MaxOffsiteAge,
			"max_recovery_drill_duration": c.Policy.MaxRecoveryDrillDuration,
			"max_gitlab_drill_duration":   c.Policy.MaxGitLabDrillDuration,
		} {
			if strings.TrimSpace(value) == "" {
				continue
			}
			if d, err := time.ParseDuration(value); err != nil || d <= 0 {
				return fmt.Errorf("policy.%s must be a positive duration", name)
			}
		}
		if c.Policy.MaxFailedRepositories < 0 {
			return errors.New("policy.max_failed_repositories must be >= 0")
		}
	}
	if c.Fleet.Enabled {
		if c.Fleet.Concurrency < 1 || c.Fleet.Concurrency > 16 {
			return errors.New("fleet.concurrency must be 1..16")
		}
		seen := map[string]bool{}
		for i, a := range c.Fleet.Accounts {
			if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.TokenEnv) == "" {
				return fmt.Errorf("fleet.accounts[%d] requires name and token_env", i)
			}
			if seen[a.Name] {
				return fmt.Errorf("duplicate fleet account name %q", a.Name)
			}
			seen[a.Name] = true
		}
	}
	if c.ControlPlane.Enabled {
		switch strings.ToLower(strings.TrimSpace(c.ControlPlane.Store.Driver)) {
		case "sqlite":
			if strings.TrimSpace(c.ControlPlane.Store.SQLitePath) == "" {
				return errors.New("control_plane.store.sqlite_path is required")
			}
		case "postgres", "postgresql":
			if strings.TrimSpace(c.ControlPlane.Store.DSNEnv) == "" {
				return errors.New("control_plane.store.dsn_env is required for postgres")
			}
		default:
			return errors.New("control_plane.store.driver must be sqlite or postgres")
		}
		if c.ControlPlane.Workers.Concurrency < 1 || c.ControlPlane.Workers.Concurrency > 64 {
			return errors.New("control_plane.workers.concurrency must be 1..64")
		}
		if c.ControlPlane.Workers.MaxAttempts < 1 || c.ControlPlane.Workers.MaxAttempts > 100 {
			return errors.New("control_plane.workers.max_attempts must be 1..100")
		}
		for name, value := range map[string]string{"workers.poll_interval": c.ControlPlane.Workers.PollInterval, "workers.lease": c.ControlPlane.Workers.Lease, "scheduler.tick": c.ControlPlane.Scheduler.Tick, "scheduler.discovery_interval": c.ControlPlane.Scheduler.DiscoveryInterval, "scheduler.default_interval": c.ControlPlane.Scheduler.DefaultInterval, "agents.heartbeat": c.ControlPlane.Agents.Heartbeat} {
			if strings.TrimSpace(value) == "" {
				continue
			}
			if d, err := time.ParseDuration(value); err != nil || d <= 0 {
				return fmt.Errorf("control_plane.%s must be a positive duration", name)
			}
		}
		if c.ControlPlane.Generations.Enabled {
			if strings.TrimSpace(c.ControlPlane.Generations.Root) == "" {
				return errors.New("control_plane.generations.root is required")
			}
			if c.ControlPlane.Generations.KeepPerRepo < 1 {
				return errors.New("control_plane.generations.keep_per_repo must be >= 1")
			}
		}
		for i, p := range c.ControlPlane.Scheduler.Policies {
			if strings.TrimSpace(p.Pattern) == "" {
				return fmt.Errorf("control_plane.scheduler.policies[%d].pattern is empty", i)
			}
			if d, err := time.ParseDuration(p.Interval); err != nil || d <= 0 {
				return fmt.Errorf("control_plane.scheduler.policies[%d].interval must be positive", i)
			}
		}
		if c.ControlPlane.Replication.Enabled {
			r := c.ControlPlane.Replication
			if r.Factor < 2 || r.Factor > 32 {
				return errors.New("control_plane.replication.factor must be 2..32")
			}
			if r.MinHealthy < 1 || r.MinHealthy > r.Factor {
				return errors.New("control_plane.replication.min_healthy must be 1..factor")
			}
			if strings.TrimSpace(r.FailureDomainLabel) == "" {
				if r.MinFailureDomains != 0 {
					return errors.New("control_plane.replication.min_failure_domains requires failure_domain_label")
				}
			} else if r.MinFailureDomains < 1 || r.MinFailureDomains > r.Factor {
				return errors.New("control_plane.replication.min_failure_domains must be 1..factor")
			}
			if strings.TrimSpace(r.SpoolRoot) == "" || r.MaxTransferBytes <= 0 {
				return errors.New("control_plane.replication spool_root and max_transfer_bytes are required")
			}
			for name, value := range map[string]string{"replication.reconcile_interval": r.ReconcileInterval, "replication.agent_timeout": r.AgentTimeout, "replication.transfer_ttl": r.TransferTTL} {
				if d, err := time.ParseDuration(value); err != nil || d <= 0 {
					return fmt.Errorf("control_plane.%s must be a positive duration", name)
				}
			}
			if r.IncludeLocal && strings.TrimSpace(r.LocalKeyPath) == "" {
				return errors.New("control_plane.replication.local_key_path is required when include_local=true")
			}
		}
		if c.ControlPlane.Storage.Enabled {
			s := c.ControlPlane.Storage
			if s.MinFreeBytes < 0 || s.MinFreePercent < 0 || s.MinFreePercent > 100 {
				return errors.New("control_plane.storage free-space thresholds are invalid")
			}
			if d, err := time.ParseDuration(s.MaxProbe); err != nil || d <= 0 {
				return errors.New("control_plane.storage.max_probe must be positive")
			}
			if d, err := time.ParseDuration(s.InventoryInterval); err != nil || d <= 0 {
				return errors.New("control_plane.storage.inventory_interval must be positive")
			}
			if s.ChunkBytes < 64<<10 || s.ChunkBytes > 256<<20 {
				return errors.New("control_plane.storage.chunk_bytes must be 64KiB..256MiB")
			}
			if s.ChunkRetries < 1 || s.ChunkRetries > 20 {
				return errors.New("control_plane.storage.chunk_retries must be 1..20")
			}
			if s.BandwidthLimitMbps < 0 {
				return errors.New("control_plane.storage.bandwidth_limit_mbps must be >= 0")
			}
			if s.ObjectReplicationFactor < 0 || s.ObjectReplicationFactor > 32 {
				return errors.New("control_plane.storage.object_replication_factor must be 0..32")
			}
			if s.ObjectReplicationFactor > 0 {
				if !c.ControlPlane.Replication.Enabled {
					return errors.New("control_plane.storage.object_replication_factor requires control_plane.replication.enabled")
				}
				if !c.ControlPlane.Agents.Enabled {
					return errors.New("control_plane.storage.object_replication_factor requires control_plane.agents.enabled")
				}
			}
			if s.DiskTelemetry.Enabled {
				if d, err := time.ParseDuration(s.DiskTelemetry.Interval); err != nil || d <= 0 {
					return errors.New("control_plane.storage.disk_telemetry.interval must be positive")
				}
				if s.DiskTelemetry.RiskThreshold < 1 || s.DiskTelemetry.RiskThreshold > 100 {
					return errors.New("control_plane.storage.disk_telemetry.risk_threshold must be 1..100")
				}
			}
			if s.Scrub.Enabled {
				if d, err := time.ParseDuration(s.Scrub.Interval); err != nil || d <= 0 {
					return errors.New("control_plane.storage.scrub.interval must be positive")
				}
				if s.Scrub.SampleObjects < 1 {
					return errors.New("control_plane.storage.scrub.sample_objects must be >= 1")
				}
			}
			if s.Tiering.Enabled {
				if strings.TrimSpace(s.Tiering.ColdRoot) == "" && strings.TrimSpace(s.Tiering.RcloneRemote) == "" {
					return errors.New("control_plane.storage.tiering requires cold_root or rclone_remote")
				}
				if d, err := time.ParseDuration(s.Tiering.MinAge); err != nil || d < 0 {
					return errors.New("control_plane.storage.tiering.min_age must be non-negative")
				}
				if s.Tiering.MinBytes < 0 {
					return errors.New("control_plane.storage.tiering.min_bytes must be >= 0")
				}
			}
			if s.Erasure.Enabled {
				if s.Erasure.DataShards < 2 || s.Erasure.DataShards > 32 || s.Erasure.ParityShards < 1 || s.Erasure.ParityShards > 16 || s.Erasure.DataShards+s.Erasure.ParityShards > 64 {
					return errors.New("control_plane.storage.erasure shard counts are invalid")
				}
				if s.Erasure.MinObjectBytes < 1<<20 || s.Erasure.BlockBytes < 64<<10 {
					return errors.New("control_plane.storage.erasure size thresholds are invalid")
				}
				if s.Erasure.Distributed {
					if !c.ControlPlane.Replication.Enabled || !c.ControlPlane.Agents.Enabled {
						return errors.New("distributed erasure requires replication and agents")
					}
					if s.Erasure.ShardReplication < 1 || s.Erasure.ShardReplication > 8 {
						return errors.New("control_plane.storage.erasure.shard_replication must be 1..8")
					}
					if strings.TrimSpace(s.Erasure.FailureDomainLabel) == "" {
						return errors.New("distributed erasure requires failure_domain_label")
					}
					if s.Erasure.MinFailureDomains < 1 {
						return errors.New("distributed erasure min_failure_domains must be >= 1")
					}
					if d, err := time.ParseDuration(s.Erasure.ReconcileInterval); err != nil || d <= 0 {
						return errors.New("distributed erasure reconcile_interval must be positive")
					}
				}
			}
		}
		if c.ControlPlane.WebAuth.Enabled {
			a := c.ControlPlane.WebAuth
			if strings.ToLower(strings.TrimSpace(a.Mode)) != "oidc" {
				return errors.New("control_plane.web_auth.mode must be oidc")
			}
			if strings.TrimSpace(a.Issuer) == "" || strings.TrimSpace(a.ClientID) == "" || strings.TrimSpace(a.RedirectURL) == "" || strings.TrimSpace(a.ClientSecretEnv) == "" || strings.TrimSpace(a.SessionKeyEnv) == "" {
				return errors.New("control_plane.web_auth OIDC issuer/client_id/redirect_url/secret env/session env are required")
			}
			if len(a.ViewerGroups)+len(a.OperatorGroups)+len(a.AdminGroups) == 0 {
				return errors.New("control_plane.web_auth requires at least one viewer/operator/admin group")
			}
		}
		if c.ControlPlane.RestoreAuth.Enabled {
			if d, err := time.ParseDuration(c.ControlPlane.RestoreAuth.ApprovalTTL); err != nil || d <= 0 {
				return errors.New("control_plane.restore_approval.approval_ttl must be positive")
			}
		}
		if c.ControlPlane.Agents.Enabled {
			if strings.TrimSpace(c.ControlPlane.Agents.Listen) == "" {
				return errors.New("control_plane.agents.listen is required")
			}
			if strings.TrimSpace(c.ControlPlane.Agents.CAPath) == "" || strings.TrimSpace(c.ControlPlane.Agents.ServerCertPath) == "" || strings.TrimSpace(c.ControlPlane.Agents.ServerKeyPath) == "" {
				return errors.New("control_plane.agents CA/server certificate paths are required")
			}
		}
	}
	if c.Offsite.ObjectLock.Enabled {
		if strings.TrimSpace(c.Offsite.ObjectLock.Bucket) == "" {
			return errors.New("offsite.object_lock.bucket is required")
		}
		mode := strings.ToUpper(strings.TrimSpace(c.Offsite.ObjectLock.ExpectedMode))
		if mode != "" && mode != "GOVERNANCE" && mode != "COMPLIANCE" {
			return errors.New("offsite.object_lock.expected_mode must be GOVERNANCE or COMPLIANCE")
		}
		if c.Offsite.ObjectLock.MinRetentionDays < 0 {
			return errors.New("offsite.object_lock.min_retention_days must be >= 0")
		}
	}
	if c.GitLab.RestoreDrill.Enabled {
		if strings.TrimSpace(c.GitLab.RestoreDrill.WorkDir) == "" {
			return errors.New("gitlab.restore_drill.work_dir is required")
		}
		if c.GitLab.RestoreDrill.HTTPPort < 1 || c.GitLab.RestoreDrill.HTTPPort > 65535 || c.GitLab.RestoreDrill.SSHPort < 1 || c.GitLab.RestoreDrill.SSHPort > 65535 {
			return errors.New("gitlab.restore_drill ports must be 1..65535")
		}
		if d, err := time.ParseDuration(c.GitLab.RestoreDrill.Timeout); err != nil || d <= 0 {
			return errors.New("gitlab.restore_drill.timeout must be a positive duration")
		}
	}
	d, err := time.ParseDuration(c.Daemon.Interval)
	if err != nil {
		return fmt.Errorf("daemon.interval: %w", err)
	}
	if d <= 0 {
		return errors.New("daemon.interval must be greater than zero")
	}
	return nil
}
