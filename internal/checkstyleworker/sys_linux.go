package checkstyleworker

import (
	"os"
	"syscall"
)

// sysProcAttr puts the JVM in its own process group and kills it when the
// server dies.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func killGroup(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
}
