//go:build !windows

package daemon

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive, non-blocking flock. flock locks belong to
// the open file description, so two opens conflict even inside one process —
// which is what lets a test race two starts in goroutines.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}
