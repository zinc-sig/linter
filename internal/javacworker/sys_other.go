//go:build !linux

package javacworker

import (
	"os"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr { return nil }

func killGroup(p *os.Process) { _ = p.Kill() }
