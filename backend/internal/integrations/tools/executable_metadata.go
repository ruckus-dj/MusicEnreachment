package tools

import "strings"

// VerifiedExecutableVersion returns the verified banner under the executable
// name expected for this platform. Installation metadata records those names
// literally, including .exe on Windows.
func VerifiedExecutableVersion(versions map[string]string, kind PackageKind, logicalName, goos string) (string, bool) {
	var expected string
	switch kind {
	case PackageFFmpeg:
		if logicalName != "ffmpeg" && logicalName != "ffprobe" {
			return "", false
		}
		expected = logicalName
	case PackageFPCalc:
		if logicalName != "fpcalc" {
			return "", false
		}
		expected = logicalName
	default:
		return "", false
	}

	if goos == "windows" {
		expected += ".exe"
	}
	banner, ok := versions[expected]
	if !ok || strings.TrimSpace(banner) == "" {
		return "", false
	}
	return strings.TrimSpace(banner), true
}
