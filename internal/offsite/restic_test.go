package offsite

import (
	"context"
	"strings"
	"testing"

	"github.com/Homiakus/repoark/internal/config"
)

func TestConfigureObjectLockComplianceNeedsExplicitFlag(t *testing.T) {
	cfg := config.Default()
	cfg.Offsite.ObjectLock.Enabled = true
	cfg.Offsite.ObjectLock.Bucket = "example"
	cfg.Offsite.ObjectLock.ExpectedMode = "COMPLIANCE"
	cfg.Offsite.ObjectLock.MinRetentionDays = 30
	err := ConfigureObjectLockDefaultRetention(context.Background(), cfg, false)
	if err == nil || !strings.Contains(err.Error(), "--allow-compliance") {
		t.Fatalf("unexpected error: %v", err)
	}
}
