package audit

import (
	"errors"
	"strings"
	"sync"
)

var mu sync.Mutex

// acquireLedgerGuard serializes audit access both within this process and,
// through an OS advisory lock, with other RepoArk processes using the same
// ledger path. Callers must release the returned guard.
func acquireLedgerGuard(path string) (func(), error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("audit path is empty")
	}
	mu.Lock()
	lock, err := lockAuditFile(path + ".lock")
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() {
		_ = lock.unlock()
		mu.Unlock()
	}, nil
}
