//go:build !windows

package testpostgres

func ensureCurrentUserDefaultDACL() error { return nil }
