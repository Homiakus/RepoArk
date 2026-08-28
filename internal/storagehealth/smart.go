package storagehealth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
)

// DiskReport is deliberately explainable. RepoArk does not claim predictive
// failure certainty; RiskScore only aggregates observable SMART/NVMe signals
// and is used as a placement/evacuation hint.
type DiskReport struct {
	Device          string    `json:"device,omitempty"`
	Tool            string    `json:"tool,omitempty"`
	Model           string    `json:"model,omitempty"`
	Serial          string    `json:"serial,omitempty"`
	SMARTPassed     *bool     `json:"smart_passed,omitempty"`
	CriticalWarning int64     `json:"critical_warning,omitempty"`
	TemperatureC    float64   `json:"temperature_c,omitempty"`
	PercentageUsed  float64   `json:"percentage_used,omitempty"`
	MediaErrors     int64     `json:"media_errors,omitempty"`
	ErrorLogEntries int64     `json:"error_log_entries,omitempty"`
	RiskScore       int       `json:"risk_score"`
	Health          string    `json:"health"`
	Reasons         []string  `json:"reasons,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
	Error           string    `json:"error,omitempty"`
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ProbeDisk tries smartctl JSON first because it can report ATA/SATA and NVMe
// devices through one interface. For explicitly NVMe devices it falls back to
// nvme-cli JSON if smartctl is unavailable or returns unusable data.
func ProbeDisk(ctx context.Context, cfg config.DiskTelemetryConfig) DiskReport {
	return probeDiskWithRunner(ctx, cfg, runCommand)
}

func probeDiskWithRunner(ctx context.Context, cfg config.DiskTelemetryConfig, run commandRunner) DiskReport {
	r := DiskReport{Device: strings.TrimSpace(cfg.Device), Health: Healthy, CheckedAt: time.Now().UTC()}
	if !cfg.Enabled || r.Device == "" {
		return r
	}
	if cfg.SmartctlPath == "" {
		cfg.SmartctlPath = "smartctl"
	}
	if cfg.NVMePath == "" {
		cfg.NVMePath = "nvme"
	}
	if b, err := run(ctx, cfg.SmartctlPath, "-j", "-a", r.Device); err == nil {
		if err := parseSMARTJSON(b, &r); err == nil {
			r.Tool = "smartctl"
			scoreDisk(&r, cfg)
			return r
		}
	}
	b, err := run(ctx, cfg.NVMePath, "smart-log", r.Device, "-o", "json")
	if err != nil {
		r.Health = Degraded
		r.Error = err.Error()
		r.Reasons = append(r.Reasons, "disk telemetry unavailable")
		return r
	}
	if err := parseNVMeJSON(b, &r); err != nil {
		r.Health = Degraded
		r.Error = err.Error()
		r.Reasons = append(r.Reasons, "disk telemetry JSON invalid")
		return r
	}
	r.Tool = "nvme"
	scoreDisk(&r, cfg)
	return r
}

func parseSMARTJSON(b []byte, r *DiskReport) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if v := stringAt(m, "model_name"); v != "" {
		r.Model = v
	}
	if v := stringAt(m, "serial_number"); v != "" {
		r.Serial = v
	}
	if sm := mapAt(m, "smart_status"); sm != nil {
		if v, ok := sm["passed"].(bool); ok {
			r.SMARTPassed = &v
		}
	}
	if t := mapAt(m, "temperature"); t != nil {
		r.TemperatureC = numberAt(t, "current")
	}
	if nv := mapAt(m, "nvme_smart_health_information_log"); nv != nil {
		applyNVMeMap(nv, r)
	}
	// smartctl also exposes ATA attributes. We only consume normalized values
	// when present instead of hard-coding vendor-specific attribute IDs.
	if p := mapAt(m, "power_cycle_count"); p != nil {
		_ = p
	}
	if r.Model == "" && r.Serial == "" && r.SMARTPassed == nil && r.TemperatureC == 0 && r.CriticalWarning == 0 && r.MediaErrors == 0 && r.PercentageUsed == 0 {
		return errors.New("SMART JSON contained no recognized health fields")
	}
	return nil
}

func parseNVMeJSON(b []byte, r *DiskReport) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	applyNVMeMap(m, r)
	if r.CriticalWarning == 0 && r.TemperatureC == 0 && r.PercentageUsed == 0 && r.MediaErrors == 0 && r.ErrorLogEntries == 0 {
		// All zero is a valid healthy NVMe report, so only reject a completely
		// empty object.
		if len(m) == 0 {
			return errors.New("empty NVMe SMART JSON")
		}
	}
	return nil
}

func applyNVMeMap(m map[string]any, r *DiskReport) {
	r.CriticalWarning = int64Number(m, "critical_warning")
	r.TemperatureC = numberAt(m, "temperature")
	r.PercentageUsed = numberAt(m, "percentage_used")
	r.MediaErrors = int64Number(m, "media_errors")
	r.ErrorLogEntries = int64Number(m, "num_err_log_entries")
}

func scoreDisk(r *DiskReport, cfg config.DiskTelemetryConfig) {
	score := 0
	if r.SMARTPassed != nil && !*r.SMARTPassed {
		score = 100
		r.Reasons = append(r.Reasons, "SMART overall-health failed")
	}
	if r.CriticalWarning != 0 {
		score += 50
		r.Reasons = append(r.Reasons, fmt.Sprintf("NVMe critical_warning=%d", r.CriticalWarning))
	}
	if cfg.MaxMediaErrors >= 0 && r.MediaErrors > cfg.MaxMediaErrors {
		score += 35
		r.Reasons = append(r.Reasons, fmt.Sprintf("media_errors=%d > %d", r.MediaErrors, cfg.MaxMediaErrors))
	}
	if cfg.MaxPercentageUsed > 0 && r.PercentageUsed >= cfg.MaxPercentageUsed {
		score += 30
		r.Reasons = append(r.Reasons, fmt.Sprintf("percentage_used=%.0f%%", r.PercentageUsed))
	}
	if cfg.MaxTemperatureC > 0 && r.TemperatureC >= cfg.MaxTemperatureC {
		score += 20
		r.Reasons = append(r.Reasons, fmt.Sprintf("temperature=%.1fC", r.TemperatureC))
	}
	if r.ErrorLogEntries > 0 {
		// Error-log entries alone are weak evidence; keep the weight low.
		score += 5
		r.Reasons = append(r.Reasons, fmt.Sprintf("error_log_entries=%d", r.ErrorLogEntries))
	}
	if score > 100 {
		score = 100
	}
	r.RiskScore = score
	threshold := cfg.RiskThreshold
	if threshold <= 0 {
		threshold = 60
	}
	switch {
	case score >= threshold:
		r.Health = Unhealthy
	case score > 0:
		r.Health = Degraded
	default:
		r.Health = Healthy
	}
}

func mapAt(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}
func stringAt(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
func numberAt(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(x, " C")), 64)
		return f
	}
	return 0
}
func int64Number(m map[string]any, key string) int64 { return int64(numberAt(m, key)) }
