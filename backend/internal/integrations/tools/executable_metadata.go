package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// VerificationResult is the canonical identity produced by one fresh
// verification of every executable in a package. Version is an opaque value;
// for FFmpeg it includes both ffmpeg and ffprobe tokens.
type VerificationResult struct {
	Version  string
	Evidence map[string]string
}

// MarshalEvidence serializes the untouched executable output from this fresh
// verification for the private persistence record.
func (result VerificationResult) MarshalEvidence() (json.RawMessage, error) {
	return json.Marshal(result.Evidence)
}

// CanonicalPackageVersion extracts only the version token from fresh executable
// output. Release identities are locators and deliberately do not participate.
func CanonicalPackageVersion(evidence map[string]string, kind PackageKind, goos string) (string, error) {
	extract := func(name string) (string, error) {
		output, ok := evidence[executableName(name, goos)]
		if !ok {
			return "", fmt.Errorf("verification output is missing for %s", name)
		}
		firstLine, _, _ := strings.Cut(output, "\n")
		fields := strings.Fields(firstLine)
		if len(fields) >= 3 && strings.EqualFold(fields[0], name) && strings.EqualFold(fields[1], "version") {
			return fields[2], nil
		}
		return "", fmt.Errorf("verification output for %s has no version token", name)
	}
	if kind == PackageFPCalc {
		return extract("fpcalc")
	}
	if kind != PackageFFmpeg {
		return "", fmt.Errorf("unsupported package %q", kind)
	}
	ffmpeg, err := extract("ffmpeg")
	if err != nil {
		return "", err
	}
	ffprobe, err := extract("ffprobe")
	if err != nil {
		return "", err
	}
	return "ffmpeg " + ffmpeg + " + ffprobe " + ffprobe, nil
}

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
