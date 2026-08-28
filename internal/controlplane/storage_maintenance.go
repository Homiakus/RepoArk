package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Homiakus/repoark/internal/cas"
	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/scrub"
	"github.com/Homiakus/repoark/internal/tiering"
)

type StorageMaintenanceReconciler struct {
	Store  Store
	Config config.Config
	Emit   func(string)
}

func (r StorageMaintenanceReconciler) Reconcile(ctx context.Context) (int, error) {
	cfg := r.Config.ControlPlane.Storage
	if !cfg.Enabled {
		return 0, nil
	}
	now := time.Now().UTC()
	jobs, _ := r.Store.ListJobs(ctx, 50000)
	agents, err := r.Store.ListAgents(ctx)
	if err != nil {
		return 0, err
	}
	affinities := []string{LocalWorkerAffinity}
	for _, a := range agents {
		if agentReadable(a, cfg) {
			affinities = append(affinities, a.ID)
		}
	}
	queued := 0
	for _, affinity := range affinities {
		if cfg.Scrub.Enabled {
			interval, _ := time.ParseDuration(cfg.Scrub.Interval)
			if interval <= 0 {
				interval = 24 * time.Hour
			}
			if !recentlyRanJob(jobs, "scrub-cas", affinity, interval, now) {
				b, _ := json.Marshal(map[string]any{"sample_objects": cfg.Scrub.SampleObjects})
				_, created, e := r.Store.Enqueue(ctx, Job{Kind: "scrub-cas", Target: affinity, Payload: string(b), Affinity: affinity, Priority: 70, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: now})
				if e != nil {
					return queued, e
				}
				if created {
					queued++
				}
			}
		}
		if cfg.Tiering.Enabled {
			interval := 24 * time.Hour
			if !recentlyRanJob(jobs, "tier-cas", affinity, interval, now) {
				_, created, e := r.Store.Enqueue(ctx, Job{Kind: "tier-cas", Target: affinity, Payload: "{}", Affinity: affinity, Priority: 40, MaxAttempts: r.Config.ControlPlane.Workers.MaxAttempts, NotBefore: now})
				if e != nil {
					return queued, e
				}
				if created {
					queued++
				}
			}
		}
	}
	return queued, nil
}

func (r Runner) scrubCAS(ctx context.Context, job Job) error {
	cfg := r.Config.ControlPlane.Storage.Scrub
	repair := scrub.RepairFunc(nil)
	if cfg.Repair {
		repair = scrub.LocalErasureRepair(r.Config.CAS.Root)
	}
	res, err := (scrub.Scrubber{CAS: cas.New(r.Config.CAS.Root, 0), SampleObjects: cfg.SampleObjects, SeedSalt: cfg.SeedSalt, Repair: repair}).Run(ctx)
	if cfg.Repair && r.Config.ControlPlane.Storage.Erasure.Enabled && r.Config.ControlPlane.Storage.Erasure.Distributed {
		target := LocalWorkerAffinity
		if job.LeaseOwner != "" {
			target = job.LeaseOwner
		}
		for _, digest := range res.UnrepairedDigests {
			if reporter, ok := r.Store.(corruptionReporter); ok {
				_ = reporter.ReportCorruptObject(ctx, digest)
			} else {
				_, _ = ScheduleDistributedRepair(ctx, r.Store, r.Config, target, digest)
			}
		}
	}
	if r.Emit != nil {
		r.Emit(fmt.Sprintf("CAS scrub sampled=%d corrupt=%d repaired=%d failed=%d", res.Sampled, res.Corrupt, res.Repaired, res.Failed))
	}
	return err
}

func (r Runner) tierCAS(ctx context.Context, _ Job) error {
	cfg := r.Config.ControlPlane.Storage.Tiering
	age, _ := time.ParseDuration(cfg.MinAge)
	res, err := tiering.CopyTier(ctx, cas.New(r.Config.CAS.Root, 0), tiering.Config{ColdRoot: cfg.ColdRoot, MinAge: age, MinBytes: cfg.MinBytes, RcloneRemote: cfg.RcloneRemote}, time.Now().UTC())
	if r.Emit != nil {
		r.Emit(fmt.Sprintf("CAS tier eligible=%d local=%d remote=%d bytes=%d", res.Eligible, res.CopiedLocal, res.CopiedS3, res.Bytes))
	}
	return err
}
