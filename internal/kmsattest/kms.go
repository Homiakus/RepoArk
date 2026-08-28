package kmsattest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/execx"
)

type Envelope struct {
	Version          int       `json:"version"`
	Provider         string    `json:"provider"`
	KeyID            string    `json:"key_id"`
	SigningAlgorithm string    `json:"signing_algorithm"`
	MessageType      string    `json:"message_type"`
	Signature        string    `json:"signature_base64"`
	CreatedAt        time.Time `json:"created_at"`
}

func SignFile(ctx context.Context, path string, cfg config.KMSAttestationConfig) (string, error) {
	if !cfg.Enabled {
		return "", errors.New("KMS attestation disabled")
	}
	if !execx.Exists("aws") {
		return "", errors.New("aws CLI not found")
	}
	alg := strings.TrimSpace(cfg.SigningAlgorithm)
	if alg == "" {
		alg = "ED25519_SHA_512"
	}
	args := []string{"kms", "sign", "--key-id", cfg.KeyID, "--message", "fileb://" + path, "--message-type", "RAW", "--signing-algorithm", alg, "--output", "json"}
	args = append(args, commonArgs(cfg)...)
	res, err := execx.Run(ctx, "", nil, "aws", args...)
	if err != nil {
		return "", err
	}
	var out struct {
		KeyID            string `json:"KeyId"`
		Signature        string `json:"Signature"`
		SigningAlgorithm string `json:"SigningAlgorithm"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		return "", fmt.Errorf("decode KMS sign response: %w", err)
	}
	if out.Signature == "" {
		return "", errors.New("KMS returned empty signature")
	}
	env := Envelope{Version: 1, Provider: "aws-kms", KeyID: out.KeyID, SigningAlgorithm: out.SigningAlgorithm, MessageType: "RAW", Signature: out.Signature, CreatedAt: time.Now().UTC()}
	b, _ := json.MarshalIndent(env, "", "  ")
	dst := path + ".kms.json"
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func VerifyFile(ctx context.Context, path string, cfg config.KMSAttestationConfig) error {
	envPath := path + ".kms.json"
	b, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return err
	}
	if env.Provider != "aws-kms" || env.KeyID == "" || env.Signature == "" {
		return errors.New("invalid KMS attestation envelope")
	}
	configuredKeyID := strings.TrimSpace(cfg.KeyID)
	if configuredKeyID == "" {
		return errors.New("KMS verification requires configured key_id trust anchor")
	}
	configuredAlg := strings.ToUpper(strings.TrimSpace(cfg.SigningAlgorithm))
	if configuredAlg == "" {
		configuredAlg = "ED25519_SHA_512"
	}
	if strings.ToUpper(strings.TrimSpace(env.SigningAlgorithm)) != configuredAlg {
		return fmt.Errorf("KMS attestation algorithm %q does not match configured trust policy %q", env.SigningAlgorithm, configuredAlg)
	}
	if strings.ToUpper(strings.TrimSpace(env.MessageType)) != "RAW" {
		return fmt.Errorf("unsupported KMS attestation message type %q; expected RAW", env.MessageType)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("decode KMS signature: %w", err)
	}
	dir := filepath.Dir(envPath)
	f, err := os.CreateTemp(dir, ".repoark-kms-sig-*")
	if err != nil {
		return err
	}
	sigPath := f.Name()
	defer os.Remove(sigPath)
	_ = f.Chmod(0o600)
	if _, err := f.Write(sig); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args := []string{"kms", "verify", "--key-id", configuredKeyID, "--message", "fileb://" + path, "--message-type", "RAW", "--signature", "fileb://" + sigPath, "--signing-algorithm", configuredAlg, "--output", "json"}
	args = append(args, commonArgs(cfg)...)
	res, err := execx.Run(ctx, "", nil, "aws", args...)
	if err != nil {
		return err
	}
	var out struct {
		SignatureValid bool `json:"SignatureValid"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		return fmt.Errorf("decode KMS verify response: %w", err)
	}
	if !out.SignatureValid {
		return errors.New("KMS signature invalid")
	}
	return nil
}

func commonArgs(cfg config.KMSAttestationConfig) []string {
	var a []string
	if strings.TrimSpace(cfg.Region) != "" {
		a = append(a, "--region", cfg.Region)
	}
	if strings.TrimSpace(cfg.Profile) != "" {
		a = append(a, "--profile", cfg.Profile)
	}
	if strings.TrimSpace(cfg.EndpointURL) != "" {
		a = append(a, "--endpoint-url", cfg.EndpointURL)
	}
	return a
}
