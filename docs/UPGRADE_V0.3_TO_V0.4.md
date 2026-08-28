# Upgrade v0.3 → v0.4

v0.4 reads v0.3 configuration and backup trees. No destructive migration is required.

## Recommended sequence

1. Keep the existing v0.3 backup root untouched.
2. Replace the binary with v0.4.
3. Run `repoark doctor`.
4. Run `repoark verify` before enabling CAS.
5. Enable CAS and run `repoark cas compact`.
6. Run `repoark cas verify`.
7. Run `repoark cas gc --dry-run`; inspect the plan before using real GC.
8. Enable package payload backup only after confirming token scope and expected storage growth.
9. Add RPO/RTO thresholds gradually; avoid turning on drill-age gates before the first successful drill has created state.
10. If using KMS, create/test the asymmetric signing key before setting `require_valid: true`.
11. If using Object Lock, start with GOVERNANCE. Move to COMPLIANCE only after lifecycle/retention has been validated operationally.
12. Run the real GitLab restore integration workflow before treating GitLab as a tested recovery tier.

## Config additions

- `cas`
- `package_payloads`
- `security.kms_attestation`
- extra `offsite.object_lock` policy fields
- `policy.max_*_duration` RTO gates

`repoark init --force` will generate current defaults, but do not overwrite a production config without merging its secret environment-variable names and endpoints.
