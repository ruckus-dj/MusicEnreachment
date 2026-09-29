package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBtbNCatalogUsesLatestAssetsAndStableVersionIdentities(t *testing.T) {
	server, adapter := newBtbNTestServer(t, recordedBtbNPayload)
	defer server.Close()
	for _, test := range []struct {
		platform Platform
		want9    string
	}{
		{Platform{"linux", "amd64"}, "ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz"},
		{Platform{"linux", "arm64"}, "ffmpeg-n9.0-latest-linuxarm64-gpl-9.0.tar.xz"},
		{Platform{"windows", "amd64"}, "ffmpeg-n9.0-latest-win64-gpl-9.0.zip"},
	} {
		releases, err := adapter.List(context.Background(), test.platform)
		if err != nil {
			t.Fatal(err)
		}
		if len(releases) != 2 || releases[0].Identity != "9.0" || releases[1].Identity != "8.1" {
			t.Fatalf("releases for %v = %#v; want 9.0 then 8.1", test.platform, releases)
		}
		if len(releases[0].Artifacts) != 1 || releases[0].Artifacts[0].Name != test.want9 {
			t.Fatalf("selected asset for %v = %#v; want %q", test.platform, releases[0].Artifacts, test.want9)
		}
		for _, release := range releases {
			for _, artifact := range release.Artifacts {
				if strings.Contains(artifact.Name, "master") || strings.Contains(artifact.Name, "shared") || strings.Contains(artifact.Name, "lgpl") {
					t.Errorf("excluded asset offered: %q", artifact.Name)
				}
			}
		}
	}
}

func TestBtbNCatalogReturnsEmptyForLatestWithoutVersionedAssets(t *testing.T) {
	server, adapter := newBtbNTestServer(t, `[{"tag_name":"latest","assets":[{"name":"ffmpeg-master-latest-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/master"},{"name":"ffmpeg-n9.0-latest-linux64-gpl-shared-9.0.tar.xz","browser_download_url":"https://github.com/BtbN/shared"}]}]`)
	defer server.Close()
	releases, err := adapter.List(context.Background(), Platform{"linux", "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if releases == nil || len(releases) != 0 {
		t.Fatalf("catalog = %#v; want explicit empty list", releases)
	}
}

func TestBtbNIdentityRoundTripsResolveDownloadAndChecksum(t *testing.T) {
	archive := []byte("fixture archive")
	checksum := strings.Repeat("c", 64)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			_, _ = w.Write([]byte(recordedBtbNPayload))
			return
		}
		if strings.Contains(r.URL.Path, "checksums") {
			_, _ = w.Write([]byte(checksum + "  ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz\n"))
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	adapter := btbNTestAdapter(server)
	catalog := NewCatalog(adapter)
	platform := Platform{"linux", "amd64"}

	release, err := catalog.Resolve(context.Background(), PackageFFmpeg, platform, "9.0")
	if err != nil || release.Identity != "9.0" || len(release.Artifacts) != 1 {
		t.Fatalf("resolve = %#v, %v", release, err)
	}
	artifactName := release.Artifacts[0].Name
	resolvedChecksum, err := catalog.Checksum(context.Background(), PackageFFmpeg, platform, "9.0", artifactName)
	if err != nil || resolvedChecksum != checksum {
		t.Fatalf("checksum = %q, %v", resolvedChecksum, err)
	}
	var destination bytes.Buffer
	downloaded, count, err := catalog.Download(context.Background(), PackageFFmpeg, platform, "9.0", artifactName, &destination, nil)
	if err != nil || downloaded.Name != artifactName || count != int64(len(archive)) || !bytes.Equal(destination.Bytes(), archive) {
		t.Fatalf("download = %#v, %d, %q, %v", downloaded, count, destination.Bytes(), err)
	}
}

const recordedBtbNPayload = `[{"tag_name":"latest","assets":[
 {"name":"ffmpeg-master-latest-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/master"},
 {"name":"ffmpeg-n9.0-latest-linux64-lgpl-9.0.tar.xz","browser_download_url":"https://github.com/BtbN/lgpl"},
 {"name":"ffmpeg-n9.0-latest-linux64-gpl-shared-9.0.tar.xz","browser_download_url":"https://github.com/BtbN/shared"},
 {"name":"ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz","browser_download_url":"https://github.com/BtbN/n9-linux64"},
 {"name":"ffmpeg-n9.0-latest-linuxarm64-gpl-9.0.tar.xz","browser_download_url":"https://github.com/BtbN/n9-linuxarm64"},
 {"name":"ffmpeg-n9.0-latest-win64-gpl-9.0.zip","browser_download_url":"https://github.com/BtbN/n9-win64"},
 {"name":"ffmpeg-n8.1-latest-linux64-gpl-8.1.tar.xz","browser_download_url":"https://github.com/BtbN/n8-linux64"},
 {"name":"ffmpeg-n8.1-latest-linuxarm64-gpl-8.1.tar.xz","browser_download_url":"https://github.com/BtbN/n8-linuxarm64"},
 {"name":"ffmpeg-n8.1-latest-win64-gpl-8.1.zip","browser_download_url":"https://github.com/BtbN/n8-win64"},
 {"name":"checksums.sha256","browser_download_url":"https://github.com/BtbN/checksums"}
]},{"tag_name":"latest","prerelease":true,"assets":[{"name":"ffmpeg-n10.0-latest-linux64-gpl-10.0.tar.xz","browser_download_url":"https://github.com/BtbN/prerelease"}]}]`

func newBtbNTestServer(t *testing.T, payload string) (*httptest.Server, *GitHubAdapter) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	return server, btbNTestAdapter(server)
}

func btbNTestAdapter(server *httptest.Server) *GitHubAdapter {
	client := server.Client()
	adapter := NewBtbNAdapter(client, "https://api.github.com/releases")
	adapter.client.Transport = rewriteTransport{server: server, base: client.Transport}
	return adapter
}

func TestMacOSAdapterGroupsFFmpegPackageArtifacts(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<a href="/download/macos/arm64/1789931890_9.0.2/ffmpeg.zip">ffmpeg</a><a href="/download/macos/arm64/1789931890_9.0.2/ffmpeg.zip.sha256">sha256</a><a href="/download/macos/arm64/1789931890_9.0.2/ffprobe.zip">ffprobe</a><a href="/download/macos/arm64/1789931890_9.0.2/ffprobe.zip.sha256">sha256</a><a href="/download/macos/arm64/1790504560_N-126899-gd975849594/ffmpeg.zip">snapshot</a><a href="/download/macos/arm64/1789931890_9.0.2/ffmpeg.pkg">installer</a><a href="/download/macos/arm64/1789931890_9.0.2/ffmpeg.zip">duplicate</a>`))
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewMacOSAdapter(client, "https://ffmpeg.martin-riedl.de/releases")
	adapter.client = &http.Client{Transport: rewriteTransport{server: server, base: client.Transport}}

	releases, err := adapter.List(context.Background(), Platform{"darwin", "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Identity != "9.0.2" || len(releases[0].Artifacts) != 2 {
		t.Fatalf("macOS releases = %#v", releases)
	}
	for _, artifact := range releases[0].Artifacts {
		if artifact.ChecksumURL == "" {
			t.Errorf("artifact %q did not include its published checksum", artifact.Name)
		}
	}
}

func TestMacOSAdapterRequiresBothDownloadLinks(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<a href="/download/macos/arm64/1789931890_9.0.2/ffmpeg.zip">ffmpeg</a><a href="/download/macos/arm64/1789931890_9.0.2/ffprobe.zip.sha256">checksum only</a>`))
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewMacOSAdapter(client, "https://ffmpeg.martin-riedl.de/releases")
	adapter.client.Transport = rewriteTransport{server: server, base: client.Transport}

	releases, err := adapter.List(context.Background(), Platform{"darwin", "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatalf("incomplete package offered: %#v", releases)
	}
}

func TestAllowedHTTPSRejectsCredentialAndPortOverrides(t *testing.T) {
	for _, raw := range []string{"https://user@github.com/releases", "https://github.com:8443/releases", "http://github.com/releases"} {
		if err := allowedHTTPS(raw, "github.com"); err == nil {
			t.Errorf("allowed unsafe source URL %q", raw)
		}
	}
}

func TestChromaprintAdapterSelectsEachSupportedPlatform(t *testing.T) {
	names := []string{
		"chromaprint-fpcalc-1.5.1-linux-x86_64.tar.gz",
		"chromaprint-fpcalc-1.5.1-linux-arm64.tar.gz",
		"chromaprint-fpcalc-1.5.1-macos-x86_64.tar.gz",
		"chromaprint-fpcalc-1.5.1-macos-arm64.tar.gz",
		"chromaprint-fpcalc-1.5.1-windows-x86_64.zip",
	}
	assets := make([]githubAsset, 0, len(names))
	for _, name := range names {
		assets = append(assets, githubAsset{
			Name: name, URL: "https://github.com/acoustid/chromaprint/releases/download/v1.5.1/" + name,
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		})
	}
	body, err := json.Marshal([]githubRelease{{Tag: "v1.5.1", Assets: assets}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewChromaprintAdapter(client, "https://api.github.com/releases")
	adapter.client = &http.Client{Transport: rewriteTransport{server: server, base: client.Transport}}
	catalog := NewCatalog(adapter)
	for _, test := range []struct {
		platform Platform
		name     string
	}{
		{Platform{"linux", "amd64"}, names[0]},
		{Platform{"linux", "arm64"}, names[1]},
		{Platform{"darwin", "amd64"}, names[2]},
		{Platform{"darwin", "arm64"}, names[3]},
		{Platform{"windows", "amd64"}, names[4]},
	} {
		t.Run(test.platform.GOOS+"/"+test.platform.GOARCH, func(t *testing.T) {
			releases, err := catalog.List(context.Background(), PackageFPCalc, test.platform)
			if err != nil {
				t.Fatal(err)
			}
			if len(releases) != 1 || releases[0].Identity != "v1.5.1" || len(releases[0].Artifacts) != 1 ||
				releases[0].Artifacts[0].Name != test.name ||
				releases[0].Artifacts[0].ChecksumSHA256 != strings.Repeat("a", 64) {
				t.Fatalf("Chromaprint releases for %v = %#v", test.platform, releases)
			}
		})
	}
	if _, err := catalog.List(context.Background(), PackageFPCalc, Platform{"windows", "arm64"}); err == nil {
		t.Fatal("unsupported Chromaprint platform accepted")
	}
}

func TestCompareVersionsUsesNumericOrdering(t *testing.T) {
	if compareVersions("10.0.0", "9.9.9") <= 0 || compareVersions("v1.6.1", "v1.6.0") <= 0 {
		t.Fatal("release versions were not ordered numerically")
	}
}

func TestGitHubAdapterRejectsMalformedRateLimitedAndOversizedCatalogs(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "malformed", statusCode: http.StatusOK, body: `{`},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, body: `[]`},
		{name: "oversized", statusCode: http.StatusOK, body: strings.Repeat(" ", (4<<20)+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := server.Client()
			adapter := NewBtbNAdapter(client, "https://api.github.com/releases")
			adapter.client = &http.Client{Transport: rewriteTransport{server: server, base: client.Transport}}
			if _, err := adapter.List(context.Background(), Platform{"linux", "amd64"}); err == nil {
				t.Fatal("invalid catalog response was accepted")
			}
		})
	}
}

func TestCatalogDownloadResolvesArtifactAndReportsMeasuredProgress(t *testing.T) {
	archive := []byte("fixture archive")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			_, _ = w.Write([]byte(recordedBtbNPayload))
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewBtbNAdapter(client, "https://api.github.com/releases")
	adapter.client.Transport = rewriteTransport{server: server, base: client.Transport}
	catalog := NewCatalog(adapter)

	var destination bytes.Buffer
	var progress []int64
	artifact, count, err := catalog.Download(context.Background(), PackageFFmpeg, Platform{"linux", "amd64"}, "9.0", "ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz", &destination, func(completed int64) {
		progress = append(progress, completed)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.Bytes(), archive) || count != int64(len(archive)) || artifact.ChecksumURL == "" {
		t.Fatalf("download result = %q, %d, %#v", destination.Bytes(), count, artifact)
	}
	if len(progress) == 0 || progress[len(progress)-1] != int64(len(archive)) {
		t.Fatalf("progress events = %#v", progress)
	}
}

func TestCatalogDownloadRejectsRedirectOutsideSourceAllowlist(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			_, _ = w.Write([]byte(recordedBtbNPayload))
			return
		}
		http.Redirect(w, r, "https://evil.example/archive", http.StatusFound)
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewBtbNAdapter(client, "https://api.github.com/releases")
	adapter.client.Transport = rewriteTransport{server: server, base: client.Transport}
	catalog := NewCatalog(adapter)

	if _, _, err := catalog.Download(context.Background(), PackageFFmpeg, Platform{"linux", "amd64"}, "9.0", "ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz", io.Discard, nil); err == nil {
		t.Fatal("redirect to an unallowlisted host was followed")
	}
}

type rewriteTransport struct {
	server *httptest.Server
	base   http.RoundTripper
}

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "https"
	r.URL.Host = strings.TrimPrefix(t.server.URL, "https://")
	return t.base.RoundTrip(r)
}
func TestCatalogRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := NewCatalog().List(context.Background(), PackageFPCalc, Platform{"windows", "arm64"}); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
