package sourcefs

import (
	"errors"
	"testing"
)

func TestValidateRootPathSupportWindowsUNCForms(t *testing.T) {
	for _, path := range []string{
		`\\server\share`, `//server/share`, `\\server/share`, `//server\share`,
		`\\?\UNC\server\share`, `\\?\unc\server\share`, `//?/UNC/server/share`,
		`\\?/uNc/server\share`, `\??\UNC\server\share`, `/??/unc/server/share`,
		`//??/UNC/server/share`,
		`\\.\UNC\server\share`, `\\.\unc\server\share`, `//./UNC/server/share`,
		`\\./uNc/server\share`,
	} {
		t.Run(path, func(t *testing.T) {
			if err := validateRootPathSupport(path, true); !errors.Is(err, ErrUnsupportedNetworkRoot) {
				t.Fatalf("validateRootPathSupport(%q) = %v, want unsupported network root", path, err)
			}
		})
	}
}

func TestValidateRootPathSupportLeavesLocalAndDevicePathsToExistingValidation(t *testing.T) {
	for _, path := range []string{`C:\music`, `\\?\C:\music`, `\\.\PhysicalDrive0`, `\??\C:\music`} {
		if err := validateRootPathSupport(path, true); err != nil {
			t.Errorf("validateRootPathSupport(%q) = %v, want nil", path, err)
		}
	}
	if err := validateRootPathSupport(`\\server\share`, false); err != nil {
		t.Fatalf("non-Windows path was classified as a network root: %v", err)
	}
}
