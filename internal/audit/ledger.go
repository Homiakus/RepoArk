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

// Append verifies the complete existing ledger before deriving the next
// sequence/hash and writing a new record. It therefore refuses to extend a
// ledger whose tamper-evident chain is already invalid.
func Append(path, action, target, status, detail string, fields map[string]any) (Record, error) {
	if err := prepareLedgerDir(path); err != nil {
		return Record{}, err
	}
	release, err := acquireLedgerGuard(path)
	if err != nil {
		return Record{}, err
	}
	defer release()
	return appendLocked(path, action, target, status, detail, fields)
}

// AppendWithCheckpoint atomically, with respect to other RepoArk processes
// using the same ledger, appends a verified record and advances the signed
// checkpoint to exactly that new head before releasing the ledger guard.
func AppendWithCheckpoint(path, keyPath, action, target, status, detail string, fields map[string]any) (Record, error) {
	if err := prepareLedgerDir(path); err != nil {
		return Record{}, err
	}
	release, err := acquireLedgerGuard(path)
	if err != nil {
		return Record{}, err
	}
	defer release()

	r, err := appendLocked(path, action, target, status, detail, fields)
	if err != nil {
		return Record{}, err
	}
	if err := writeCheckpointForHeadLocked(path, keyPath, r); err != nil {
		return r, err
	}
	return r, nil
}

func appendLocked(path, action, target, status, detail string, fields map[string]any) (Record, error) {
	_, last, err := scanVerifiedLocked(path, nil)
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

func prepareLedgerDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("audit path is empty")
	}
	return os.MkdirAll(filepath.Dir(path), 0o700)
}

func Verify(path string) (int, error) {
	release, err := acquireLedgerGuard(path)
	if err != nil {
		return 0, err
	}
	defer release()
	count, _, err := scanVerifiedLocked(path, nil)
	return count, err
}

// scanVerifiedLocked validates the complete ledger and returns both its record
// count and verified head. visit is called only after each record's sequence,
// previous hash and own hash have been validated. The caller must hold the
// ledger guard for the entire scan.
func scanVerifiedLocked(path string, visit func(Record)) (int, Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, Record{}, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 8<<20)
	var prev string
	var head Record
	var wantSeq uint64 = 1
	count := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return count, head, fmt.Errorf("audit line %d: %w", count+1, err)
		}
		if r.Seq != wantSeq {
			return count, head, fmt.Errorf("audit sequence mismatch at record %d: got %d", wantSeq, r.Seq)
		}
		if r.PrevHash != prev {
			return count, head, fmt.Errorf("audit prev_hash mismatch at record %d", r.Seq)
		}
		h, err := hashRecord(r)
		if err != nil {
			return count, head, err
		}
		if !strings.EqualFold(h, r.Hash) {
			return count, head, fmt.Errorf("audit hash mismatch at record %d", r.Seq)
		}
		head = r
		prev = r.Hash
		wantSeq++
		count++
		if visit != nil {
			visit(r)
		}
	}
	if err := s.Err(); err != nil {
		return count, head, err
	}
	return count, head, nil
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
	release, err := acquireLedgerGuard(path)
	if err != nil {
		return Record{}, err
	}
	defer release()
	_, head, err := scanVerifiedLocked(path, nil)
	return head, err
}
