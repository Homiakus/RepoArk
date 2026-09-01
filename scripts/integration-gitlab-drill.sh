#!/usr/bin/env bash
set -euo pipefail

ROOT="${REPOARK_IT_ROOT:-${RUNNER_TEMP:-/tmp}/repoark-gitlab-integration}"
BIN="${REPOARK_BIN:-./repoark}"
IMAGE="${REPOARK_GITLAB_IMAGE:-gitlab/gitlab-ce:19.2.4-ce.0}"
SOURCE_HTTP="${REPOARK_IT_SOURCE_HTTP_PORT:-19080}"
SOURCE_SSH="${REPOARK_IT_SOURCE_SSH_PORT:-19222}"
DRILL_HTTP="${REPOARK_IT_DRILL_HTTP_PORT:-19180}"
DRILL_SSH="${REPOARK_IT_DRILL_SSH_PORT:-19322}"
CFG="$ROOT/config.yml"
DATA="$ROOT/gitlab"
DRILLS="$ROOT/drills"

rm -rf "$ROOT"
mkdir -p "$ROOT" "$DATA" "$DRILLS"

cat > "$CFG" <<YAML
version: 4
gitlab:
  enabled: true
  url: http://127.0.0.1:${SOURCE_HTTP}
  image: ${IMAGE}
  hostname: 127.0.0.1
  http_port: ${SOURCE_HTTP}
  https_port: 19443
  ssh_port: ${SOURCE_SSH}
  data_dir: ${DATA}
  container_name: repoark-gitlab-it-source
  preserve_namespaces: true
  restore_drill:
    enabled: true
    work_dir: ${DRILLS}
    http_port: ${DRILL_HTTP}
    ssh_port: ${DRILL_SSH}
    timeout: 30m
    keep_on_failure: true
policy:
  enabled: false
cas:
  enabled: false
audit:
  enabled: false
YAML

cleanup() {
  if [[ -f "$DATA/compose.yml" ]]; then
    docker compose -f "$DATA/compose.yml" down --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

"$BIN" --config "$CFG" gitlab deploy

echo "Waiting for source GitLab..."
deadline=$((SECONDS + 1800))
# Probe through the published HTTP path used by the backup/restore client.
# GitLab monitoring endpoints such as /-/health are localhost-allowlisted by
# default. Through a Docker-published port the request originates from the
# bridge address, so use the external user-facing sign-in page instead.
until curl -fsS "http://127.0.0.1:${SOURCE_HTTP}/users/sign_in" >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then
    echo "Source GitLab did not become ready" >&2
    docker logs repoark-gitlab-it-source --tail 200 || true
    exit 1
  fi
  sleep 10
done

"$BIN" --config "$CFG" gitlab backup
archive="$(find "$DATA/exports" -maxdepth 1 -type f -name 'repoark-gitlab-*.tar.gz' -printf '%T@ %p\n' | sort -nr | head -1 | cut -d' ' -f2-)"
if [[ -z "$archive" || ! -f "$archive" ]]; then
  echo "GitLab backup archive was not produced" >&2
  exit 1
fi

"$BIN" --config "$CFG" gitlab drill "$archive"
"$BIN" --config "$CFG" policy check >/dev/null 2>&1 || true

echo "GitLab integration restore drill passed: $archive"
