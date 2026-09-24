//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// tryLockExclusive attempts a non-blocking exclusive lock on f.
// Returns (false, nil) when another open file description holds the lock —
// that is contention, not an error. flock(2) keys on the open file
// description, not the process, so two os.OpenFile calls inside ONE process
// (the concurrency test) serialize exactly like two processes do.
func tryLockExclusive(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
