# HA / Distributed-Storage Chaos Test Matrix

RepoArk v0.7 extends the deterministic v0.6 failure-state suite from generation availability into storage health, resumable transfer and object-layer behavior.

## Built-in deterministic cases

| Failure | Expected invariant |
|---|---|
| Original storage agent heartbeat expires | restore selects another online `ready` replica |
| Original storage agent returns | restore routing safely prefers original owner again |
| One of two replicas offline with `min_healthy=2` | health reports quorum deficit |
| Two replicas online but both in same configured `zone` | health reports failure-domain deficit |
| Spare agent exists in a new zone | reconciler queues replacement to the new zone |
| No online ready replica | restore is rejected, never routed to an empty worker |
| Storage probe reports `unhealthy` | node is excluded from restore routing and placement |
| Storage probe reports `degraded` with evacuation enabled | old copy remains readable, stops satisfying durable factor, healthy replacement is queued |
| Candidate cannot fit generation while preserving free-space reserve | candidate is skipped |
| Old v0.6 agent reports no storage health | readable during rolling upgrade, but not a new/durable placement target |
| Upload connection fails after server fsync | retry resumes from authoritative server offset |
| Finalize response is lost | finalize is idempotent and does not duplicate install work |
| Download local partial is already complete and hash-valid | transfer succeeds without another network request |
| Source/target CAS Merkle roots differ in one prefix | reconciliation scopes work to the divergent prefix |
| CAS archive contains unsafe path/type | install is rejected before CAS mutation |
| CAS object bytes do not match digest filename | install is rejected |
| Erasure parity loses up to recoverable shard count | original object reconstructs and final SHA-256 matches |
| Source crashes after relay upload | encrypted transfer remains durable until target install or TTL cleanup |
| Transfer TTL expires | spool blob and transfer state are reaped |
| Wrong mTLS identity requests relay download | request is rejected |
| Ciphertext bit is modified | AES-GCM authentication rejects transfer |
| Worker dies on final permitted job attempt | lease reaper marks job terminal failed; no attempt `max+1` |

## CI

`.github/workflows/ha-chaos.yml` continues the generation/replication failure-state suite.

`.github/workflows/distributed-storage.yml` repeatedly runs compact inventory, CAS reconciliation/archive, erasure recovery, storage-health and resumable relay tests under Go's race detector.

These workflows are deterministic software-level chaos. They do not claim to emulate every real filesystem/controller failure.

## Recommended production drill

At least quarterly:

1. block GitHub access from RepoArk;
2. mark/power off the preferred storage agent;
3. degrade or capacity-limit a second storage node;
4. retain a verified replica in another failure domain;
5. inspect `control replicas` and `control inventory`;
6. restore an existing immutable generation through the approval workflow;
7. verify checksum/signature and measured RTO;
8. interrupt one large replication transfer and confirm resume rather than restart;
9. reconstruct at least one erasure-protected test object after deleting a permitted number of shards;
10. return the original storage node and verify safe routing failback without deleting redundant copies.
