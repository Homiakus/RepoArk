package audit

import (
	"errors"
	"os"
	"strings"
)

const maxRecentRecords = 4096

// Recent returns at most limit verified records matching action, newest first.
// An empty action matches every record. Matching records are collected during
// the same verified scan, so there is no verify/read TOCTOU window.
func Recent(path string, limit int, action string) ([]Record, error) {
	if limit <= 0 {
		return []Record{}, nil
	}
	if limit > maxRecentRecords {
		limit = maxRecentRecords
	}

	release, err := acquireLedgerGuard(path)
	if err != nil {
		return nil, err
	}
	defer release()

	action = strings.TrimSpace(action)
	ring := make([]Record, limit)
	count := 0
	next := 0
	_, _, err = scanVerifiedLocked(path, func(r Record) {
		if action != "" && r.Action != action {
			return
		}
		ring[next] = r
		next = (next + 1) % limit
		if count < limit {
			count++
		}
	})
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return []Record{}, nil
	}

	out := make([]Record, 0, count)
	start := next - 1
	if start < 0 {
		start += limit
	}
	for i := 0; i < count; i++ {
		idx := start - i
		for idx < 0 {
			idx += limit
		}
		out = append(out, ring[idx])
	}
	return out, nil
}

// RecentIfExists is equivalent to Recent but treats an absent ledger as an
// empty history. It does not hide permission, parse, sequence, or hash errors.
func RecentIfExists(path string, limit int, action string) ([]Record, error) {
	records, err := Recent(path, limit, action)
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, nil
	}
	return records, err
}
