# Immutable Backup Generations

The old `latest` backup layout is convenient for fast recovery but is mutable by design. v0.5 adds repository generations for point-in-time recovery.

A generation directory contains:

- `generation.json`;
- optional `generation.json.sig`;
- `repo.bundle` for a non-empty repository, or `mirror.tar.gz` for an empty repository;
- optional `lfs.tar.gz`;
- SHA-256 values in metadata.

When the current bundle and generation root share a filesystem, RepoArk first tries a hard link. The current bundle is built into a temporary file and atomically renamed, so later backup cycles do not mutate the inode preserved by the historical generation. If hard linking is impossible, RepoArk copies and fsyncs the file.

When signing is enabled, restore through `--generation` verifies the detached generation signature against the external signing trust anchor before trusting the embedded hashes.

Retention is per repository via `keep_per_repo`.


## Control-plane restore staging

`control_plane.generations.restore_root` is local to each worker or agent. `repoark control restore OWNER/REPO GENERATION_ID` stages the selected immutable recovery point there unless `--target` is explicitly supplied. For an `agent://` generation the restore job is affinitized to that certificate identity. A control-plane-local generation is affinitized to the reserved `__repoark_local__` worker group, preventing remote agents from accidentally attempting a restore against paths they cannot see.

The SQL index is retention-synchronized with filesystem pruning, so expired generations do not remain advertised as selectable recovery points.
