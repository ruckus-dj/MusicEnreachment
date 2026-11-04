//go:build !darwin

package testpostgres

func probeCloneFileCopy(string) (bool, string, error) {
	return false, "file cloning is enabled only for an owned disposable macOS cluster", nil
}
