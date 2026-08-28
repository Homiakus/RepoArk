package controlplane

import (
	"context"
	"fmt"
	"sort"

	"github.com/Homiakus/repoark/internal/objectinventory"
)

type AgentInventory struct {
	AgentID  string                    `json:"agent_id"`
	Root     string                    `json:"root,omitempty"`
	Objects  int                       `json:"objects"`
	Bytes    int64                     `json:"bytes"`
	Segments []objectinventory.Segment `json:"segments,omitempty"`
	Storage  string                    `json:"storage_health,omitempty"`
}

type InventoryComparison struct {
	Left              string   `json:"left"`
	Right             string   `json:"right"`
	Equal             bool     `json:"equal"`
	DivergentPrefixes []string `json:"divergent_prefixes,omitempty"`
}

func AgentInventories(ctx context.Context, store Store) ([]AgentInventory, error) {
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentInventory, 0, len(agents))
	for _, a := range agents {
		row := AgentInventory{AgentID: a.ID, Root: a.InventoryRoot, Objects: a.InventoryObjects, Bytes: a.InventoryBytes, Storage: a.StorageHealth}
		if a.InventoryJSON != "" {
			if inv, e := objectinventory.DecodeCompact(a.InventoryJSON); e == nil {
				row.Segments = inv.Segments
				if row.Root == "" {
					row.Root = inv.MerkleRoot
				}
				if row.Objects == 0 {
					row.Objects = inv.Objects
				}
				if row.Bytes == 0 {
					row.Bytes = inv.Bytes
				}
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

// CompareInventories performs the first Merkle reconciliation step: identical
// roots need no work; otherwise only divergent 2-hex-prefix segments need a
// detailed object walk/transfer. This keeps heartbeat inventory bounded to 256
// segment roots regardless of CAS size.
func CompareInventories(ctx context.Context, store Store, leftID, rightID string) (InventoryComparison, error) {
	left, err := store.GetAgent(ctx, leftID)
	if err != nil {
		return InventoryComparison{}, err
	}
	right, err := store.GetAgent(ctx, rightID)
	if err != nil {
		return InventoryComparison{}, err
	}
	if left.InventoryJSON == "" || right.InventoryJSON == "" {
		return InventoryComparison{}, fmt.Errorf("inventory unavailable for one or both agents")
	}
	a, err := objectinventory.DecodeCompact(left.InventoryJSON)
	if err != nil {
		return InventoryComparison{}, err
	}
	b, err := objectinventory.DecodeCompact(right.InventoryJSON)
	if err != nil {
		return InventoryComparison{}, err
	}
	diff := objectinventory.DivergentPrefixes(a, b)
	return InventoryComparison{Left: leftID, Right: rightID, Equal: len(diff) == 0, DivergentPrefixes: diff}, nil
}
