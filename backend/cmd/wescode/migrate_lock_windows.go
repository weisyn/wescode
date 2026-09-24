//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockExclusive attempts a non-blocking exclusive lock on byte range
// [0,1) of f. Returns (false, nil) on contention — LockFileEx with
// LOCKFILE_FAIL_IMMEDIATELY reports ERROR_LOCK_VIOLATION rather than
// blocking. Any other error is fail-closed (migration refused, data left
// in the legacy tree) because guessing wrong here clobbers user data.
func tryLockExclusive(f *os.File) (bool, error) {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
