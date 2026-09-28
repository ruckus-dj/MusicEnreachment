package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubAdapterFiltersPrereleaseMasterAndIncompatibleAssets(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"master-latest","assets":[{"name":"ffmpeg-master-linux64-gpl.tar.xz","browser_download_url":"https://download.test/master"}]},{"tag_name":"8.0","assets":[{"name":"ffmpeg-8.0-linux64-gpl.tar.xz","browser_download_url":"https://download.test/8"}]},{"tag_name":"9.0-rc","prerelease":true,"assets":[{"name":"ffmpeg-9-linux64-gpl.tar.xz","browser_download_url":"https://download.test/9"}]}]`))
	}))
	defer server.Close()
	client := server.Client()
	adapter := NewBtbNAdapter(client, strings.Replace(server.URL, "https://127.0.0.1", "https://api.github.com", 1))
	adapter.client = &http.Client{Transport: rewriteTransport{server: server, base: client.Transport}}
	releases, err := adapter.List(context.Background(), Platform{"linux", "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Identity != "8.0" {
		t.Fatalf("releases = %#v", releases)
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
