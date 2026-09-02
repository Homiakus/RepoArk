package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/signing"
)

type Checkpoint struct {
	Seq       uint64    `json:"seq"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	Signature string    `json:"signature"`
}

type checkpointPayload struct {
	Seq       uint64    `json:"seq"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
}

// WriteCheckpoint verifies and signs the current ledger head while holding the
// same process/file guard used by Append. The checkpoint cannot therefore be
// written for a head that changes between verification and persistence.
func WriteCheckpoint(ledgerPath, keyPath string) error {
	release, err := acquireLedgerGuard(ledgerPath)
	if err != nil {
		return err
	}
	defer release()
	_, head, err := scanVerifiedLocked(ledgerPath, nil)
	if err != nil {
		return err
	}
	return writeCheckpointForHeadLocked(ledgerPath, keyPath, head)
}

func writeCheckpointForHeadLocked(ledgerPath, keyPath string, head Record) error {
	if head.Seq == 0 || strings.TrimSpace(head.Hash) == "" {
		return errors.New("cannot checkpoint an empty audit ledger")
	}
	priv, pub, err := signing.EnsureKey(keyPath)
	if err != nil {
		return err
	}
	payload := checkpointPayload{Seq: head.Seq, Hash: head.Hash, CreatedAt: time.Now().UTC()}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cp := Checkpoint{Seq: payload.Seq, Hash: payload.Hash, CreatedAt: payload.CreatedAt, Signature: signing.Sign(priv, b)}
	out, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		return err
	}
	// Publish the companion public key first; the checkpoint JSON is the commit
	// point and is replaced atomically only after all prerequisites succeed.
	if err := signing.WritePublic(ledgerPath+".checkpoint.pub", pub); err != nil {
		return err
	}
	return atomicWriteAuditFile(ledgerPath+".checkpoint.json", out, 0o600)
}

func VerifyCheckpoint(ledgerPath, trustedPublicKeyPath string) error {
	release, err := acquireLedgerGuard(ledgerPath)
	if err != nil {
		return err
	}
	defer release()

	b, err := os.ReadFile(ledgerPath + ".checkpoint.json")
	if err != nil {
		return err
	}
	var cp Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return err
	}
	_, head, err := scanVerifiedLocked(ledgerPath, nil)
	if err != nil {
		return err
	}
	if head.Seq != cp.Seq || !strings.EqualFold(head.Hash, cp.Hash) {
		return errors.New("audit checkpoint does not match ledger head")
	}
	payload, err := json.Marshal(checkpointPayload{Seq: cp.Seq, Hash: cp.Hash, CreatedAt: cp.CreatedAt})
	if err != nil {
		return err
	}
	pub, err := signing.LoadPublic(trustedPublicKeyPath)
	if err != nil {
		return err
	}
	return signing.Verify(pub, payload, cp.Signature)
}

func atomicWriteAuditFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".repoark-audit-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		return os.Rename(name, path)
	}
	return nil
}
