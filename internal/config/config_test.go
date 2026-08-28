package config

import "testing"

func TestDefaultValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestVerificationPolicyCannotBeDisabledAccidentally(t *testing.T) {
	cfg := Default()
	cfg.Security.RequireVerification = true
	cfg.Backup.VerifyAfterBackup = false
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected verification policy validation error")
	}
}

func TestDaemonIntervalMustBePositive(t *testing.T) {
	cfg := Default()
	cfg.Daemon.Interval = "0s"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-positive interval validation error")
	}
}

func TestReplicationPlacementValidation(t *testing.T) {
	cfg := Default()
	cfg.ControlPlane.Enabled = true
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.Factor = 2
	cfg.ControlPlane.Replication.MinHealthy = 2
	cfg.ControlPlane.Replication.TransferTTL = "2h"
	cfg.ControlPlane.Replication.FailureDomainLabel = "zone"
	cfg.ControlPlane.Replication.MinFailureDomains = 3
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected failure-domain validation error")
	}
	cfg.ControlPlane.Replication.MinFailureDomains = 2
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid placement rejected: %v", err)
	}
}

func TestReplicationTransferTTLMustBePositive(t *testing.T) {
	cfg := Default()
	cfg.ControlPlane.Enabled = true
	cfg.ControlPlane.Replication.Enabled = true
	cfg.ControlPlane.Replication.TransferTTL = "0s"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected transfer TTL validation error")
	}
}

func TestObjectReplicationRequiresHAAgentTransport(t *testing.T) {
	cfg := Default()
	cfg.ControlPlane.Enabled = true
	cfg.ControlPlane.Storage.ObjectReplicationFactor = 2
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected object replication to require HA replication transport")
	}
	cfg.ControlPlane.Replication.Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected object replication to require mTLS agents")
	}
	cfg.ControlPlane.Agents.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid object replication config rejected: %v", err)
	}
}

func TestWebAuthRequiresRBACGroup(t *testing.T) {
	cfg := Default()
	cfg.ControlPlane.Enabled = true
	cfg.ControlPlane.WebAuth.Enabled = true
	cfg.ControlPlane.WebAuth.Issuer = "https://id.example.test"
	cfg.ControlPlane.WebAuth.ClientID = "repoark"
	cfg.ControlPlane.WebAuth.RedirectURL = "https://repoark.example.test/auth/callback"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected web auth without role groups to fail")
	}
	cfg.ControlPlane.WebAuth.ViewerGroups = []string{"repoark-viewers"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid web auth config rejected: %v", err)
	}
}
