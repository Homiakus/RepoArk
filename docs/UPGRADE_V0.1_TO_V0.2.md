# Upgrade from RepoArk v0.1 to v0.2

v0.2 is designed to read an existing v0.1 configuration and backup tree.

## Recommended procedure

1. Keep a copy of the v0.1 executable/source and current config.
2. Replace the executable with v0.2.
3. Run:

```bash
repoark doctor
```

4. Inspect the new fields in `configs/config.example.yml` and opt into features such as OCI export, recovery drills, observability and notifications.
5. Ensure/create the signing key:

```bash
repoark keys generate
```

6. Run the first v0.2 backup:

```bash
repoark backup
repoark verify
```

7. Prove recovery:

```bash
repoark drill 1
```

## Existing backups

Old manifests and their absolute artifact paths remain readable. New manifests use relative paths.

Historical unsigned v0.1 manifests are not retroactively signed. The first v0.2 backup creates a new signed latest manifest when `security.sign_manifests: true`.

## New optional external tools

Only install tools for features you enable:

- `skopeo` for GHCR/OCI payload export;
- `rclone` for the rclone off-site backend;
- `restic` for restic off-site backup;
- `git-lfs` for complete LFS payload protection.

## Secret migration

v0.2 keeps tokens and notification secrets in environment variables. If any prior local customization placed a webhook URL or PAT directly in YAML, move it to an environment variable before using v0.2.
