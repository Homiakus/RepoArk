//go:build windows

package audit

import (
	"os"

	"golang.org/x/sys/windows"
)

type auditFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func lockAuditFile(path string) (*auditFileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	lock := &auditFileLock{file: f}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &lock.overlapped); err != nil {
		_ = f.Close()
		return nil, err
	}
	return lock, nil
}

func (l *auditFileLock) unlock() error {
	unlockErr := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
