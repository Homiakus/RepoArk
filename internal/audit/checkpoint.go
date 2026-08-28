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

func WriteCheckpoint(ledgerPath, keyPath string) error {
	head, err := Head(ledgerPath)
	if err != nil {
		return err
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
	if err := os.WriteFile(ledgerPath+".checkpoint.json", out, 0o600); err != nil {
		return err
	}
	return signing.WritePublic(ledgerPath+".checkpoint.pub", pub)
}

func VerifyCheckpoint(ledgerPath, trustedPublicKeyPath string) error {
	b, err := os.ReadFile(ledgerPath + ".checkpoint.json")
	if err != nil {
		return err
	}
	var cp Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return err
	}
	head, err := Head(ledgerPath)
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
