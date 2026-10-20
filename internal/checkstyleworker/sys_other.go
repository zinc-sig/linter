//go:build !linux

package checkstyleworker

import (
	"os"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr { return nil }

func killGroup(p *os.Process) { _ = p.Kill() }
