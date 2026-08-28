package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	privatePEMType = "REPOARK ED25519 PRIVATE KEY"
	publicPEMType  = "REPOARK ED25519 PUBLIC KEY"
)

func EnsureKey(path string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if path == "" {
		return nil, nil, errors.New("empty signing key path")
	}
	if b, err := os.ReadFile(path); err == nil {
		priv, err := parsePrivate(b)
		if err != nil {
			return nil, nil, err
		}
		return priv, priv.Public().(ed25519.PublicKey), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	block := &pem.Block{Type: privatePEMType, Bytes: []byte(priv)}
	if err := atomicWrite(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, nil, err
	}
	if err := WritePublic(path+".pub", pub); err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

func LoadPrivate(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePrivate(b)
}

func parsePrivate(b []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != privatePEMType {
		return nil, errors.New("invalid RepoArk Ed25519 private key")
	}
	if len(block.Bytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key size: %d", len(block.Bytes))
	}
	return ed25519.PrivateKey(append([]byte(nil), block.Bytes...)), nil
}

func WritePublic(path string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 public key")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicWrite(path, pem.EncodeToMemory(&pem.Block{Type: publicPEMType, Bytes: []byte(pub)}), 0o644)
}

func LoadPublic(path string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != publicPEMType || len(block.Bytes) != ed25519.PublicKeySize {
		return nil, errors.New("invalid RepoArk Ed25519 public key")
	}
	return ed25519.PublicKey(append([]byte(nil), block.Bytes...)), nil
}

func Sign(priv ed25519.PrivateKey, data []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
}

func Verify(pub ed25519.PublicKey, data []byte, encoded string) error {
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("Ed25519 signature verification failed")
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".repoark-key-*")
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
