# Upgrade v0.4 → v0.5

v0.5 is backward-compatible at the direct CLI/config level. Existing backup roots, manifests, CAS data, GitLab recovery data and v0.4 commands remain usable.

1. Replace the binary with v0.5.
2. Run `repoark doctor`.
3. Keep `control_plane.enabled: false` if you want exactly the old daemon/direct behavior.
4. To adopt durable orchestration, copy the `control_plane` block from `configs/config.example.yml`, choose `generations.root` + `generations.restore_root`, and enable it.
5. Run `repoark control sync` and inspect `repoark control repos`.
6. Start `repoark control serve` under systemd/container supervision.
7. After the first scheduled backup, inspect `repoark generations list OWNER/REPO`.
8. If using agents, bootstrap or install your PKI before exposing the agent listener.

SQLite is appropriate for one control-plane process. Use PostgreSQL if multiple service instances must share the queue/state database.

The legacy `repoark daemon` is not removed, but do not run it alongside a control-plane scheduler unless duplicate independent backup cycles are intentional.


For remote agents, `restore_root` is evaluated on each agent machine. Keep generation storage persistent and either replicate it independently or accept that agent-local generations require that storage node for restore. v0.5 will report such unavailable path-bound jobs as stranded; it will not fake storage failover by leasing them elsewhere.
