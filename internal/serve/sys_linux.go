package serve

import (
	"fmt"
	"syscall"
)

// setNonDumpable clears the process's dumpable flag, which makes its
// /proc entries root-owned and refuses ptrace by the same user.
func setNonDumpable() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); errno != 0 {
		return fmt.Errorf("prctl(PR_SET_DUMPABLE): %w", errno)
	}
	return nil
}
