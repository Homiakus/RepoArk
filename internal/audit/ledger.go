package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Record is one tamper-evident event in the RepoArk audit ledger. Hash is the
// SHA-256 of the canonical JSON representation of all preceding fields,
// including PrevHash. Chaining makes removal, insertion and modification
// detectable during Verify.
type Record struct {
	Seq      uint64         `json:"seq"`
	Time     time.Time      `json:"time"`
	Action   string         `json:"action"`
	Target   string         `json:"target,omitempty"`
	Status   string         `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
	PrevHash string         `json:"prev_hash,omitempty"`
	Hash     string         `json:"hash"`
}

type unsignedRecord struct {
	Seq      uint64         `json:"seq"`
	Time     time.Time      `json:"time"`
	Action   string         `json:"action"`
	Target   string         `json:"target,omitempty"`
	Status   string         `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
	PrevHash string         `json:"prev_hash,omitempty"`
}

var mu sync.Mutex

func Append(path, action, target, status, detail string, fields map[string]any) (Record, error) {
	mu.Lock()
	defer mu.Unlock()
	if strings.TrimSpace(path) == "" {
		return Record{}, errors.New("audit path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Record{}, err
	}
	last, err := readLast(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Record{}, err
	}
	r := Record{
		Seq:      last.Seq + 1,
		Time:     time.Now().UTC(),
		Action:   strings.TrimSpace(action),
		Target:   strings.TrimSpace(target),
		Status:   strings.TrimSpace(status),
		Detail:   strings.TrimSpace(detail),
		Fields:   fields,
		PrevHash: last.Hash,
	}
	if r.Action == "" || r.Status == "" {
		return Record{}, errors.New("audit action and status are required")
	}
	h, err := hashRecord(r)
	if err != nil {
		return Record{}, err
	}
	r.Hash = h
	b, err := json.Marshal(r)
	if err != nil {
		return Record{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Record{}, err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return Record{}, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return Record{}, err
	}
	return r, f.Close()
}

func Verify(path string) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	return verifyLocked(path)
}

// verifyLocked validates the complete ledger while the package audit mutex is
// held. Callers that need to verify and then consume the same ledger snapshot
// can keep the mutex across both operations and avoid a verify/read race with
// concurrent Append calls.
func verifyLocked(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 8<<20)
	var prev string
	var wantSeq uint64 = 1
	count := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return count, fmt.Errorf("audit line %d: %w", count+1, err)
		}
		if r.Seq != wantSeq {
			return count, fmt.Errorf("audit sequence mismatch at record %d: got %d", wantSeq, r.Seq)
		}
		if r.PrevHash != prev {
			return count, fmt.Errorf("audit prev_hash mismatch at record %d", r.Seq)
		}
		h, err := hashRecord(r)
		if err != nil {
			return count, err
		}
		if !strings.EqualFold(h, r.Hash) {
			return count, fmt.Errorf("audit hash mismatch at record %d", r.Seq)
		}
		prev = r.Hash
		wantSeq++
		count++
	}
	if err := s.Err(); err != nil {
		return count, err
	}
	return count, nil
}

func readLast(path string) (Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 8<<20)
	var last Record
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return Record{}, err
		}
		last = r
	}
	return last, s.Err()
}

func hashRecord(r Record) (string, error) {
	u := unsignedRecord{Seq: r.Seq, Time: r.Time, Action: r.Action, Target: r.Target, Status: r.Status, Detail: r.Detail, Fields: r.Fields, PrevHash: r.PrevHash}
	b, err := json.Marshal(u)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Head returns the last verified ledger record.
func Head(path string) (Record, error) {
	mu.Lock()
	defer mu.Unlock()
	if _, err := verifyLocked(path); err != nil {
		return Record{}, err
	}
	return readLast(path)
}
