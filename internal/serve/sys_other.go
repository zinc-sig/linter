//go:build !linux

package serve

func setNonDumpable() error { return nil }
