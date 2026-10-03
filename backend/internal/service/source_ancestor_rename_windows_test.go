//go:build windows

package service_test

import (
	"errors"

	"golang.org/x/sys/windows"
)

func sourceAnalysisAncestorRenameAccessDenied(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
