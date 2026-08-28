# Upgrade v0.7 → v0.8

1. Stop the v0.7 control-plane process and remote agents.
2. Back up the control-plane SQLite/PostgreSQL database and the RepoArk config/PKI directories.
3. Install the v0.8 binary.
4. Start the control plane once. SQL migration is additive and advances the schema marker to `5`.
5. Update the config to schema `8`. Existing v0.7 configs load with safe defaults; new SMART/scrub/tiering/distributed-erasure features remain disabled unless explicitly enabled.
6. Upgrade storage agents before enabling distributed erasure. Mixed-version agents may remain readable during rolling upgrades, but new durability decisions should only use agents reporting current storage telemetry.
7. Run `repoark doctor`, `repoark control stats`, `repoark control replicas`, and `repoark control erasure` before enabling automatic repair/tiering.
8. Enable `disk_telemetry`, then `scrub`, then distributed erasure in separate changes. Observe `/healthz` and Prometheus after each step.

## Important operational notes

- Do not enable distributed erasure without working mTLS agents and HA replication transport.
- Use a meaningful `failure_domain_label` (`zone`, `site`, `rack`) and label agents consistently.
- `shard_replication: 1` means one durable copy per shard index; resilience comes from spreading different shard indices across failure domains plus Reed–Solomon parity.
- Keep generation-level replication enabled even when distributed erasure is used.
- Tiering is copy-only in v0.8; do not manually delete hot CAS objects unless a separate verified retention procedure proves they are recoverable.
