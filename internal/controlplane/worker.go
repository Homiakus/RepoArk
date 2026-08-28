package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/backup"
	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/cassync"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/erasure"
	"github.com/Homiakus/repoark/internal/generation"
	"github.com/Homiakus/repoark/internal/gitlab"
	"github.com/Homiakus/repoark/internal/manifest"
	"github.com/Homiakus/repoark/internal/replication"
)

type Runner struct {
	Store  Store
	Config config.Config
	Emit   func(string)
}

type backupPayload struct {
	RepositoryID string `json:"repository_id"`
	FullName     string `json:"full_name"`
}

func (r Runner) Run(ctx context.Context, job Job) error {
	switch job.Kind {
	case "backup-repo":
		return r.backupRepository(ctx, job)
	case "mirror-gitlab":
		return r.mirrorRepository(ctx, job)
	case "restore-generation":
		return r.restoreGeneration(ctx, job)
	case "replicate-generation":
		return r.replicateGeneration(ctx, job)
	case "install-replica":
		return r.installReplica(ctx, job)
	case "replicate-cas":
		return r.replicateCAS(ctx, job)
	case "install-cas":
		return r.installCAS(ctx, job)
	case "protect-erasure":
		return r.protectErasure(ctx, job)
	case "scrub-cas":
		return r.scrubCAS(ctx, job)
	case "tier-cas":
		return r.tierCAS(ctx, job)
	case "repair-object":
		return r.repairObject(ctx, job)
	default:
		return fmt.Errorf("unsupported job kind %q", job.Kind)
	}
}

func (r Runner) backupRepository(ctx context.Context, job Job) error {
	var p backupPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return err
	}
	if p.FullName == "" {
		p.FullName = job.Target
	}
	mgr := backup.New(r.Config)
	res, err := mgr.BackupRepository(ctx, p.FullName, func(e backup.Event) {
		if r.Emit != nil {
			r.Emit(fmt.Sprintf("%s %s %s", e.Repo, e.Stage, e.Message))
		}
	})
	if err != nil {
		_ = r.Store.MarkBackupResult(ctx, p.RepositoryID, false, "", time.Now().UTC())
		return err
	}
	generationID := ""
	if r.Config.ControlPlane.Generations.Enabled {
		var meta generation.Meta
		var err error
		if r.Config.Security.SignManifests {
			meta, err = generation.CaptureSigned(r.Config.Backup.Root, r.Config.ControlPlane.Generations.Root, res, r.Config.ControlPlane.Generations.KeepPerRepo, r.Config.Security.SigningKeyPath)
		} else {
			meta, err = generation.Capture(r.Config.Backup.Root, r.Config.ControlPlane.Generations.Root, res, r.Config.ControlPlane.Generations.KeepPerRepo)
		}
		if err != nil {
			return err
		}
		generationID = meta.ID
		ownerRepo := filepath.Join(r.Config.ControlPlane.Generations.Root, filepath.FromSlash(p.FullName), meta.ID, "generation.json")
		g := Generation{ID: meta.ID, RepositoryID: p.RepositoryID, Repository: p.FullName, MetaPath: ownerRepo, CreatedAt: meta.CreatedAt, Verified: meta.Verified, BundleSHA256: meta.BundleSHA256, LFSSHA256: meta.LFSSHA256}
		if err := r.Store.RecordGeneration(ctx, g); err != nil {
			return err
		}
		replicaAgent := job.LeaseOwner
		if replicaAgent == "" {
			replicaAgent = LocalWorkerAffinity
		}
		if err := r.Store.RecordReplica(ctx, GenerationReplica{GenerationID: g.ID, RepositoryID: g.RepositoryID, AgentID: replicaAgent, MetaPath: ownerRepo, State: ReplicaReady, VerifiedAt: time.Now().UTC()}); err != nil {
			return err
		}
		if err := r.Store.PruneGenerationRecords(ctx, p.RepositoryID, r.Config.ControlPlane.Generations.KeepPerRepo); err != nil {
			return err
		}
	}
	if err := r.Store.MarkBackupResult(ctx, p.RepositoryID, true, generationID, time.Now().UTC()); err != nil {
		return err
	}
	if r.Config.ControlPlane.Mirroring.Enabled && r.Config.ControlPlane.Mirroring.AfterBackup && r.Config.GitLab.Enabled {
		b, _ := json.Marshal(res)
		_, _, err := r.Store.Enqueue(ctx, Job{Kind: "mirror-gitlab", Target: p.FullName, Payload: string(b), Affinity: job.LeaseOwner, Priority: job.Priority, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: time.Now().UTC()})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r Runner) mirrorRepository(ctx context.Context, job Job) error {
	var res manifest.RepoResult
	if err := json.Unmarshal([]byte(job.Payload), &res); err != nil {
		return err
	}
	if res.FullName == "" {
		res.FullName = job.Target
	}
	return gitlab.New(r.Config).MigrateRepository(ctx, res, func(e gitlab.Event) {
		if r.Emit != nil {
			r.Emit(fmt.Sprintf("%s %s", e.Repo, e.Message))
		}
	})
}

type restoreGenerationPayload struct {
	Repository   string `json:"repository"`
	GenerationID string `json:"generation_id"`
	Target       string `json:"target,omitempty"`
	ApprovalID   string `json:"approval_id,omitempty"`
}

func (r Runner) restoreGeneration(ctx context.Context, job Job) error {
	var p restoreGenerationPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return err
	}
	if p.Repository == "" || p.GenerationID == "" {
		return fmt.Errorf("invalid restore-generation payload")
	}
	target := p.Target
	if target == "" {
		target = filepath.Join(r.Config.ControlPlane.Generations.RestoreRoot, filepath.FromSlash(p.Repository), p.GenerationID)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if r.Config.Security.SignManifests {
		if err := generation.RestoreVerified(ctx, r.Config.ControlPlane.Generations.Root, p.Repository, p.GenerationID, target, r.Config.Security.SigningKeyPath+".pub"); err != nil {
			return err
		}
	} else if err := generation.Restore(ctx, r.Config.ControlPlane.Generations.Root, p.Repository, p.GenerationID, target); err != nil {
		return err
	}
	if r.Emit != nil {
		r.Emit(fmt.Sprintf("%s generation %s restored to %s", p.Repository, p.GenerationID, target))
	}
	return nil
}

type replicationUploadStore interface {
	UploadReplication(context.Context, string, io.Reader) (installReplicaPayload, error)
}
type replicationResumableUploadStore interface {
	UploadReplicationFile(context.Context, string, *os.File, int64, int, int) (installReplicaPayload, error)
}
type replicationDownloadStore interface {
	DownloadReplication(context.Context, string, io.Writer) error
}
type replicationResumableDownloadStore interface {
	DownloadReplicationFile(context.Context, string, string, int64, string, int, int) error
}

func (r Runner) replicateGeneration(ctx context.Context, job Job) error {
	var p replicateGenerationPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return err
	}
	if p.TransferID == "" || p.Repository == "" || p.GenerationID == "" || p.TargetAgent == "" || p.TargetReplicationPubKey == "" {
		return fmt.Errorf("invalid replicate-generation payload")
	}
	_, dir, err := generation.Find(r.Config.ControlPlane.Generations.Root, p.Repository, p.GenerationID)
	if err != nil {
		return err
	}
	if r.Config.Security.SignManifests {
		if _, err := generation.VerifyDir(dir, r.Config.Security.SigningKeyPath+".pub"); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp("", "repoark-repl-*.blob")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	pr, pw := io.Pipe()
	archiveErr := make(chan error, 1)
	go func() { err := replication.ArchiveGeneration(dir, pw); _ = pw.CloseWithError(err); archiveErr <- err }()
	encErr := replication.Encrypt(tmp, pr, p.TargetReplicationPubKey)
	_ = pr.Close()
	arcErr := <-archiveErr
	if encErr != nil {
		_ = tmp.Close()
		return encErr
	}
	if arcErr != nil {
		_ = tmp.Close()
		return arcErr
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		_ = tmp.Close()
		return err
	}
	if remote, ok := r.Store.(replicationResumableUploadStore); ok && r.Config.ControlPlane.Storage.ChunkBytes > 0 {
		_, err = remote.UploadReplicationFile(ctx, p.TransferID, tmp, r.Config.ControlPlane.Storage.ChunkBytes, r.Config.ControlPlane.Storage.ChunkRetries, r.Config.ControlPlane.Storage.BandwidthLimitMbps)
		_ = tmp.Close()
		return err
	}
	if remote, ok := r.Store.(replicationUploadStore); ok {
		_, err = remote.UploadReplication(ctx, p.TransferID, tmp)
		_ = tmp.Close()
		return err
	}
	// Local control-plane worker stages the encrypted relay blob directly and
	// then creates the target-affined installation job.
	if err := os.MkdirAll(r.Config.ControlPlane.Replication.SpoolRoot, 0o700); err != nil {
		_ = tmp.Close()
		return err
	}
	spool := replicationSpoolPath(r.Config.ControlPlane.Replication.SpoolRoot, p.TransferID)
	out, err := os.OpenFile(spool+".part", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(tmp, r.Config.ControlPlane.Replication.MaxTransferBytes+1))
	closeErr := out.Close()
	_ = tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > r.Config.ControlPlane.Replication.MaxTransferBytes {
		_ = os.Remove(spool + ".part")
		return fmt.Errorf("replication transfer exceeds configured maximum")
	}
	if err := os.Rename(spool+".part", spool); err != nil {
		return err
	}
	install := installReplicaPayload{TransferID: p.TransferID, RepositoryID: p.RepositoryID, Repository: p.Repository, GenerationID: p.GenerationID, SourceAgent: p.SourceAgent, TargetAgent: p.TargetAgent, CipherSHA256: hex.EncodeToString(h.Sum(nil)), CipherBytes: n}
	transfer := ReplicationTransfer{ID: p.TransferID, GenerationID: p.GenerationID, RepositoryID: p.RepositoryID, SourceAgent: p.SourceAgent, TargetAgent: p.TargetAgent, SpoolPath: spool, State: TransferReady, Bytes: n, SHA256: install.CipherSHA256, ExpiresAt: replicationTransferExpiry(r.Config.ControlPlane.Replication, time.Now().UTC())}
	if err := r.Store.RecordReplicationTransfer(ctx, transfer); err != nil {
		_ = os.Remove(spool)
		return err
	}
	b, _ := json.Marshal(install)
	_, _, err = r.Store.Enqueue(ctx, Job{Kind: "install-replica", Target: p.GenerationID + "@" + p.TargetAgent, Payload: string(b), Affinity: p.TargetAgent, Priority: job.Priority, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: time.Now().UTC()})
	return err
}

func (r Runner) replicateCAS(ctx context.Context, job Job) error {
	var p replicateCASPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return err
	}
	if p.TransferID == "" || p.SourceAgent == "" || p.TargetAgent == "" || p.TargetReplicationPubKey == "" || p.Factor < 1 {
		return fmt.Errorf("invalid replicate-cas payload")
	}
	tmp, err := os.CreateTemp("", "repoark-cas-repl-*.blob")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	pr, pw := io.Pipe()
	archiveErr := make(chan error, 1)
	go func() {
		var e error
		if len(p.Digests) > 0 {
			_, e = cassync.ArchiveDigests(r.Config.CAS.Root, pw, p.Digests)
		} else {
			_, e = cassync.Archive(r.Config.CAS.Root, pw, p.Prefixes, p.PoolAgents, p.Factor, p.TargetAgent)
		}
		_ = pw.CloseWithError(e)
		archiveErr <- e
	}()
	encErr := replication.Encrypt(tmp, pr, p.TargetReplicationPubKey)
	_ = pr.Close()
	arcErr := <-archiveErr
	if encErr != nil {
		_ = tmp.Close()
		return encErr
	}
	if arcErr != nil {
		_ = tmp.Close()
		return arcErr
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		_ = tmp.Close()
		return err
	}
	remote, ok := r.Store.(replicationResumableUploadStore)
	if !ok {
		return fmt.Errorf("CAS object replication requires resumable remote store")
	}
	_, err = remote.UploadReplicationFile(ctx, p.TransferID, tmp, r.Config.ControlPlane.Storage.ChunkBytes, r.Config.ControlPlane.Storage.ChunkRetries, r.Config.ControlPlane.Storage.BandwidthLimitMbps)
	_ = tmp.Close()
	return err
}

func (r Runner) installCAS(ctx context.Context, job Job) error {
	var p installCASPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return err
	}
	if p.TransferID == "" || p.TargetAgent == "" || p.CipherSHA256 == "" {
		return fmt.Errorf("invalid install-cas payload")
	}
	remote, ok := r.Store.(replicationResumableDownloadStore)
	if !ok {
		return fmt.Errorf("CAS object installation requires resumable remote store")
	}
	transferDir := filepath.Join(r.Config.CAS.Root, ".repoark-transfers")
	if err := os.MkdirAll(transferDir, 0700); err != nil {
		return err
	}
	encPath := filepath.Join(transferDir, p.TransferID+".cipher.part")
	if err := remote.DownloadReplicationFile(ctx, p.TransferID, encPath, p.CipherBytes, p.CipherSHA256, r.Config.ControlPlane.Storage.ChunkRetries, r.Config.ControlPlane.Storage.BandwidthLimitMbps); err != nil {
		return err
	}
	enc, err := os.Open(encPath)
	if err != nil {
		return err
	}
	defer enc.Close()
	plain, err := os.CreateTemp(transferDir, p.TransferID+".tar-")
	if err != nil {
		return err
	}
	plainPath := plain.Name()
	defer os.Remove(plainPath)
	keyPath := r.Config.ControlPlane.Agents.ReplicationKeyPath
	if err := replication.Decrypt(plain, enc, keyPath); err != nil {
		_ = plain.Close()
		return err
	}
	if err := plain.Sync(); err != nil {
		_ = plain.Close()
		return err
	}
	if _, err := plain.Seek(0, 0); err != nil {
		_ = plain.Close()
		return err
	}
	m, err := cassync.Extract(plain, r.Config.CAS.Root)
	_ = plain.Close()
	if err != nil {
		return err
	}
	if p.Erasure != nil {
		found := false
		for _, obj := range m.Objects {
			if strings.EqualFold(obj.Digest, p.Erasure.ShardSHA256) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("erasure shard %s missing from CAS transfer", p.Erasure.ShardSHA256)
		}
		agentID := job.LeaseOwner
		if agentID == "" {
			agentID = p.TargetAgent
		}
		domain := localFailureDomain(r.Config, agentID, r.Config.ControlPlane.Storage.Erasure.FailureDomainLabel)
		owner := fmt.Sprintf("erasure:%s:%d", p.Erasure.ObjectSHA256, p.Erasure.ShardIndex)
		if _, err := r.Store.EnsureObjectRef(ctx, ObjectRef{Digest: p.Erasure.ShardSHA256, Kind: "erasure-shard", Bytes: p.Erasure.ShardBytes}, owner); err != nil {
			return err
		}
		if err := r.Store.RecordErasureShard(ctx, ErasureShard{ObjectSHA256: p.Erasure.ObjectSHA256, ShardIndex: p.Erasure.ShardIndex, ShardSHA256: p.Erasure.ShardSHA256, AgentID: agentID, FailureDomain: domain, State: ShardReady, Bytes: p.Erasure.ShardBytes}); err != nil {
			return err
		}
	}
	_ = os.Remove(encPath)
	if r.Emit != nil {
		r.Emit(fmt.Sprintf("CAS reconciliation installed on %s objects=%d", p.TargetAgent, len(m.Objects)))
	}
	return nil
}

func (r Runner) installReplica(ctx context.Context, job Job) error {
	var p installReplicaPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return err
	}
	if p.TransferID == "" || p.Repository == "" || p.GenerationID == "" || p.TargetAgent == "" {
		return fmt.Errorf("invalid install-replica payload")
	}
	var enc *os.File
	var encName string
	resumable := false
	if remote, ok := r.Store.(replicationResumableDownloadStore); ok && r.Config.ControlPlane.Storage.ChunkBytes > 0 {
		transferDir := filepath.Join(r.Config.ControlPlane.Generations.Root, ".repoark-transfers")
		if err := os.MkdirAll(transferDir, 0o700); err != nil {
			return err
		}
		encName = filepath.Join(transferDir, p.TransferID+".cipher.part")
		if err := remote.DownloadReplicationFile(ctx, p.TransferID, encName, p.CipherBytes, p.CipherSHA256, r.Config.ControlPlane.Storage.ChunkRetries, r.Config.ControlPlane.Storage.BandwidthLimitMbps); err != nil {
			return err
		}
		var err error
		enc, err = os.Open(encName)
		if err != nil {
			return err
		}
		resumable = true
	} else {
		var err error
		enc, err = os.CreateTemp("", "repoark-repl-download-*.blob")
		if err != nil {
			return err
		}
		encName = enc.Name()
		defer os.Remove(encName)
		if remote, ok := r.Store.(replicationDownloadStore); ok {
			if err := remote.DownloadReplication(ctx, p.TransferID, enc); err != nil {
				_ = enc.Close()
				return err
			}
		} else {
			t, err := r.Store.GetReplicationTransfer(ctx, p.TransferID)
			if err != nil {
				_ = enc.Close()
				return err
			}
			if !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(time.Now().UTC()) {
				_ = enc.Close()
				return fmt.Errorf("replication transfer expired")
			}
			if t.TargetAgent != p.TargetAgent || t.GenerationID != p.GenerationID || t.SHA256 != p.CipherSHA256 || t.Bytes != p.CipherBytes {
				_ = enc.Close()
				return fmt.Errorf("replication transfer metadata mismatch")
			}
			in, err := os.Open(replicationSpoolPath(r.Config.ControlPlane.Replication.SpoolRoot, p.TransferID))
			if err != nil {
				_ = enc.Close()
				return err
			}
			_, err = io.Copy(enc, in)
			_ = in.Close()
			if err != nil {
				_ = enc.Close()
				return err
			}
		}
		if err := enc.Sync(); err != nil {
			_ = enc.Close()
			return err
		}
		if _, err := enc.Seek(0, 0); err != nil {
			_ = enc.Close()
			return err
		}
	}
	keyPath := r.Config.ControlPlane.Replication.LocalKeyPath
	if _, ok := r.Store.(*RemoteStore); ok {
		keyPath = r.Config.ControlPlane.Agents.ReplicationKeyPath
	}
	if _, err := replication.EnsureKey(keyPath); err != nil {
		_ = enc.Close()
		return err
	}
	parent := filepath.Join(r.Config.ControlPlane.Generations.Root, filepath.FromSlash(p.Repository))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		_ = enc.Close()
		return err
	}
	stage, err := os.MkdirTemp(parent, ".replica-"+p.GenerationID+"-")
	if err != nil {
		_ = enc.Close()
		return err
	}
	defer os.RemoveAll(stage)
	plain, err := os.CreateTemp("", "repoark-repl-plain-*.tar.gz")
	if err != nil {
		_ = enc.Close()
		return err
	}
	plainName := plain.Name()
	defer os.Remove(plainName)
	if err := replication.Decrypt(plain, enc, keyPath); err != nil {
		_ = enc.Close()
		_ = plain.Close()
		return err
	}
	_ = enc.Close()
	if err := plain.Sync(); err != nil {
		_ = plain.Close()
		return err
	}
	if _, err := plain.Seek(0, 0); err != nil {
		_ = plain.Close()
		return err
	}
	if err := replication.ExtractGeneration(plain, stage); err != nil {
		_ = plain.Close()
		return err
	}
	_ = plain.Close()
	trusted := ""
	if r.Config.Security.SignManifests {
		trusted = r.Config.Security.SigningKeyPath + ".pub"
	}
	meta, err := generation.VerifyDir(stage, trusted)
	if err != nil {
		return err
	}
	if meta.Repository != p.Repository || meta.ID != p.GenerationID {
		return fmt.Errorf("replicated generation identity mismatch")
	}
	final := filepath.Join(parent, p.GenerationID)
	if _, err := os.Stat(final); err == nil {
		if _, err := generation.VerifyDir(final, trusted); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err := os.Rename(stage, final); err != nil {
		return err
	}
	// Hydrate the destination CAS from the verified immutable generation.
	// Replication intentionally transfers only reachable generation content,
	// not the entire source CAS namespace, so orphaned blobs are not copied.
	if r.Config.CAS.Enabled && r.Config.CAS.AutoCompact {
		if _, err := cas.New(r.Config.CAS.Root, r.Config.CAS.MinFileSize).Compact([]string{final}); err != nil {
			return fmt.Errorf("compact replicated generation into CAS: %w", err)
		}
		if ec := r.Config.ControlPlane.Storage.Erasure; ec.Enabled {
			_, err := erasure.ProtectPaths(r.Config.CAS.Root, []string{final}, ec.MinObjectBytes, erasure.Config{DataShards: ec.DataShards, ParityShards: ec.ParityShards, BlockBytes: ec.BlockBytes})
			if err != nil {
				return fmt.Errorf("erasure-protect replicated CAS objects: %w", err)
			}
		}
	}
	rp := GenerationReplica{GenerationID: p.GenerationID, RepositoryID: p.RepositoryID, AgentID: p.TargetAgent, MetaPath: filepath.Join(final, "generation.json"), State: ReplicaReady, Bytes: p.CipherBytes, SHA256: p.CipherSHA256, VerifiedAt: time.Now().UTC()}
	if err := r.Store.RecordReplica(ctx, rp); err != nil {
		return err
	}
	if _, ok := r.Store.(*RemoteStore); !ok {
		_ = os.Remove(replicationSpoolPath(r.Config.ControlPlane.Replication.SpoolRoot, p.TransferID))
		_ = r.Store.DeleteReplicationTransfer(ctx, p.TransferID)
	}
	if resumable {
		_ = os.Remove(encName)
	}
	if r.Emit != nil {
		r.Emit(fmt.Sprintf("%s generation %s replica installed on %s", p.Repository, p.GenerationID, p.TargetAgent))
	}
	return nil
}

func replicationSpoolPath(root, transferID string) string {
	clean := strings.Map(func(ch rune) rune {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' {
			return ch
		}
		return '_'
	}, transferID)
	return filepath.Join(root, clean+".blob")
}

// AffinityFromMetaPath identifies the storage agent for a generation reported
// as agent://<certificate-identity>/.... Local/shared generation paths return empty.
func AffinityFromMetaPath(metaPath string) string {
	const prefix = "agent://"
	if !strings.HasPrefix(metaPath, prefix) {
		return LocalWorkerAffinity
	}
	rest := strings.TrimPrefix(metaPath, prefix)
	name, _, _ := strings.Cut(rest, "/")
	return strings.TrimSpace(name)
}

type WorkerPool struct {
	Store  Store
	Runner Runner
	Config config.WorkerConfig
	Owner  string
}

func (w WorkerPool) Run(ctx context.Context) error {
	poll, _ := time.ParseDuration(w.Config.PollInterval)
	if poll <= 0 {
		poll = 2 * time.Second
	}
	lease, _ := time.ParseDuration(w.Config.Lease)
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	n := w.Config.Concurrency
	if n <= 0 {
		n = 1
	}
	errCh := make(chan error, n)
	owner := w.Owner
	if owner == "" {
		owner = LocalWorkerAffinity
	}
	for i := 0; i < n; i++ {
		go func(i int) {
			t := time.NewTicker(poll)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				case <-t.C:
					jobs, err := w.Store.Lease(ctx, owner, 1, lease)
					if err != nil {
						continue
					}
					for _, j := range jobs {
						err := w.Runner.Run(ctx, j)
						if err == nil {
							_ = w.Store.Complete(ctx, j.ID, owner)
						} else {
							backoff := retryDelay(j.Attempts)
							_ = w.Store.Fail(ctx, j.ID, owner, err.Error(), backoff)
						}
					}
				}
			}
		}(i)
	}
	for i := 0; i < n; i++ {
		err := <-errCh
		if err != nil && ctx.Err() == nil {
			return err
		}
	}
	return ctx.Err()
}
func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	sec := math.Min(900, math.Pow(2, float64(attempt)))
	return time.Duration(sec) * time.Second
}
