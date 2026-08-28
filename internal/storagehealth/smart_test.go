package storagehealth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Homiakus/repoark/internal/config"
)

func TestSMARTScoreTriggersEvacuationSignal(t *testing.T) {
	cfg := config.DiskTelemetryConfig{Enabled: true, Device: "/dev/nvme0", SmartctlPath: "smartctl", MaxTemperatureC: 70, MaxPercentageUsed: 90, MaxMediaErrors: 0, RiskThreshold: 60}
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"model_name":"NVMe Test","serial_number":"S1","smart_status":{"passed":true},"nvme_smart_health_information_log":{"critical_warning":1,"temperature":72,"percentage_used":95,"media_errors":2,"num_err_log_entries":3}}`), nil
	}
	r := probeDiskWithRunner(context.Background(), cfg, run)
	if r.Health != Unhealthy || r.RiskScore < 60 {
		t.Fatalf("report=%+v", r)
	}
	if r.Model != "NVMe Test" || r.Serial != "S1" {
		t.Fatalf("identity=%+v", r)
	}
}

func TestDiskTelemetryFallbackToNVMe(t *testing.T) {
	cfg := config.DiskTelemetryConfig{Enabled: true, Device: "/dev/nvme0", SmartctlPath: "smartctl", NVMePath: "nvme", MaxTemperatureC: 80, MaxPercentageUsed: 95, MaxMediaErrors: 1, RiskThreshold: 60}
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if strings.Contains(name, "smartctl") {
			return nil, errors.New("missing")
		}
		return []byte(`{"critical_warning":0,"temperature":40,"percentage_used":12,"media_errors":0,"num_err_log_entries":0}`), nil
	}
	r := probeDiskWithRunner(context.Background(), cfg, run)
	if r.Health != Healthy || r.Tool != "nvme" {
		t.Fatalf("report=%+v", r)
	}
}
