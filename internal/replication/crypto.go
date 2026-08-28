package replication

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	magic      = "RARKRPL1"
	chunkSize  = 1 << 20
	maxFrame   = chunkSize + 128
	keyVersion = "x25519-v1"
)

type KeyInfo struct {
	Version   string `json:"version"`
	PublicKey string `json:"public_key"`
}

func EnsureKey(path string) (publicBase64 string, err error) {
	if path == "" {
		return "", errors.New("replication key path is empty")
	}
	if b, err := os.ReadFile(path); err == nil {
		priv, err := parsePrivate(b)
		if err != nil {
			return "", err
		}
		return base64.RawStdEncoding.EncodeToString(priv.PublicKey().Bytes()), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	encoded := base64.RawStdEncoding.EncodeToString(priv.Bytes()) + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(priv.PublicKey().Bytes()), nil
}

func PublicFromPrivate(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	priv, err := parsePrivate(b)
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(priv.PublicKey().Bytes()), nil
}

func Encrypt(dst io.Writer, src io.Reader, destinationPublicBase64 string) error {
	pubRaw, err := base64.RawStdEncoding.DecodeString(destinationPublicBase64)
	if err != nil {
		return fmt.Errorf("decode destination public key: %w", err)
	}
	dest, err := ecdh.X25519().NewPublicKey(pubRaw)
	if err != nil {
		return fmt.Errorf("destination public key: %w", err)
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	shared, err := eph.ECDH(dest)
	if err != nil {
		return err
	}
	key := deriveKey(shared, eph.PublicKey().Bytes(), dest.Bytes())
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(dst)
	if _, err := bw.WriteString(magic); err != nil {
		return err
	}
	if _, err := bw.Write(eph.PublicKey().Bytes()); err != nil {
		return err
	}
	buf := make([]byte, chunkSize)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			nonce := make([]byte, gcm.NonceSize())
			if _, err := rand.Read(nonce); err != nil {
				return err
			}
			sealed := gcm.Seal(nil, nonce, buf[:n], nil)
			if len(sealed) > maxFrame {
				return errors.New("replication frame exceeded maximum")
			}
			if err := binary.Write(bw, binary.BigEndian, uint32(len(sealed))); err != nil {
				return err
			}
			if _, err := bw.Write(nonce); err != nil {
				return err
			}
			if _, err := bw.Write(sealed); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := binary.Write(bw, binary.BigEndian, uint32(0)); err != nil {
		return err
	}
	return bw.Flush()
}

func Decrypt(dst io.Writer, src io.Reader, privateKeyPath string) error {
	b, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return err
	}
	priv, err := parsePrivate(b)
	if err != nil {
		return err
	}
	br := bufio.NewReader(src)
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(br, head); err != nil {
		return err
	}
	if string(head) != magic {
		return errors.New("invalid replication stream magic")
	}
	ephRaw := make([]byte, 32)
	if _, err := io.ReadFull(br, ephRaw); err != nil {
		return err
	}
	eph, err := ecdh.X25519().NewPublicKey(ephRaw)
	if err != nil {
		return err
	}
	shared, err := priv.ECDH(eph)
	if err != nil {
		return err
	}
	key := deriveKey(shared, ephRaw, priv.PublicKey().Bytes())
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	for {
		var n uint32
		if err := binary.Read(br, binary.BigEndian, &n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if n > maxFrame {
			return errors.New("replication frame too large")
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err := io.ReadFull(br, nonce); err != nil {
			return err
		}
		sealed := make([]byte, n)
		if _, err := io.ReadFull(br, sealed); err != nil {
			return err
		}
		plain, err := gcm.Open(nil, nonce, sealed, nil)
		if err != nil {
			return fmt.Errorf("replication authentication failed: %w", err)
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
	}
}

func parsePrivate(b []byte) (*ecdh.PrivateKey, error) {
	raw, err := base64.RawStdEncoding.DecodeString(stringTrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("decode replication private key: %w", err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("replication private key: %w", err)
	}
	return priv, nil
}

func deriveKey(shared, ephemeralPublic, destinationPublic []byte) []byte {
	// HKDF-SHA256 (extract+single-block expand) implemented locally to keep the
	// replication crypto dependency-free. 32 bytes are sufficient for AES-256.
	salt := []byte("repoark-replication-x25519-v1")
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(shared)
	prk := extract.Sum(nil)
	expand := hmac.New(sha256.New, prk)
	_, _ = expand.Write([]byte(keyVersion))
	_, _ = expand.Write(ephemeralPublic)
	_, _ = expand.Write(destinationPublic)
	_, _ = expand.Write([]byte{1})
	return expand.Sum(nil)[:32]
}

func stringTrimSpace(s string) string {
	for len(s) > 0 {
		c := s[0]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			break
		}
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
