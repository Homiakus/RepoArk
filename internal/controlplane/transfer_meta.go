package controlplane

import (
	"encoding/json"
	"errors"
)

type transferSourceMeta struct {
	Kind                    string
	TransferID              string
	SourceAgent             string
	TargetAgent             string
	TargetReplicationPubKey string
	GenerationID            string
	RepositoryID            string
	Repository              string
	CAS                     *replicateCASPayload
}

func decodeTransferSourceJob(j Job, agent, transfer string) (transferSourceMeta, error) {
	switch j.Kind {
	case "replicate-generation":
		var p replicateGenerationPayload
		if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
			return transferSourceMeta{}, errors.New("invalid replication job")
		}
		if transfer == "" || transfer != p.TransferID || p.SourceAgent != agent {
			return transferSourceMeta{}, errors.New("replication transfer identity mismatch")
		}
		return transferSourceMeta{Kind: j.Kind, TransferID: p.TransferID, SourceAgent: p.SourceAgent, TargetAgent: p.TargetAgent, TargetReplicationPubKey: p.TargetReplicationPubKey, GenerationID: p.GenerationID, RepositoryID: p.RepositoryID, Repository: p.Repository}, nil
	case "replicate-cas":
		var p replicateCASPayload
		if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
			return transferSourceMeta{}, errors.New("invalid CAS replication job")
		}
		if transfer == "" || transfer != p.TransferID || p.SourceAgent != agent {
			return transferSourceMeta{}, errors.New("CAS transfer identity mismatch")
		}
		return transferSourceMeta{Kind: j.Kind, TransferID: p.TransferID, SourceAgent: p.SourceAgent, TargetAgent: p.TargetAgent, TargetReplicationPubKey: p.TargetReplicationPubKey, GenerationID: "cas:" + p.SourceAgent + ">" + p.TargetAgent, RepositoryID: "__cas__", CAS: &p}, nil
	default:
		return transferSourceMeta{}, errors.New("job is not a replication source")
	}
}
