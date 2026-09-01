#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

# SOURCE_SHA256SUMS.txt intentionally excludes itself to avoid recursion.
# The historical pre-recovery inventory remains a normal hashed input.
git ls-files -z \
  | sort -z \
  | while IFS= read -r -d '' file; do
      if [[ "$file" == "SOURCE_SHA256SUMS.txt" ]]; then
        continue
      fi
      sum="$(sha256sum -- "$file" | awk '{print $1}')"
      printf '%s  ./%s\n' "$sum" "$file"
    done
