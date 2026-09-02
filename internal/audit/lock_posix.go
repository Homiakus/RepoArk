//go:build !windows

package audit

import (
	"os"

	"golang.org/x/sys/unix"
)

type auditFileLock struct {
	file *os.File
}

func lockAuditFile(path string) (*auditFileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &auditFileLock{file: f}, nil
}

func (l *auditFileLock) unlock() error {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
