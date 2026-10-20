//go:build unix

package main

import (
	"os"
	"syscall"
)

func freeze() { _ = syscall.Kill(os.Getpid(), syscall.SIGSTOP) }
