package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
	"github.com/Homiakus/repoark/internal/objectinventory"
	"github.com/Homiakus/repoark/internal/replication"
	"github.com/Homiakus/repoark/internal/storagehealth"
)

type AgentClient struct {
	Config config.Config
	Runner Runner
}

func (a AgentClient) Run(ctx context.Context) error {
	cfg := a.Config.ControlPlane.Agents
	cert, err := tls.LoadX509KeyPair(cfg.ClientCertPath, cfg.ClientKeyPath)
	if err != nil {
		return err
	}
	agentName := cfg.AgentName
	if len(cert.Certificate) > 0 {
		if parsed, e := x509.ParseCertificate(cert.Certificate[0]); e == nil {
			if len(parsed.DNSNames) > 0 && strings.TrimSpace(parsed.DNSNames[0]) != "" {
				agentName = strings.TrimSpace(parsed.DNSNames[0])
			} else if strings.TrimSpace(parsed.Subject.CommonName) != "" {
				agentName = strings.TrimSpace(parsed.Subject.CommonName)
			}
		}
	}
	caPEM, err := os.ReadFile(cfg.CAPath)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("failed to parse agent CA")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: pool}}
	client := &http.Client{Transport: tr, Timeout: 45 * time.Second}
	base := strings.TrimRight(cfg.ServerURL, "/")
	remoteStore := NewRemoteStore(client, base, agentName)
	replicationPub := ""
	if a.Config.ControlPlane.Replication.Enabled {
		replicationPub, err = replication.EnsureKey(cfg.ReplicationKeyPath)
		if err != nil {
			return err
		}
	}
	a.Runner.Store = remoteStore
	heartbeat, _ := time.ParseDuration(cfg.Heartbeat)
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	poll, _ := time.ParseDuration(a.Config.ControlPlane.Workers.PollInterval)
	if poll <= 0 {
		poll = 2 * time.Second
	}
	hbTicker := time.NewTicker(heartbeat)
	defer hbTicker.Stop()
	workTicker := time.NewTicker(poll)
	defer workTicker.Stop()
	invInterval, _ := time.ParseDuration(a.Config.ControlPlane.Storage.InventoryInterval)
	if invInterval <= 0 {
		invInterval = 10 * time.Minute
	}
	var cachedInv objectinventory.Inventory
	var lastInventory time.Time
	sendHeartbeat := func() error {
		maxProbe, _ := time.ParseDuration(a.Config.ControlPlane.Storage.MaxProbe)
		report := storagehealth.Probe(a.Config.ControlPlane.Generations.Root, storagehealth.Thresholds{MinFreeBytes: uint64(max64(a.Config.ControlPlane.Storage.MinFreeBytes, 0)), MinFreePercent: a.Config.ControlPlane.Storage.MinFreePercent, MaxProbe: maxProbe})
		disk := storagehealth.ProbeDisk(ctx, a.Config.ControlPlane.Storage.DiskTelemetry)
		if disk.Health == storagehealth.Unhealthy || (disk.Health == storagehealth.Degraded && report.Health == storagehealth.Healthy) {
			report.Health = disk.Health
		}
		if disk.Error != "" {
			if report.Error != "" {
				report.Error += "; "
			}
			report.Error += "disk telemetry: " + disk.Error
		}
		if a.Config.ControlPlane.Storage.InventoryEnabled && (lastInventory.IsZero() || time.Since(lastInventory) >= invInterval) {
			if inv, e := objectinventory.Build(a.Config.CAS.Root); e == nil {
				cachedInv, lastInventory = inv, time.Now()
			}
		}
		return agentHeartbeat(ctx, client, base, cfg.Labels, replicationPub, report, disk, cachedInv)
	}
	if err := sendHeartbeat(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hbTicker.C:
			_ = sendHeartbeat()
		case <-workTicker.C:
			var jobs []Job
			if err := agentPost(ctx, client, base+"/api/v1/agent/lease", nil, &jobs); err != nil {
				continue
			}
			for _, j := range jobs {
				remoteStore.SetJob(j.ID, j.Target)
				err := a.Runner.Run(ctx, j)
				remoteStore.ClearJob()
				if err == nil {
					_ = agentPost(ctx, client, base+"/api/v1/agent/jobs/"+j.ID+"/complete", nil, nil)
				} else {
					_ = agentPost(ctx, client, base+"/api/v1/agent/jobs/"+j.ID+"/fail", agentFailRequest{Error: err.Error()}, nil)
				}
			}
		}
	}
}
func agentHeartbeat(ctx context.Context, c *http.Client, base string, labels map[string]string, replicationPublicKey string, storage storagehealth.Report, disk storagehealth.DiskReport, inv objectinventory.Inventory) error {
	copyLabels := map[string]string{}
	for k, v := range labels {
		copyLabels[k] = v
	}
	if _, ok := copyLabels["role"]; !ok {
		copyLabels["role"] = "backup-worker"
	}
	req := agentHeartbeatRequest{Labels: copyLabels, ReplicationPublicKey: replicationPublicKey, StorageHealth: storage.Health, StorageTotalBytes: int64(storage.TotalBytes), StorageFreeBytes: int64(storage.FreeBytes), StorageFreePercent: storage.FreePercent, StorageProbeMS: storage.ProbeMS, StorageError: storage.Error, DiskRiskScore: disk.RiskScore, DiskModel: disk.Model, DiskSerial: disk.Serial, DiskTemperatureC: disk.TemperatureC, DiskPercentageUsed: disk.PercentageUsed, DiskMediaErrors: disk.MediaErrors, DiskCriticalWarning: disk.CriticalWarning}
	if inv.MerkleRoot != "" {
		req.InventoryRoot, req.InventoryObjects, req.InventoryBytes, req.InventoryJSON = inv.MerkleRoot, inv.Objects, inv.Bytes, objectinventory.EncodeCompact(inv)
	}
	return agentPost(ctx, c, base+"/api/v1/agent/heartbeat", req, nil)
}

func max64(v, min int64) int64 {
	if v < min {
		return min
	}
	return v
}
func agentPost(ctx context.Context, c *http.Client, url string, body any, out any) error {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	return decodeJSON(resp, out)
}
func (a AgentClient) String() string {
	return fmt.Sprintf("agent %s -> %s", a.Config.ControlPlane.Agents.AgentName, a.Config.ControlPlane.Agents.ServerURL)
}
