//go:build !windows

package storagehealth

import "syscall"

func diskSpace(path string) (uint64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	b := uint64(st.Bsize)
	return uint64(st.Blocks) * b, uint64(st.Bavail) * b, nil
}
