package tools

import "testing"

func TestCanonicalPackageVersionKeepsBothFFmpegExecutableTokens(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		suffix := ""
		if goos == "windows" {
			suffix = ".exe"
		}
		got, err := CanonicalPackageVersion(map[string]string{
			"ffmpeg" + suffix:  "ffmpeg version 8.1.2 Copyright",
			"ffprobe" + suffix: "ffprobe version 7.4.3 Copyright",
		}, PackageFFmpeg, goos)
		if err != nil {
			t.Fatalf("%s: canonical version: %v", goos, err)
		}
		if got != "ffmpeg 8.1.2 + ffprobe 7.4.3" {
			t.Fatalf("%s: canonical version = %q", goos, got)
		}
	}
}

func TestCanonicalPackageVersionKeepsFPCalcToken(t *testing.T) {
	got, err := CanonicalPackageVersion(map[string]string{
		"fpcalc": "fpcalc version 1.6.1",
	}, PackageFPCalc, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.6.1" {
		t.Fatalf("canonical version = %q", got)
	}
}
