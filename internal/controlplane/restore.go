package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Homiakus/repoark/internal/config"
)

// ResolveGeneration validates a repository/generation pair against durable
// control-plane state. Callers never route a restore based solely on user input.
func ResolveGeneration(ctx context.Context, store Store, repository, generationID string) (Repository, Generation, error) {
	repos, err := store.ListRepositories(ctx)
	if err != nil {
		return Repository{}, Generation{}, err
	}
	var rec Repository
	found := false
	for _, r := range repos {
		if strings.EqualFold(r.FullName, repository) {
			rec = r
			found = true
			break
		}
	}
	if !found {
		return Repository{}, Generation{}, fmt.Errorf("repository %s is not in control-plane store", repository)
	}
	gens, err := store.ListGenerations(ctx, rec.ID, 10000)
	if err != nil {
		return Repository{}, Generation{}, err
	}
	for _, g := range gens {
		if g.ID == generationID {
			return rec, g, nil
		}
	}
	return Repository{}, Generation{}, fmt.Errorf("generation %s for %s not found", generationID, repository)
}

func RestoreJobTarget(repo, generation, target string) string {
	sum := sha256.Sum256([]byte(repo + "\x00" + generation + "\x00" + target))
	return fmt.Sprintf("restore:%s@%s@%x", repo, generation, sum[:6])
}

// EnqueueGenerationRestore selects a node that physically owns readable data
// and creates a path-bound restore job. approvalID is carried through so the
// worker can atomically mark an approved workflow executed on success.
func EnqueueGenerationRestore(ctx context.Context, store Store, cfg config.Config, repository, generationID, target, approvalID string, priority int) (Job, bool, error) {
	rec, selected, err := ResolveGeneration(ctx, store, repository, generationID)
	if err != nil {
		return Job{}, false, err
	}
	affinity := AffinityFromMetaPath(selected.MetaPath)
	if cfg.ControlPlane.Replication.Enabled {
		affinity, err = SelectRestoreAffinityWithStorage(ctx, store, selected.ID, selected.MetaPath, cfg.ControlPlane.Replication, cfg.ControlPlane.Storage)
		if err != nil {
			return Job{}, false, err
		}
	}
	payload, _ := json.Marshal(restoreGenerationPayload{Repository: rec.FullName, GenerationID: generationID, Target: target, ApprovalID: approvalID})
	jobTarget := RestoreJobTarget(rec.FullName, generationID, target)
	if approvalID != "" {
		jobTarget += "@" + approvalID
	}
	if priority <= 0 {
		priority = 200
	}
	return store.Enqueue(ctx, Job{Kind: "restore-generation", Target: jobTarget, Payload: string(payload), Affinity: affinity, Priority: priority, MaxAttempts: cfg.ControlPlane.Workers.MaxAttempts})
}
