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
	firstPrefix, secondPrefix := first+string(filepath.Separator), second+string(filepath.Separator)
	if filepath.Dir(first) == first {
		firstPrefix = first
	}
	if filepath.Dir(second) == second {
		secondPrefix = second
	}
	if first == second || strings.HasPrefix(second, firstPrefix) || strings.HasPrefix(first, secondPrefix) {
		return true
	}
	if firstInfo, err := os.Stat(first); err == nil {
		if secondInfo, err := os.Stat(second); err == nil && os.SameFile(firstInfo, secondInfo) {
			return true
		}
	}
	if !strings.EqualFold(first, second) &&
		!strings.HasPrefix(strings.ToLower(second), strings.ToLower(firstPrefix)) &&
		!strings.HasPrefix(strings.ToLower(first), strings.ToLower(secondPrefix)) {
		return false
	}
	for ancestor := first; ; ancestor = filepath.Dir(ancestor) {
		if info, err := os.Stat(ancestor); err == nil && info.IsDir() {
			if semantics, err := ProbeFilesystemSemantics(ancestor); err == nil {
				return !semantics.CaseSensitive
			}
		}
		if ancestor == filepath.Dir(ancestor) {
			return false
		}
	}
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

func ProbeWritable(path string) error {
	normalized, err := NormalizePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(normalized, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
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
	probeRoot, err := os.MkdirTemp(normalized, ".melotrove-semantics-")
	if err != nil {
		return FilesystemSemantics{}, fmt.Errorf("create semantics probe: %w", err)
	}
	defer func() { _ = os.RemoveAll(probeRoot) }()

	var probeFiles []string
	defer func() {
		for _, probe := range probeFiles {
			_ = os.Remove(probe)
		}
	}()

	// Case sensitivity probe
	lowerProbe := filepath.Join(probeRoot, ".melotrove-case-probe-lower")
	upperProbe := filepath.Join(probeRoot, ".melotrove-case-probe-LOWER")
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

	unicodePrefix := ".melotrove-unicode-"
	nfcName := unicodePrefix + "\u00e9"
	nfdName := unicodePrefix + "e\u0301"
	nfcProbe := filepath.Join(probeRoot, nfcName)
	nfdProbe := filepath.Join(probeRoot, nfdName)
	probeFiles = append(probeFiles, nfcProbe, nfdProbe)

	if err := os.WriteFile(nfcProbe, []byte("nfc"), 0o644); err != nil {
		return FilesystemSemantics{}, fmt.Errorf("write unicode probe: %w", err)
	}
	nfcInfo, err := os.Stat(nfcProbe)
	if err != nil {
		return FilesystemSemantics{}, fmt.Errorf("stat unicode probe: %w", err)
	}
	nfdInfo, err := os.Stat(nfdProbe)
	aliases := false
	if err == nil {
		aliases = os.SameFile(nfcInfo, nfdInfo)
	} else if !os.IsNotExist(err) {
		return FilesystemSemantics{}, fmt.Errorf("stat unicode probe: %w", err)
	}
	entries, err := os.ReadDir(probeRoot)
	if err != nil {
		return FilesystemSemantics{}, fmt.Errorf("read unicode probe: %w", err)
	}
	storedName := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), unicodePrefix) {
			storedName = strings.TrimPrefix(entry.Name(), unicodePrefix)
			break
		}
	}
	return FilesystemSemantics{CaseSensitive: caseSensitive, UnicodeNormalization: classifyUnicodeNormalization(aliases, storedName)}, nil
}

func classifyUnicodeNormalization(aliases bool, storedName string) string {
	if !aliases {
		return "none"
	}
	switch storedName {
	case "\u00e9":
		return "nfc"
	case "e\u0301":
		return "nfd"
	default:
		return "unknown"
	}
}
