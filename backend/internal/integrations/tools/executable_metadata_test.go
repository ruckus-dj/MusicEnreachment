package tools

import "testing"

func TestVerifiedExecutableVersion(t *testing.T) {
	tests := []struct {
		name     string
		versions map[string]string
		kind     PackageKind
		logical  string
		goos     string
		want     string
		ok       bool
	}{
		{name: "linux actual key", versions: map[string]string{"fpcalc": " fpcalc version 1.2.3\n"}, kind: PackageFPCalc, logical: "fpcalc", goos: "linux", want: "fpcalc version 1.2.3", ok: true},
		{name: "windows actual key", versions: map[string]string{"ffprobe.exe": "ffprobe version 6.0"}, kind: PackageFFmpeg, logical: "ffprobe", goos: "windows", want: "ffprobe version 6.0", ok: true},
		{name: "does not fallback to bare windows key", versions: map[string]string{"fpcalc": "fpcalc version 1.2.3"}, kind: PackageFPCalc, logical: "fpcalc", goos: "windows"},
		{name: "missing executable", versions: map[string]string{"ffmpeg": "ffmpeg version 6.0"}, kind: PackageFFmpeg, logical: "ffprobe", goos: "linux"},
		{name: "empty banner", versions: map[string]string{"fpcalc.exe": " \n"}, kind: PackageFPCalc, logical: "fpcalc", goos: "windows"},
		{name: "wrong executable for package", versions: map[string]string{"ffmpeg.exe": "ffmpeg version 6.0"}, kind: PackageFFmpeg, logical: "ffprobe", goos: "windows"},
		{name: "arbitrary extension is not accepted", versions: map[string]string{"fpcalc.dll": "fpcalc version 1.2.3"}, kind: PackageFPCalc, logical: "fpcalc", goos: "linux"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := VerifiedExecutableVersion(test.versions, test.kind, test.logical, test.goos)
			if got != test.want || ok != test.ok {
				t.Fatalf("VerifiedExecutableVersion() = (%q, %t), want (%q, %t)", got, ok, test.want, test.ok)
			}
		})
	}
}
