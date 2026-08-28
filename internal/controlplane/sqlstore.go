package controlplane

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type SQLStore struct {
	db      *sql.DB
	dialect string
}

func OpenStore(cfg config.StoreConfig) (*SQLStore, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	var driverName, dsn string
	switch driver {
	case "", "sqlite":
		driver, driverName = "sqlite", "sqlite"
		if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o700); err != nil {
			return nil, err
		}
		dsn = "file:" + filepath.ToSlash(cfg.SQLitePath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	case "postgres", "postgresql":
		driver, driverName = "postgres", "pgx"
		dsn = strings.TrimSpace(os.Getenv(cfg.DSNEnv))
		if dsn == "" {
			return nil, fmt.Errorf("%s is not set", cfg.DSNEnv)
		}
	default:
		return nil, fmt.Errorf("unsupported control-plane store driver %q", cfg.Driver)
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" {
		db.SetMaxOpenConns(1)
	}
	s := &SQLStore{db: db, dialect: driver}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLStore) Close() error { return s.db.Close() }

func (s *SQLStore) q(q string) string {
	if s.dialect != "postgres" {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *SQLStore) Migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS repoark_meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, target TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 50, attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 5, not_before TEXT NOT NULL, affinity TEXT NOT NULL DEFAULT '', lease_owner TEXT NOT NULL DEFAULT '',
			lease_until TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_ready ON jobs(status, not_before, priority, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_target ON jobs(kind, target, status)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_affinity_unique ON jobs(kind,target,affinity) WHERE status IN ('queued','running')`,
		`CREATE TABLE IF NOT EXISTS repositories (
			id TEXT PRIMARY KEY, account TEXT NOT NULL, full_name TEXT NOT NULL, backup_root TEXT NOT NULL,
			interval_seconds INTEGER NOT NULL, priority INTEGER NOT NULL DEFAULT 50, mirror_gitlab INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1, next_run_at TEXT NOT NULL, last_backup_at TEXT NOT NULL DEFAULT '',
			last_generation_id TEXT NOT NULL DEFAULT '', last_backup_successful INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL, UNIQUE(account, full_name))`,
		`CREATE INDEX IF NOT EXISTS idx_repositories_due ON repositories(enabled, next_run_at)`,
		`CREATE TABLE IF NOT EXISTS generations (
			id TEXT PRIMARY KEY, repository_id TEXT NOT NULL, repository TEXT NOT NULL, meta_path TEXT NOT NULL,
			created_at TEXT NOT NULL, verified INTEGER NOT NULL DEFAULT 0, bundle_sha256 TEXT NOT NULL DEFAULT '',
			lfs_sha256 TEXT NOT NULL DEFAULT '', FOREIGN KEY(repository_id) REFERENCES repositories(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_generations_repo ON generations(repository_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, cert_subject TEXT NOT NULL, labels_json TEXT NOT NULL DEFAULT '', replication_public_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, storage_health TEXT NOT NULL DEFAULT '', storage_total_bytes INTEGER NOT NULL DEFAULT 0, storage_free_bytes INTEGER NOT NULL DEFAULT 0,
			storage_free_percent DOUBLE PRECISION NOT NULL DEFAULT 0, storage_probe_ms INTEGER NOT NULL DEFAULT 0, storage_error TEXT NOT NULL DEFAULT '',
			disk_risk_score INTEGER NOT NULL DEFAULT 0, disk_model TEXT NOT NULL DEFAULT '', disk_serial TEXT NOT NULL DEFAULT '', disk_temperature_c DOUBLE PRECISION NOT NULL DEFAULT 0,
			disk_percentage_used DOUBLE PRECISION NOT NULL DEFAULT 0, disk_media_errors INTEGER NOT NULL DEFAULT 0, disk_critical_warning INTEGER NOT NULL DEFAULT 0, inventory_root TEXT NOT NULL DEFAULT '', inventory_objects INTEGER NOT NULL DEFAULT 0, inventory_bytes INTEGER NOT NULL DEFAULT 0, inventory_json TEXT NOT NULL DEFAULT '',
			last_seen_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS generation_replicas (
			generation_id TEXT NOT NULL, repository_id TEXT NOT NULL, agent_id TEXT NOT NULL, meta_path TEXT NOT NULL,
			state TEXT NOT NULL, bytes INTEGER NOT NULL DEFAULT 0, sha256 TEXT NOT NULL DEFAULT '', verified_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL, PRIMARY KEY(generation_id,agent_id),
			FOREIGN KEY(generation_id) REFERENCES generations(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_generation_replicas_state ON generation_replicas(state,agent_id)`,
		`CREATE TABLE IF NOT EXISTS replication_transfers (
			id TEXT PRIMARY KEY, generation_id TEXT NOT NULL, repository_id TEXT NOT NULL, source_agent TEXT NOT NULL, target_agent TEXT NOT NULL,
			spool_path TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, bytes INTEGER NOT NULL DEFAULT 0, sha256 TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_replication_transfers_expiry ON replication_transfers(state,expires_at)`,
		`CREATE TABLE IF NOT EXISTS restore_approvals (
			id TEXT PRIMARY KEY, repository TEXT NOT NULL, generation_id TEXT NOT NULL, target TEXT NOT NULL DEFAULT '', requested_by TEXT NOT NULL,
			approved_by TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_restore_approvals_status ON restore_approvals(status,created_at)`,
		`CREATE TABLE IF NOT EXISTS object_refs (
			digest TEXT PRIMARY KEY, kind TEXT NOT NULL DEFAULT '', bytes INTEGER NOT NULL DEFAULT 0,
			ref_count INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_object_refs_live ON object_refs(ref_count,digest)`,
		`CREATE TABLE IF NOT EXISTS object_ref_owners (
			digest TEXT NOT NULL, owner TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(digest,owner))`,
		`CREATE INDEX IF NOT EXISTS idx_object_ref_owners_digest ON object_ref_owners(digest)`,
		`CREATE TABLE IF NOT EXISTS object_leases (
			id TEXT PRIMARY KEY, digest TEXT NOT NULL, owner TEXT NOT NULL, expires_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_object_leases_expiry ON object_leases(expires_at,digest)`,
		`CREATE TABLE IF NOT EXISTS erasure_sets (
			object_sha256 TEXT PRIMARY KEY, original_bytes INTEGER NOT NULL, data_shards INTEGER NOT NULL, parity_shards INTEGER NOT NULL,
			block_bytes INTEGER NOT NULL, state TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS erasure_shards (
			object_sha256 TEXT NOT NULL, shard_index INTEGER NOT NULL, shard_sha256 TEXT NOT NULL, agent_id TEXT NOT NULL, failure_domain TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL, bytes INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL, PRIMARY KEY(object_sha256,shard_index,agent_id),
			FOREIGN KEY(object_sha256) REFERENCES erasure_sets(object_sha256) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_erasure_shards_agent_state ON erasure_shards(agent_id,state)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("control-plane migrate: %w", err)
		}
	}
	// v0.5 databases predate replication_public_key. CREATE TABLE IF NOT EXISTS
	// does not evolve an existing table, so perform one idempotent additive migration.
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE agents ADD COLUMN replication_public_key TEXT NOT NULL DEFAULT ''`); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
			return fmt.Errorf("control-plane migrate agents replication key: %w", err)
		}
	}
	for _, alter := range []string{
		`ALTER TABLE agents ADD COLUMN storage_health TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN storage_total_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN storage_free_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN storage_free_percent DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN storage_probe_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN storage_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN disk_risk_score INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN disk_model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN disk_serial TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN disk_temperature_c DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN disk_percentage_used DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN disk_media_errors INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN disk_critical_warning INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN inventory_root TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN inventory_objects INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN inventory_bytes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN inventory_json TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.ExecContext(ctx, alter); err != nil {
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "duplicate column") && !strings.Contains(msg, "already exists") {
				return fmt.Errorf("control-plane migrate agent storage columns: %w", err)
			}
		}
	}
	return s.SetMeta(ctx, "schema_version", "5")
}

func (s *SQLStore) Enqueue(ctx context.Context, j Job) (Job, bool, error) {
	now := time.Now().UTC()
	if j.ID == "" {
		j.ID = newID("job")
	}
	if j.Status == "" {
		j.Status = JobQueued
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 5
	}
	if j.NotBefore.IsZero() {
		j.NotBefore = now
	}
	j.CreatedAt, j.UpdatedAt = now, now
	var existing string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id FROM jobs WHERE kind=? AND target=? AND affinity=? AND status IN ('queued','running') ORDER BY created_at LIMIT 1`), j.Kind, j.Target, j.Affinity).Scan(&existing)
	if err == nil {
		existingJob, getErr := s.GetJob(ctx, existing)
		if getErr != nil {
			return Job{}, false, getErr
		}
		return existingJob, false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	_, err = s.db.ExecContext(ctx, s.q(`INSERT INTO jobs(id,kind,target,payload,status,priority,attempts,max_attempts,not_before,affinity,lease_owner,lease_until,created_at,updated_at,last_error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		j.ID, j.Kind, j.Target, j.Payload, j.Status, j.Priority, j.Attempts, j.MaxAttempts, ts(j.NotBefore), j.Affinity, "", "", ts(j.CreatedAt), ts(j.UpdatedAt), "")
	if err == nil {
		return j, true, nil
	}
	// A concurrent scheduler may have won the partial unique active-job index.
	// Re-read and return that job so the enqueue operation remains idempotent.
	var existingID string
	if qerr := s.db.QueryRowContext(ctx, s.q(`SELECT id FROM jobs WHERE kind=? AND target=? AND affinity=? AND status IN ('queued','running') ORDER BY created_at LIMIT 1`), j.Kind, j.Target, j.Affinity).Scan(&existingID); qerr == nil {
		existingJob, getErr := s.GetJob(ctx, existingID)
		if getErr != nil {
			return Job{}, false, getErr
		}
		return existingJob, false, nil
	}
	return Job{}, false, err
}

func (s *SQLStore) Lease(ctx context.Context, owner string, limit int, lease time.Duration) ([]Job, error) {
	if limit <= 0 {
		limit = 1
	}
	now := time.Now().UTC()
	until := now.Add(lease)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// An expired lease on the final permitted attempt is terminal. Without this
	// reaper, a crashed worker could cause attempt max+1 to be issued.
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE jobs SET status='failed',lease_owner='',lease_until='',updated_at=?,last_error=CASE WHEN last_error='' THEN 'lease expired on final attempt' ELSE last_error END WHERE status='running' AND lease_until<>'' AND lease_until<=? AND attempts>=max_attempts`), ts(now), ts(now)); err != nil {
		return nil, err
	}
	query := `SELECT id,kind,target,payload,status,priority,attempts,max_attempts,not_before,affinity,lease_owner,lease_until,created_at,updated_at,last_error FROM jobs WHERE not_before<=? AND attempts<max_attempts AND (affinity='' OR affinity=?) AND (status='queued' OR (status='running' AND lease_until<>'' AND lease_until<=?)) ORDER BY priority DESC, created_at ASC LIMIT ?`
	if s.dialect == "postgres" {
		query += ` FOR UPDATE SKIP LOCKED`
	}
	rows, err := tx.QueryContext(ctx, s.q(query), ts(now), owner, ts(now), limit)
	if err != nil {
		return nil, err
	}
	var candidates []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, j)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var leased []Job
	for _, j := range candidates {
		res, err := tx.ExecContext(ctx, s.q(`UPDATE jobs SET status='running', attempts=attempts+1, lease_owner=?, lease_until=?, updated_at=? WHERE id=? AND attempts<max_attempts AND (affinity='' OR affinity=?) AND (status='queued' OR (status='running' AND lease_until<>'' AND lease_until<=?))`), owner, ts(until), ts(now), j.ID, owner, ts(now))
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			continue
		}
		j.Status = JobRunning
		j.Attempts++
		j.LeaseOwner = owner
		j.LeaseUntil = until
		j.UpdatedAt = now
		leased = append(leased, j)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return leased, nil
}

func (s *SQLStore) Complete(ctx context.Context, id, owner string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind, payload string
	if err := tx.QueryRowContext(ctx, s.q(`SELECT kind,payload FROM jobs WHERE id=? AND status='running' AND lease_owner=?`), id, owner).Scan(&kind, &payload); err != nil {
		return fmt.Errorf("job %s is not leased by %s: %w", id, owner, err)
	}
	now := time.Now().UTC()
	if kind == "restore-generation" {
		var p restoreGenerationPayload
		if json.Unmarshal([]byte(payload), &p) == nil && p.ApprovalID != "" {
			res, err := tx.ExecContext(ctx, s.q(`UPDATE restore_approvals SET status=?,updated_at=? WHERE id=? AND status=?`), ApprovalExecuted, ts(now), p.ApprovalID, ApprovalScheduled)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return errors.New("restore approval is not scheduled")
			}
		}
	}
	res, err := tx.ExecContext(ctx, s.q(`UPDATE jobs SET status='succeeded',lease_owner='',lease_until='',updated_at=?,last_error='' WHERE id=? AND status='running' AND lease_owner=?`), ts(now), id, owner)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("job %s is not leased by %s", id, owner)
	}
	return tx.Commit()
}

func (s *SQLStore) Fail(ctx context.Context, id, owner, detail string, retryAfter time.Duration) error {
	var attempts, max int
	if err := s.db.QueryRowContext(ctx, s.q(`SELECT attempts,max_attempts FROM jobs WHERE id=? AND status='running' AND lease_owner=?`), id, owner).Scan(&attempts, &max); err != nil {
		return err
	}
	now := time.Now().UTC()
	status := JobQueued
	notBefore := now.Add(retryAfter)
	if attempts >= max {
		status = JobFailed
		notBefore = now
	}
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE jobs SET status=?, not_before=?, lease_owner='', lease_until='', updated_at=?, last_error=? WHERE id=? AND status='running' AND lease_owner=?`), status, ts(notBefore), ts(now), detail, id, owner)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("job %s is no longer leased by %s", id, owner)
	}
	return nil
}

func (s *SQLStore) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id,kind,target,payload,status,priority,attempts,max_attempts,not_before,affinity,lease_owner,lease_until,created_at,updated_at,last_error FROM jobs ORDER BY created_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *SQLStore) GetJob(ctx context.Context, id string) (Job, error) {
	row := s.db.QueryRowContext(ctx, s.q(`SELECT id,kind,target,payload,status,priority,attempts,max_attempts,not_before,affinity,lease_owner,lease_until,created_at,updated_at,last_error FROM jobs WHERE id=?`), id)
	return scanJob(row)
}

func (s *SQLStore) RetryJob(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE jobs SET status='queued',attempts=0,not_before=?,lease_owner='',lease_until='',last_error='',updated_at=? WHERE id=? AND status='failed'`), ts(time.Now().UTC()), ts(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("failed job %s not found", id)
	}
	return nil
}

func RepositoryID(account, fullName string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(account)) + "\x00" + strings.ToLower(strings.TrimSpace(fullName))))
	return "repo_" + hex.EncodeToString(h[:12])
}

func (s *SQLStore) UpsertRepository(ctx context.Context, r Repository) error {
	if r.ID == "" {
		r.ID = RepositoryID(r.Account, r.FullName)
	}
	if r.NextRunAt.IsZero() {
		r.NextRunAt = time.Now().UTC()
	}
	r.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO repositories(id,account,full_name,backup_root,interval_seconds,priority,mirror_gitlab,enabled,next_run_at,last_backup_at,last_generation_id,last_backup_successful,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account,full_name) DO UPDATE SET backup_root=excluded.backup_root,interval_seconds=excluded.interval_seconds,priority=excluded.priority,mirror_gitlab=excluded.mirror_gitlab,enabled=excluded.enabled,updated_at=excluded.updated_at`), r.ID, r.Account, r.FullName, r.BackupRoot, r.IntervalSeconds, r.Priority, boolInt(r.MirrorGitLab), boolInt(r.Enabled), ts(r.NextRunAt), optTS(r.LastBackupAt), r.LastGenerationID, boolInt(r.LastBackupSuccessful), ts(r.UpdatedAt))
	return err
}
func (s *SQLStore) ListRepositories(ctx context.Context) ([]Repository, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,account,full_name,backup_root,interval_seconds,priority,mirror_gitlab,enabled,next_run_at,last_backup_at,last_generation_id,last_backup_successful,updated_at FROM repositories ORDER BY account,full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Repository
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *SQLStore) DueRepositories(ctx context.Context, now time.Time, limit int) ([]Repository, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id,account,full_name,backup_root,interval_seconds,priority,mirror_gitlab,enabled,next_run_at,last_backup_at,last_generation_id,last_backup_successful,updated_at FROM repositories WHERE enabled=1 AND next_run_at<=? ORDER BY priority DESC,next_run_at ASC LIMIT ?`), ts(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Repository
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *SQLStore) MarkScheduled(ctx context.Context, id string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE repositories SET next_run_at=?,updated_at=? WHERE id=?`), ts(next), ts(time.Now().UTC()), id)
	return err
}
func (s *SQLStore) MarkBackupResult(ctx context.Context, id string, ok bool, generationID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE repositories SET last_backup_at=?,last_generation_id=?,last_backup_successful=?,updated_at=? WHERE id=?`), ts(at), generationID, boolInt(ok), ts(time.Now().UTC()), id)
	return err
}

func (s *SQLStore) RecordGeneration(ctx context.Context, g Generation) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO generations(id,repository_id,repository,meta_path,created_at,verified,bundle_sha256,lfs_sha256) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`), g.ID, g.RepositoryID, g.Repository, g.MetaPath, ts(g.CreatedAt), boolInt(g.Verified), g.BundleSHA256, g.LFSSHA256)
	return err
}
func (s *SQLStore) ListAllGenerations(ctx context.Context, limit int) ([]Generation, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id,repository_id,repository,meta_path,created_at,verified,bundle_sha256,lfs_sha256 FROM generations ORDER BY created_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Generation
	for rows.Next() {
		var g Generation
		var created string
		var verified int
		if err := rows.Scan(&g.ID, &g.RepositoryID, &g.Repository, &g.MetaPath, &created, &verified, &g.BundleSHA256, &g.LFSSHA256); err != nil {
			return nil, err
		}
		g.CreatedAt = parseTS(created)
		g.Verified = verified != 0
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *SQLStore) RecordReplica(ctx context.Context, r GenerationReplica) error {
	if r.State == "" {
		r.State = ReplicaReady
	}
	r.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO generation_replicas(generation_id,repository_id,agent_id,meta_path,state,bytes,sha256,verified_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(generation_id,agent_id) DO UPDATE SET repository_id=excluded.repository_id,meta_path=excluded.meta_path,state=excluded.state,bytes=excluded.bytes,sha256=excluded.sha256,verified_at=excluded.verified_at,updated_at=excluded.updated_at`), r.GenerationID, r.RepositoryID, r.AgentID, r.MetaPath, r.State, r.Bytes, r.SHA256, optTS(r.VerifiedAt), ts(r.UpdatedAt))
	return err
}
func (s *SQLStore) ListReplicas(ctx context.Context, generationID string) ([]GenerationReplica, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT generation_id,repository_id,agent_id,meta_path,state,bytes,sha256,verified_at,updated_at FROM generation_replicas WHERE generation_id=? ORDER BY agent_id`), generationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GenerationReplica
	for rows.Next() {
		r, err := scanReplica(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *SQLStore) RecordReplicationTransfer(ctx context.Context, t ReplicationTransfer) error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("replication transfer id is required")
	}
	if t.State == "" {
		t.State = TransferReady
	}
	t.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO replication_transfers(id,generation_id,repository_id,source_agent,target_agent,spool_path,state,bytes,sha256,expires_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET generation_id=excluded.generation_id,repository_id=excluded.repository_id,source_agent=excluded.source_agent,target_agent=excluded.target_agent,spool_path=excluded.spool_path,state=excluded.state,bytes=excluded.bytes,sha256=excluded.sha256,expires_at=excluded.expires_at,updated_at=excluded.updated_at`), t.ID, t.GenerationID, t.RepositoryID, t.SourceAgent, t.TargetAgent, t.SpoolPath, t.State, t.Bytes, t.SHA256, ts(t.ExpiresAt), ts(t.UpdatedAt))
	return err
}
func (s *SQLStore) GetReplicationTransfer(ctx context.Context, id string) (ReplicationTransfer, error) {
	var t ReplicationTransfer
	var expires, updated string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id,generation_id,repository_id,source_agent,target_agent,spool_path,state,bytes,sha256,expires_at,updated_at FROM replication_transfers WHERE id=?`), id).Scan(&t.ID, &t.GenerationID, &t.RepositoryID, &t.SourceAgent, &t.TargetAgent, &t.SpoolPath, &t.State, &t.Bytes, &t.SHA256, &expires, &updated)
	if err != nil {
		return ReplicationTransfer{}, err
	}
	t.ExpiresAt, t.UpdatedAt = parseTS(expires), parseTS(updated)
	return t, nil
}
func (s *SQLStore) ListExpiredReplicationTransfers(ctx context.Context, now time.Time, limit int) ([]ReplicationTransfer, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id,generation_id,repository_id,source_agent,target_agent,spool_path,state,bytes,sha256,expires_at,updated_at FROM replication_transfers WHERE expires_at<=? ORDER BY expires_at ASC LIMIT ?`), ts(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplicationTransfer
	for rows.Next() {
		var t ReplicationTransfer
		var expires, updated string
		if err := rows.Scan(&t.ID, &t.GenerationID, &t.RepositoryID, &t.SourceAgent, &t.TargetAgent, &t.SpoolPath, &t.State, &t.Bytes, &t.SHA256, &expires, &updated); err != nil {
			return nil, err
		}
		t.ExpiresAt, t.UpdatedAt = parseTS(expires), parseTS(updated)
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *SQLStore) DeleteReplicationTransfer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM replication_transfers WHERE id=?`), id)
	return err
}
func (s *SQLStore) ListAllReplicas(ctx context.Context, limit int) ([]GenerationReplica, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT generation_id,repository_id,agent_id,meta_path,state,bytes,sha256,verified_at,updated_at FROM generation_replicas ORDER BY updated_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GenerationReplica
	for rows.Next() {
		r, err := scanReplica(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *SQLStore) ListGenerations(ctx context.Context, repoID string, limit int) ([]Generation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id,repository_id,repository,meta_path,created_at,verified,bundle_sha256,lfs_sha256 FROM generations WHERE repository_id=? ORDER BY created_at DESC LIMIT ?`), repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Generation
	for rows.Next() {
		var g Generation
		var created string
		var verified int
		if err := rows.Scan(&g.ID, &g.RepositoryID, &g.Repository, &g.MetaPath, &created, &verified, &g.BundleSHA256, &g.LFSSHA256); err != nil {
			return nil, err
		}
		g.CreatedAt = parseTS(created)
		g.Verified = verified != 0
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *SQLStore) PruneGenerationRecords(ctx context.Context, repoID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id FROM generations WHERE repository_id=? ORDER BY created_at DESC`), repoID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids[keep:] {
		if _, err := s.db.ExecContext(ctx, s.q(`DELETE FROM generations WHERE id=?`), id); err != nil {
			return err
		}
	}
	return nil
}
func (s *SQLStore) DisableMissingRepositories(ctx context.Context, account string, seen map[string]struct{}) error {
	repos, err := s.ListRepositories(ctx)
	if err != nil {
		return err
	}
	now := ts(time.Now().UTC())
	for _, r := range repos {
		if r.Account != account {
			continue
		}
		if _, ok := seen[r.ID]; ok {
			continue
		}
		if _, err := s.db.ExecContext(ctx, s.q(`UPDATE repositories SET enabled=0,updated_at=? WHERE id=?`), now, r.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStore) HeartbeatAgent(ctx context.Context, a Agent) error {
	now := time.Now().UTC()
	if a.ID == "" {
		a.ID = a.Name
	}
	if a.Status == "" {
		a.Status = "online"
	}
	a.LastSeenAt = now
	a.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO agents(id,name,cert_subject,labels_json,replication_public_key,status,storage_health,storage_total_bytes,storage_free_bytes,storage_free_percent,storage_probe_ms,storage_error,disk_risk_score,disk_model,disk_serial,disk_temperature_c,disk_percentage_used,disk_media_errors,disk_critical_warning,inventory_root,inventory_objects,inventory_bytes,inventory_json,last_seen_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,cert_subject=excluded.cert_subject,labels_json=excluded.labels_json,replication_public_key=excluded.replication_public_key,status=excluded.status,storage_health=excluded.storage_health,storage_total_bytes=excluded.storage_total_bytes,storage_free_bytes=excluded.storage_free_bytes,storage_free_percent=excluded.storage_free_percent,storage_probe_ms=excluded.storage_probe_ms,storage_error=excluded.storage_error,disk_risk_score=excluded.disk_risk_score,disk_model=excluded.disk_model,disk_serial=excluded.disk_serial,disk_temperature_c=excluded.disk_temperature_c,disk_percentage_used=excluded.disk_percentage_used,disk_media_errors=excluded.disk_media_errors,disk_critical_warning=excluded.disk_critical_warning,inventory_root=excluded.inventory_root,inventory_objects=excluded.inventory_objects,inventory_bytes=excluded.inventory_bytes,inventory_json=excluded.inventory_json,last_seen_at=excluded.last_seen_at,updated_at=excluded.updated_at`), a.ID, a.Name, a.CertSubject, a.LabelsJSON, a.ReplicationPublicKey, a.Status, a.StorageHealth, a.StorageTotalBytes, a.StorageFreeBytes, a.StorageFreePercent, a.StorageProbeMS, a.StorageError, a.DiskRiskScore, a.DiskModel, a.DiskSerial, a.DiskTemperatureC, a.DiskPercentageUsed, a.DiskMediaErrors, a.DiskCriticalWarning, a.InventoryRoot, a.InventoryObjects, a.InventoryBytes, a.InventoryJSON, ts(now), ts(now))
	return err
}
func (s *SQLStore) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,cert_subject,labels_json,replication_public_key,status,storage_health,storage_total_bytes,storage_free_bytes,storage_free_percent,storage_probe_ms,storage_error,disk_risk_score,disk_model,disk_serial,disk_temperature_c,disk_percentage_used,disk_media_errors,disk_critical_warning,inventory_root,inventory_objects,inventory_bytes,inventory_json,last_seen_at,updated_at FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		var seen, updated string
		if err := rows.Scan(&a.ID, &a.Name, &a.CertSubject, &a.LabelsJSON, &a.ReplicationPublicKey, &a.Status, &a.StorageHealth, &a.StorageTotalBytes, &a.StorageFreeBytes, &a.StorageFreePercent, &a.StorageProbeMS, &a.StorageError, &a.DiskRiskScore, &a.DiskModel, &a.DiskSerial, &a.DiskTemperatureC, &a.DiskPercentageUsed, &a.DiskMediaErrors, &a.DiskCriticalWarning, &a.InventoryRoot, &a.InventoryObjects, &a.InventoryBytes, &a.InventoryJSON, &seen, &updated); err != nil {
			return nil, err
		}
		a.LastSeenAt = parseTS(seen)
		a.UpdatedAt = parseTS(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *SQLStore) GetAgent(ctx context.Context, id string) (Agent, error) {
	var a Agent
	var seen, updated string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id,name,cert_subject,labels_json,replication_public_key,status,storage_health,storage_total_bytes,storage_free_bytes,storage_free_percent,storage_probe_ms,storage_error,disk_risk_score,disk_model,disk_serial,disk_temperature_c,disk_percentage_used,disk_media_errors,disk_critical_warning,inventory_root,inventory_objects,inventory_bytes,inventory_json,last_seen_at,updated_at FROM agents WHERE id=?`), id).Scan(&a.ID, &a.Name, &a.CertSubject, &a.LabelsJSON, &a.ReplicationPublicKey, &a.Status, &a.StorageHealth, &a.StorageTotalBytes, &a.StorageFreeBytes, &a.StorageFreePercent, &a.StorageProbeMS, &a.StorageError, &a.DiskRiskScore, &a.DiskModel, &a.DiskSerial, &a.DiskTemperatureC, &a.DiskPercentageUsed, &a.DiskMediaErrors, &a.DiskCriticalWarning, &a.InventoryRoot, &a.InventoryObjects, &a.InventoryBytes, &a.InventoryJSON, &seen, &updated)
	if err != nil {
		return Agent{}, err
	}
	a.LastSeenAt = parseTS(seen)
	a.UpdatedAt = parseTS(updated)
	return a, nil
}
func (s *SQLStore) CreateRestoreApproval(ctx context.Context, a RestoreApproval) error {
	if a.ID == "" {
		a.ID = newID("restore")
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if a.Status == "" {
		a.Status = ApprovalPending
	}
	a.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO restore_approvals(id,repository,generation_id,target,requested_by,approved_by,status,created_at,expires_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`), a.ID, a.Repository, a.GenerationID, a.Target, a.RequestedBy, a.ApprovedBy, a.Status, ts(a.CreatedAt), ts(a.ExpiresAt), ts(a.UpdatedAt))
	return err
}
func (s *SQLStore) GetRestoreApproval(ctx context.Context, id string) (RestoreApproval, error) {
	var a RestoreApproval
	var ca, ea, ua string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id,repository,generation_id,target,requested_by,approved_by,status,created_at,expires_at,updated_at FROM restore_approvals WHERE id=?`), id).Scan(&a.ID, &a.Repository, &a.GenerationID, &a.Target, &a.RequestedBy, &a.ApprovedBy, &a.Status, &ca, &ea, &ua)
	if err != nil {
		return a, err
	}
	a.CreatedAt = parseTS(ca)
	a.ExpiresAt = parseTS(ea)
	a.UpdatedAt = parseTS(ua)
	return a, nil
}
func (s *SQLStore) ListRestoreApprovals(ctx context.Context, limit int) ([]RestoreApproval, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id,repository,generation_id,target,requested_by,approved_by,status,created_at,expires_at,updated_at FROM restore_approvals ORDER BY created_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RestoreApproval
	for rows.Next() {
		var a RestoreApproval
		var ca, ea, ua string
		if err := rows.Scan(&a.ID, &a.Repository, &a.GenerationID, &a.Target, &a.RequestedBy, &a.ApprovedBy, &a.Status, &ca, &ea, &ua); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTS(ca)
		a.ExpiresAt = parseTS(ea)
		a.UpdatedAt = parseTS(ua)
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *SQLStore) ApproveRestore(ctx context.Context, id, actor string, distinct bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var requested, status, expires string
	if err := tx.QueryRowContext(ctx, s.q(`SELECT requested_by,status,expires_at FROM restore_approvals WHERE id=?`), id).Scan(&requested, &status, &expires); err != nil {
		return err
	}
	now := time.Now().UTC()
	if status != ApprovalPending {
		return fmt.Errorf("restore approval is %s", status)
	}
	if exp := parseTS(expires); !exp.IsZero() && !exp.After(now) {
		_, _ = tx.ExecContext(ctx, s.q(`UPDATE restore_approvals SET status=?,updated_at=? WHERE id=?`), ApprovalExpired, ts(now), id)
		_ = tx.Commit()
		return errors.New("restore approval expired")
	}
	if distinct && requested == actor {
		return errors.New("requester cannot approve own restore")
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE restore_approvals SET status=?,approved_by=?,updated_at=? WHERE id=? AND status=?`), ApprovalApproved, actor, ts(now), id, ApprovalPending); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLStore) ScheduleRestore(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE restore_approvals SET status=?,updated_at=? WHERE id=? AND status=? AND expires_at>?`), ApprovalScheduled, ts(time.Now().UTC()), id, ApprovalApproved, ts(time.Now().UTC()))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("restore approval is not approved or expired")
	}
	return nil
}
func (s *SQLStore) ReleaseRestoreSchedule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE restore_approvals SET status=?,updated_at=? WHERE id=? AND status=?`), ApprovalApproved, ts(time.Now().UTC()), id, ApprovalScheduled)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("restore approval is not scheduled")
	}
	return nil
}
func (s *SQLStore) MarkRestoreExecuted(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE restore_approvals SET status=?,updated_at=? WHERE id=? AND status=?`), ApprovalExecuted, ts(time.Now().UTC()), id, ApprovalScheduled)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("restore approval is not approved")
	}
	return nil
}
func (s *SQLStore) Stats(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
	queries := []struct {
		q    string
		args []any
		dst  *int
	}{{`SELECT COUNT(*) FROM jobs WHERE status='queued'`, nil, &st.QueuedJobs}, {`SELECT COUNT(*) FROM jobs WHERE status='running'`, nil, &st.RunningJobs}, {`SELECT COUNT(*) FROM jobs WHERE status='failed'`, nil, &st.FailedJobs}, {`SELECT COUNT(*) FROM repositories`, nil, &st.Repositories}, {`SELECT COUNT(*) FROM repositories WHERE enabled=1 AND next_run_at<=?`, []any{ts(now)}, &st.DueRepositories}, {`SELECT COUNT(*) FROM generations`, nil, &st.Generations}, {`SELECT COUNT(*) FROM agents WHERE last_seen_at>=?`, []any{ts(now.Add(-2 * time.Minute))}, &st.ConnectedAgents}, {`SELECT COUNT(*) FROM jobs j LEFT JOIN agents a ON a.id=j.affinity AND a.last_seen_at>=? WHERE j.status IN ('queued','running') AND j.affinity<>'' AND j.affinity<>? AND a.id IS NULL`, []any{ts(now.Add(-2 * time.Minute)), LocalWorkerAffinity}, &st.StrandedJobs}, {`SELECT COUNT(*) FROM generation_replicas WHERE state='ready'`, nil, &st.ReadyReplicas}, {`SELECT COUNT(*) FROM restore_approvals WHERE status='pending' AND expires_at>?`, []any{ts(now)}, &st.PendingApprovals}, {`SELECT COUNT(*) FROM replication_transfers`, nil, &st.ActiveTransfers}, {`SELECT COUNT(*) FROM agents WHERE storage_health='degraded'`, nil, &st.DegradedStorageAgents}, {`SELECT COUNT(*) FROM agents WHERE storage_health='unhealthy'`, nil, &st.UnhealthyStorageAgents}, {`SELECT COUNT(*) FROM object_refs`, nil, &st.ObjectRefs}, {`SELECT COUNT(*) FROM object_leases WHERE expires_at>?`, []any{ts(now)}, &st.ActiveObjectLeases}, {`SELECT COUNT(*) FROM erasure_sets`, nil, &st.ErasureSets}, {`SELECT COUNT(*) FROM erasure_shards WHERE state='ready'`, nil, &st.ErasureShardCopies}}
	for _, x := range queries {
		if err := s.db.QueryRowContext(ctx, s.q(x.q), x.args...).Scan(x.dst); err != nil {
			return st, err
		}
	}
	return st, nil
}
func (s *SQLStore) SetMeta(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO repoark_meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`), k, v)
	return err
}
func (s *SQLStore) GetMeta(ctx context.Context, k string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT v FROM repoark_meta WHERE k=?`), k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *SQLStore) AdjustObjectRef(ctx context.Context, ref ObjectRef, delta int64) (ObjectRef, error) {
	if strings.TrimSpace(ref.Digest) == "" {
		return ObjectRef{}, fmt.Errorf("object digest is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ObjectRef{}, err
	}
	defer tx.Rollback()
	query := `SELECT digest,kind,bytes,ref_count,updated_at FROM object_refs WHERE digest=?`
	if s.dialect == "postgres" {
		query += ` FOR UPDATE`
	}
	var cur ObjectRef
	var updated string
	err = tx.QueryRowContext(ctx, s.q(query), ref.Digest).Scan(&cur.Digest, &cur.Kind, &cur.Bytes, &cur.RefCount, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		cur = ref
		if delta < 0 {
			return ObjectRef{}, fmt.Errorf("negative refcount for %s", ref.Digest)
		}
		cur.RefCount = delta
		cur.UpdatedAt = time.Now().UTC()
		_, err = tx.ExecContext(ctx, s.q(`INSERT INTO object_refs(digest,kind,bytes,ref_count,updated_at) VALUES(?,?,?,?,?)`), cur.Digest, cur.Kind, cur.Bytes, cur.RefCount, ts(cur.UpdatedAt))
	} else if err == nil {
		cur.UpdatedAt = parseTS(updated)
		if ref.Kind != "" {
			cur.Kind = ref.Kind
		}
		if ref.Bytes > 0 {
			cur.Bytes = ref.Bytes
		}
		cur.RefCount += delta
		if cur.RefCount < 0 {
			return ObjectRef{}, fmt.Errorf("negative refcount for %s", ref.Digest)
		}
		cur.UpdatedAt = time.Now().UTC()
		_, err = tx.ExecContext(ctx, s.q(`UPDATE object_refs SET kind=?,bytes=?,ref_count=?,updated_at=? WHERE digest=?`), cur.Kind, cur.Bytes, cur.RefCount, ts(cur.UpdatedAt), cur.Digest)
	}
	if err != nil {
		return ObjectRef{}, err
	}
	if err := tx.Commit(); err != nil {
		return ObjectRef{}, err
	}
	return cur, nil
}

func (s *SQLStore) EnsureObjectRef(ctx context.Context, ref ObjectRef, owner string) (bool, error) {
	ref.Digest = strings.ToLower(strings.TrimSpace(ref.Digest))
	owner = strings.TrimSpace(owner)
	if ref.Digest == "" || owner == "" {
		return false, fmt.Errorf("digest and owner are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, s.q(`INSERT INTO object_ref_owners(digest,owner,created_at) VALUES(?,?,?) ON CONFLICT(digest,owner) DO NOTHING`), ref.Digest, owner, ts(time.Now().UTC()))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, s.q(`INSERT INTO object_refs(digest,kind,bytes,ref_count,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(digest) DO UPDATE SET kind=CASE WHEN excluded.kind='' THEN object_refs.kind ELSE excluded.kind END,bytes=CASE WHEN excluded.bytes<=0 THEN object_refs.bytes ELSE excluded.bytes END,ref_count=object_refs.ref_count+1,updated_at=excluded.updated_at`), ref.Digest, ref.Kind, ref.Bytes, 1, ts(now))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLStore) ReleaseObjectRef(ctx context.Context, digest, owner string) (bool, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	owner = strings.TrimSpace(owner)
	if digest == "" || owner == "" {
		return false, fmt.Errorf("digest and owner are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, s.q(`DELETE FROM object_ref_owners WHERE digest=? AND owner=?`), digest, owner)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, s.q(`UPDATE object_refs SET ref_count=CASE WHEN ref_count>0 THEN ref_count-1 ELSE 0 END,updated_at=? WHERE digest=?`), ts(time.Now().UTC()), digest)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLStore) GetObjectRef(ctx context.Context, digest string) (ObjectRef, bool, error) {
	var r ObjectRef
	var updated string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT digest,kind,bytes,ref_count,updated_at FROM object_refs WHERE digest=?`), digest).Scan(&r.Digest, &r.Kind, &r.Bytes, &r.RefCount, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectRef{}, false, nil
	}
	if err != nil {
		return ObjectRef{}, false, err
	}
	r.UpdatedAt = parseTS(updated)
	return r, true, nil
}

func (s *SQLStore) ListObjectRefs(ctx context.Context, limit int) ([]ObjectRef, error) {
	q := `SELECT digest,kind,bytes,ref_count,updated_at FROM object_refs ORDER BY digest`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObjectRef
	for rows.Next() {
		var r ObjectRef
		var updated string
		if err := rows.Scan(&r.Digest, &r.Kind, &r.Bytes, &r.RefCount, &updated); err != nil {
			return nil, err
		}
		r.UpdatedAt = parseTS(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLStore) AcquireObjectLease(ctx context.Context, l ObjectLease) error {
	if l.ID == "" {
		l.ID = newID("olease")
	}
	if l.Digest == "" || l.Owner == "" || l.ExpiresAt.IsZero() {
		return fmt.Errorf("invalid object lease")
	}
	l.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO object_leases(id,digest,owner,expires_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET digest=excluded.digest,owner=excluded.owner,expires_at=excluded.expires_at,updated_at=excluded.updated_at`), l.ID, l.Digest, l.Owner, ts(l.ExpiresAt), ts(l.UpdatedAt))
	return err
}
func (s *SQLStore) ReleaseObjectLease(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM object_leases WHERE id=?`), id)
	return err
}
func (s *SQLStore) ListActiveObjectLeases(ctx context.Context, now time.Time, limit int) ([]ObjectLease, error) {
	q := `SELECT id,digest,owner,expires_at,updated_at FROM object_leases WHERE expires_at>? ORDER BY expires_at`
	args := []any{ts(now)}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ObjectLease
	for rows.Next() {
		var l ObjectLease
		var e, u string
		if err := rows.Scan(&l.ID, &l.Digest, &l.Owner, &e, &u); err != nil {
			return nil, err
		}
		l.ExpiresAt = parseTS(e)
		l.UpdatedAt = parseTS(u)
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *SQLStore) ProtectedObjectDigests(ctx context.Context, now time.Time) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT digest FROM object_refs WHERE ref_count>0 UNION SELECT digest FROM object_leases WHERE expires_at>?`), ts(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out[d] = struct{}{}
	}
	return out, rows.Err()
}

func (s *SQLStore) RecordErasureSet(ctx context.Context, e ErasureSet) error {
	if e.ObjectSHA256 == "" {
		return fmt.Errorf("erasure object digest is required")
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO erasure_sets(object_sha256,original_bytes,data_shards,parity_shards,block_bytes,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(object_sha256) DO UPDATE SET original_bytes=excluded.original_bytes,data_shards=excluded.data_shards,parity_shards=excluded.parity_shards,block_bytes=excluded.block_bytes,state=excluded.state,updated_at=excluded.updated_at`), e.ObjectSHA256, e.OriginalBytes, e.DataShards, e.ParityShards, e.BlockBytes, e.State, ts(e.CreatedAt), ts(e.UpdatedAt))
	return err
}
func (s *SQLStore) GetErasureSet(ctx context.Context, digest string) (ErasureSet, bool, error) {
	var e ErasureSet
	var c, u string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT object_sha256,original_bytes,data_shards,parity_shards,block_bytes,state,created_at,updated_at FROM erasure_sets WHERE object_sha256=?`), digest).Scan(&e.ObjectSHA256, &e.OriginalBytes, &e.DataShards, &e.ParityShards, &e.BlockBytes, &e.State, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return ErasureSet{}, false, nil
	}
	if err != nil {
		return ErasureSet{}, false, err
	}
	e.CreatedAt = parseTS(c)
	e.UpdatedAt = parseTS(u)
	return e, true, nil
}
func (s *SQLStore) ListErasureSets(ctx context.Context, limit int) ([]ErasureSet, error) {
	q := `SELECT object_sha256,original_bytes,data_shards,parity_shards,block_bytes,state,created_at,updated_at FROM erasure_sets ORDER BY object_sha256`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErasureSet
	for rows.Next() {
		var e ErasureSet
		var c, u string
		if err := rows.Scan(&e.ObjectSHA256, &e.OriginalBytes, &e.DataShards, &e.ParityShards, &e.BlockBytes, &e.State, &c, &u); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTS(c)
		e.UpdatedAt = parseTS(u)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *SQLStore) RecordErasureShard(ctx context.Context, sh ErasureShard) error {
	if sh.ObjectSHA256 == "" || sh.ShardSHA256 == "" || sh.AgentID == "" {
		return fmt.Errorf("invalid erasure shard")
	}
	sh.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO erasure_shards(object_sha256,shard_index,shard_sha256,agent_id,failure_domain,state,bytes,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(object_sha256,shard_index,agent_id) DO UPDATE SET shard_sha256=excluded.shard_sha256,failure_domain=excluded.failure_domain,state=excluded.state,bytes=excluded.bytes,updated_at=excluded.updated_at`), sh.ObjectSHA256, sh.ShardIndex, sh.ShardSHA256, sh.AgentID, sh.FailureDomain, sh.State, sh.Bytes, ts(sh.UpdatedAt))
	return err
}
func (s *SQLStore) DeleteErasureShard(ctx context.Context, digest string, index int, agent string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM erasure_shards WHERE object_sha256=? AND shard_index=? AND agent_id=?`), digest, index, agent)
	return err
}
func (s *SQLStore) ListErasureShards(ctx context.Context, digest string) ([]ErasureShard, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT object_sha256,shard_index,shard_sha256,agent_id,failure_domain,state,bytes,updated_at FROM erasure_shards WHERE object_sha256=? ORDER BY shard_index,agent_id`), digest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErasureShard
	for rows.Next() {
		var sh ErasureShard
		var u string
		if err := rows.Scan(&sh.ObjectSHA256, &sh.ShardIndex, &sh.ShardSHA256, &sh.AgentID, &sh.FailureDomain, &sh.State, &sh.Bytes, &u); err != nil {
			return nil, err
		}
		sh.UpdatedAt = parseTS(u)
		out = append(out, sh)
	}
	return out, rows.Err()
}

func scanJob(s interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var nb, lu, ca, ua string
	if err := s.Scan(&j.ID, &j.Kind, &j.Target, &j.Payload, &j.Status, &j.Priority, &j.Attempts, &j.MaxAttempts, &nb, &j.Affinity, &j.LeaseOwner, &lu, &ca, &ua, &j.LastError); err != nil {
		return j, err
	}
	j.NotBefore = parseTS(nb)
	j.LeaseUntil = parseTS(lu)
	j.CreatedAt = parseTS(ca)
	j.UpdatedAt = parseTS(ua)
	return j, nil
}
func scanRepo(s interface{ Scan(...any) error }) (Repository, error) {
	var r Repository
	var mirror, enabled, ok int
	var next, last, updated string
	if err := s.Scan(&r.ID, &r.Account, &r.FullName, &r.BackupRoot, &r.IntervalSeconds, &r.Priority, &mirror, &enabled, &next, &last, &r.LastGenerationID, &ok, &updated); err != nil {
		return r, err
	}
	r.MirrorGitLab = mirror != 0
	r.Enabled = enabled != 0
	r.NextRunAt = parseTS(next)
	r.LastBackupAt = parseTS(last)
	r.LastBackupSuccessful = ok != 0
	r.UpdatedAt = parseTS(updated)
	return r, nil
}
func scanReplica(s interface{ Scan(...any) error }) (GenerationReplica, error) {
	var r GenerationReplica
	var verified, updated string
	if err := s.Scan(&r.GenerationID, &r.RepositoryID, &r.AgentID, &r.MetaPath, &r.State, &r.Bytes, &r.SHA256, &verified, &updated); err != nil {
		return r, err
	}
	r.VerifiedAt = parseTS(verified)
	r.UpdatedAt = parseTS(updated)
	return r, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func optTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return ts(t)
}
func parseTS(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}
