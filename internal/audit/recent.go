package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const maxRecentRecords = 4096

// Recent returns at most limit verified records matching action, newest first.
// An empty action matches every record. The ledger is verified before any
// records are returned so callers never build operational state from a broken
// hash chain.
func Recent(path string, limit int, action string) ([]Record, error) {
	if limit <= 0 {
		return []Record{}, nil
	}
	if limit > maxRecentRecords {
		limit = maxRecentRecords
	}
	if _, err := Verify(path); err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	action = strings.TrimSpace(action)
	ring := make([]Record, limit)
	count := 0
	next := 0
	s := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 8<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("audit recent record: %w", err)
		}
		if action != "" && r.Action != action {
			continue
		}
		ring[next] = r
		next = (next + 1) % limit
		if count < limit {
			count++
		}
	}
	if err := s.Err(); err != nil {
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
