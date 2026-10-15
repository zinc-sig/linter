//go:build !unix

package linter

import "os/exec"

func setProcessGroup(*exec.Cmd) {}
