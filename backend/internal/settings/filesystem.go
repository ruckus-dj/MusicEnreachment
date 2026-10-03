package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type probeDirectoryLock struct {
	mu   sync.Mutex
	refs int
}

var probeDirectoryLocks = struct {
	sync.Mutex
	locks map[string]*probeDirectoryLock
}{locks: make(map[string]*probeDirectoryLock)}

var probeFilesystemSemanticsHook = struct {
	sync.RWMutex
	fn func()
}{}

var probeDirectoryRegistrationHook = struct {
	sync.RWMutex
	fn func(string)
}{}

// SetProbeDirectoryRegistrationHook installs a test seam called after a probe
// has registered for its directory guard and before it attempts to acquire it.
func SetProbeDirectoryRegistrationHook(fn func(string)) func() {
	probeDirectoryRegistrationHook.Lock()
	previous := probeDirectoryRegistrationHook.fn
	probeDirectoryRegistrationHook.fn = fn
	probeDirectoryRegistrationHook.Unlock()
	return func() {
		probeDirectoryRegistrationHook.Lock()
		probeDirectoryRegistrationHook.fn = previous
		probeDirectoryRegistrationHook.Unlock()
	}
}

// SetProbeFilesystemSemanticsHook installs a synchronization hook for tests that
// need to observe an in-progress semantics probe. The returned function restores
// the previous hook. Production callers should not use this test seam.
func SetProbeFilesystemSemanticsHook(fn func()) func() {
	probeFilesystemSemanticsHook.Lock()
	previous := probeFilesystemSemanticsHook.fn
	probeFilesystemSemanticsHook.fn = fn
	probeFilesystemSemanticsHook.Unlock()
	return func() {
		probeFilesystemSemanticsHook.Lock()
		probeFilesystemSemanticsHook.fn = previous
		probeFilesystemSemanticsHook.Unlock()
	}
}

// WithProbeDirectory serializes filesystem probes for the normalized directory.
// Callers should use the complete probe helpers below rather than nesting this
// guard around them.
func WithProbeDirectory(path string, fn func(string) error) error {
	return withProbeDirectory(path, false, fn)
}

// withProbeDirectory coordinates directory creation separately from the
// per-directory probe lock. The registry lock is held only until a prospective
// directory has been materialized and its actual filesystem identity registered;
// the potentially long probe runs under its directory lock alone.
func withProbeDirectory(path string, create bool, fn func(string) error) error {
	normalized, err := NormalizePath(path)
	if err != nil {
		return err
	}
	probeDirectoryLocks.Lock()
	if create {
		if err := os.MkdirAll(normalized, 0o755); err != nil {
			probeDirectoryLocks.Unlock()
			return fmt.Errorf("create directory: %w", err)
		}
	}
	lock := probeDirectoryLocks.locks[normalized]
	if info, statErr := os.Stat(normalized); statErr == nil && info.IsDir() {
		// Stat active keys afresh: aliases of a newly materialized directory
		// must share the existing probe lock before this registry lock is released.
		for key, candidate := range probeDirectoryLocks.locks {
			candidateInfo, candidateErr := os.Stat(key)
			if candidateErr == nil && candidateInfo.IsDir() && os.SameFile(info, candidateInfo) {
				lock = candidate
				break
			}
		}
	}
	if lock == nil {
		lock = &probeDirectoryLock{}
	}
	probeDirectoryLocks.locks[normalized] = lock
	lock.refs++
	probeDirectoryLocks.Unlock()
	probeDirectoryRegistrationHook.RLock()
	hook := probeDirectoryRegistrationHook.fn
	probeDirectoryRegistrationHook.RUnlock()
	if hook != nil {
		hook(normalized)
	}

	lock.mu.Lock()
	defer func() {
		lock.mu.Unlock()
		probeDirectoryLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			for key, candidate := range probeDirectoryLocks.locks {
				if candidate == lock {
					delete(probeDirectoryLocks.locks, key)
				}
			}
		}
		probeDirectoryLocks.Unlock()
	}()
	return fn(normalized)
}

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
	return withProbeDirectory(path, true, probeWritableEmpty)
}

func probeWritableEmpty(normalized string) error {
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
		return errors.Join(err, os.Remove(name))
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove write probe: %w", err)
	}
	return nil
}

func ProbeWritable(path string) error {
	return withProbeDirectory(path, true, probeWritable)
}

func probeWritable(normalized string) error {
	probe, err := os.CreateTemp(normalized, ".melotrove-write-probe-")
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		return errors.Join(err, os.Remove(name))
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
	var semantics FilesystemSemantics
	err := withProbeDirectory(path, true, func(normalized string) error {
		var err error
		semantics, err = probeFilesystemSemantics(normalized)
		return err
	})
	return semantics, err
}

// ProbeOutputDirectory holds the directory guard across both the writable/empty
// check and semantics probes, including all temporary-file cleanup.
func ProbeOutputDirectory(path string, requireEmpty bool) (FilesystemSemantics, error) {
	var semantics FilesystemSemantics
	err := withProbeDirectory(path, true, func(normalized string) error {
		var err error
		if requireEmpty {
			err = probeWritableEmpty(normalized)
		} else {
			err = probeWritable(normalized)
		}
		if err != nil {
			return err
		}
		semantics, err = probeFilesystemSemantics(normalized)
		return err
	})
	return semantics, err
}

func probeFilesystemSemantics(normalized string) (semantics FilesystemSemantics, resultErr error) {
	probeRoot, err := os.MkdirTemp(normalized, ".melotrove-semantics-")
	if err != nil {
		return FilesystemSemantics{}, fmt.Errorf("create semantics probe: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(probeRoot); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove semantics probe: %w", err))
		}
	}()

	// Case sensitivity probe
	lowerProbe := filepath.Join(probeRoot, ".melotrove-case-probe-lower")
	upperProbe := filepath.Join(probeRoot, ".melotrove-case-probe-LOWER")
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
	probeFilesystemSemanticsHook.RLock()
	hook := probeFilesystemSemanticsHook.fn
	probeFilesystemSemanticsHook.RUnlock()
	if hook != nil {
		hook()
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
