// Package matchingpolicy provides a shared version gate for persisted matching
// plans, evaluations, and decisions.
package matchingpolicy

const CurrentVersion = "matching-rapidfuzz-v1"

func Supported(version string) bool {
	return version == CurrentVersion
}
