package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NormalizePath canonicalizes an existing or prospective local path before it
// is persisted or compared. Symlinks are resolved only for existing ancestors.
func NormalizePath(value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("path must be absolute")
	}
	path := filepath.Clean(value)
	for existing := path; ; existing = filepath.Dir(existing) {
		if resolved, err := filepath.EvalSymlinks(existing); err == nil {
			relative, relativeErr := filepath.Rel(existing, path)
			if relativeErr == nil && relative != "." {
				return filepath.Join(resolved, relative), nil
			}
			return resolved, nil
		}
		if existing == filepath.Dir(existing) {
			break
		}
	}
	return path, nil
}

func PathsOverlap(first, second string) bool {
	first, err1 := NormalizePath(first)
	second, err2 := NormalizePath(second)
	if err1 != nil || err2 != nil {
		return false
	}
	return first == second || strings.HasPrefix(second, first+string(filepath.Separator)) || strings.HasPrefix(first, second+string(filepath.Separator))
}

type FilesystemSemantics struct {
	CaseSensitive        bool
	UnicodeNormalization string // "none", "nfc", "nfd", or "unknown"
}

func ProbeWritableEmpty(path string) error {
	normalized, err := NormalizePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(normalized, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	entries, err := os.ReadDir(normalized)
	if err != nil {
		return fmt.Errorf("read directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("directory must be empty")
	}
	probe, err := os.CreateTemp(normalized, ".melotrove-write-probe-")
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove write probe: %w", err)
	}
	return nil
}

// ProbeFilesystemSemantics determines case sensitivity and Unicode normalization
// behavior by creating temporary probe files. All probes are cleaned up on both
// success and error paths.
func ProbeFilesystemSemantics(path string) (FilesystemSemantics, error) {
	normalized, err := NormalizePath(path)
	if err != nil {
		return FilesystemSemantics{}, err
	}
	if err := os.MkdirAll(normalized, 0o755); err != nil {
		return FilesystemSemantics{}, fmt.Errorf("create directory: %w", err)
	}

	var probeFiles []string
	defer func() {
		for _, probe := range probeFiles {
			_ = os.Remove(probe)
		}
	}()

	// Case sensitivity probe
	lowerProbe := filepath.Join(normalized, ".melotrove-case-probe-lower")
	upperProbe := filepath.Join(normalized, ".melotrove-case-probe-LOWER")
	probeFiles = append(probeFiles, lowerProbe, upperProbe)

	if err := os.WriteFile(lowerProbe, []byte("lower"), 0o644); err != nil {
		return FilesystemSemantics{}, fmt.Errorf("write case probe: %w", err)
	}
	var caseSensitive bool
	if _, err := os.Stat(upperProbe); os.IsNotExist(err) {
		caseSensitive = true
	} else if err != nil {
		return FilesystemSemantics{}, fmt.Errorf("case probe stat: %w", err)
	}

	// Unicode normalization probe: test NFC vs NFD representation of "é"
	// NFC: single codepoint U+00E9
	// NFD: base 'e' U+0065 + combining accent U+0301
	nfcProbe := filepath.Join(normalized, ".melotrove-unicode-\u00e9")
	nfdProbe := filepath.Join(normalized, ".melotrove-unicode-e\u0301")
	probeFiles = append(probeFiles, nfcProbe, nfdProbe)

	if err := os.WriteFile(nfcProbe, []byte("nfc"), 0o644); err != nil {
		return FilesystemSemantics{}, fmt.Errorf("write unicode probe: %w", err)
	}

	unicodeNorm := "none"
	if nfdContent, err := os.ReadFile(nfdProbe); err == nil && string(nfdContent) == "nfc" {
		// NFD path read the NFC file content → filesystem normalizes to NFD
		unicodeNorm = "nfd"
	} else if _, err := os.Stat(nfdProbe); err == nil {
		// Both files exist separately → no normalization or NFC
		// Distinguish by checking if reading nfcProbe with NFD name works
		if content, readErr := os.ReadFile(nfcProbe); readErr == nil && string(content) == "nfc" {
			// Could also check if nfdProbe exists separately
			if _, statErr := os.Lstat(nfdProbe); os.IsNotExist(statErr) {
				unicodeNorm = "nfc"
			} else {
				unicodeNorm = "none"
			}
		}
	} else if os.IsNotExist(err) {
		// NFD path does not exist → filesystem uses NFC
		unicodeNorm = "nfc"
	}

	return FilesystemSemantics{CaseSensitive: caseSensitive, UnicodeNormalization: unicodeNorm}, nil
}
