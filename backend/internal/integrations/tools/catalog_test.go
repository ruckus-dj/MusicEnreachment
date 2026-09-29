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

func TestGitHubAdapterFiltersPrereleaseMasterAndIncompatibleAssets(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"master-latest","assets":[{"name":"ffmpeg-master-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/master"}]},{"tag_name":"8.0","assets":[{"name":"ffmpeg-8.0-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/8"}]},{"tag_name":"latest","assets":[{"name":"ffmpeg-latest-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/latest"}]},{"tag_name":"9.0-rc","prerelease":true,"assets":[{"name":"ffmpeg-9-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/9"}]}]`))
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewBtbNAdapter(client, "https://api.github.com/releases")
	adapter.client = &http.Client{Transport: rewriteTransport{server: server, base: client.Transport}}
	releases, err := adapter.List(context.Background(), Platform{"linux", "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Identity != "8.0" {
		t.Fatalf("releases = %#v", releases)
	}
}

func TestGitHubAdapterResolveReloadsAndRejectsUnlistedRelease(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"8.0","assets":[{"name":"ffmpeg-8.0-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/8"}]}]`))
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewBtbNAdapter(client, "https://api.github.com/releases")
	adapter.client = &http.Client{Transport: rewriteTransport{server: server, base: client.Transport}}

	release, err := adapter.Resolve(context.Background(), Platform{"linux", "amd64"}, "8.0")
	if err != nil || len(release.Artifacts) != 1 {
		t.Fatalf("resolve release = %#v, %v", release, err)
	}
	if _, err := adapter.Resolve(context.Background(), Platform{"linux", "amd64"}, "7.0"); err == nil {
		t.Fatal("resolve accepted an unavailable release")
	}
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

func TestBtbNAssetsMatchSupportedPlatformArchives(t *testing.T) {
	assets := []githubAsset{
		{Name: "ffmpeg-n8.1-linux64-lgpl-8.1.tar.xz", URL: "https://github.com/BtbN/lgpl"},
		{Name: "ffmpeg-n8.1-linux64-gpl-8.1.tar.xz", URL: "https://github.com/BtbN/linux64", Digest: "sha256:aaaaaaaa"},
		{Name: "ffmpeg-n8.1-linuxarm64-gpl-8.1.tar.xz", URL: "https://github.com/BtbN/linuxarm64", Digest: "sha256:bbbbbbbb"},
		{Name: "ffmpeg-n8.1-win64-gpl-8.1.zip", URL: "https://github.com/BtbN/win64", Digest: "sha256:cccccccc"},
		{Name: "ffmpeg-n8.1-win64-gpl-shared.zip", URL: "https://github.com/BtbN/shared"},
	}
	tests := []struct {
		platform Platform
		want     string
	}{
		{Platform{"linux", "amd64"}, "linux64"},
		{Platform{"linux", "arm64"}, "linuxarm64"},
		{Platform{"windows", "amd64"}, "win64"},
	}
	for _, test := range tests {
		artifacts := btbnAssets(assets, test.platform)
		if len(artifacts) != 1 || !strings.Contains(artifacts[0].Name, test.want) || artifacts[0].ChecksumSHA256 == "" {
			t.Errorf("btbnAssets(%#v) = %#v", test.platform, artifacts)
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
			_, _ = w.Write([]byte(`[{"tag_name":"8.0","assets":[{"name":"ffmpeg-8.0-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/ffmpeg-8.0-linux64-gpl.tar.xz","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}]`))
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
	artifact, count, err := catalog.Download(context.Background(), PackageFFmpeg, Platform{"linux", "amd64"}, "8.0", "ffmpeg-8.0-linux64-gpl.tar.xz", &destination, func(completed int64) {
		progress = append(progress, completed)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.Bytes(), archive) || count != int64(len(archive)) || artifact.ChecksumSHA256 == "" {
		t.Fatalf("download result = %q, %d, %#v", destination.Bytes(), count, artifact)
	}
	if len(progress) == 0 || progress[len(progress)-1] != int64(len(archive)) {
		t.Fatalf("progress events = %#v", progress)
	}
}

func TestCatalogDownloadRejectsRedirectOutsideSourceAllowlist(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			_, _ = w.Write([]byte(`[{"tag_name":"8.0","assets":[{"name":"ffmpeg-8.0-linux64-gpl.tar.xz","browser_download_url":"https://github.com/BtbN/ffmpeg-8.0-linux64-gpl.tar.xz"}]}]`))
			return
		}
		http.Redirect(w, r, "https://evil.example/archive", http.StatusFound)
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewBtbNAdapter(client, "https://api.github.com/releases")
	adapter.client.Transport = rewriteTransport{server: server, base: client.Transport}
	catalog := NewCatalog(adapter)

	if _, _, err := catalog.Download(context.Background(), PackageFFmpeg, Platform{"linux", "amd64"}, "8.0", "ffmpeg-8.0-linux64-gpl.tar.xz", io.Discard, nil); err == nil {
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
