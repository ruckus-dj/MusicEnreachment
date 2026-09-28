package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

type PackageKind string

const (
	PackageFFmpeg PackageKind = "ffmpeg"
	PackageFPCalc PackageKind = "fpcalc"
)

type Platform struct{ GOOS, GOARCH string }

func (p Platform) Supported() bool {
	return (p.GOOS == "linux" || p.GOOS == "darwin") && (p.GOARCH == "amd64" || p.GOARCH == "arm64") || p.GOOS == "windows" && p.GOARCH == "amd64"
}

type Artifact struct{ Name, URL, ChecksumURL string }
type Release struct {
	Identity  string
	Artifacts []Artifact
}
type Adapter interface {
	Package() PackageKind
	List(context.Context, Platform) ([]Release, error)
}

// Catalog is intentionally stateless: callers retain its response in memory.
type Catalog struct{ adapters map[PackageKind]Adapter }

func NewCatalog(adapters ...Adapter) *Catalog {
	c := &Catalog{adapters: map[PackageKind]Adapter{}}
	for _, adapter := range adapters {
		c.adapters[adapter.Package()] = adapter
	}
	return c
}
func (c *Catalog) List(ctx context.Context, kind PackageKind, platform Platform) ([]Release, error) {
	if !platform.Supported() {
		return nil, fmt.Errorf("unsupported platform %s/%s", platform.GOOS, platform.GOARCH)
	}
	adapter := c.adapters[kind]
	if adapter == nil {
		return nil, fmt.Errorf("no adapter for package %q", kind)
	}
	return adapter.List(ctx, platform)
}

type GitHubAdapter struct {
	kind         PackageKind
	endpoint     string
	client       *http.Client
	selectAssets func([]githubAsset, Platform) []Artifact
}

func NewChromaprintAdapter(client *http.Client, endpoint string) *GitHubAdapter {
	return newGitHubAdapter(PackageFPCalc, client, endpoint, chromaprintAssets)
}
func NewBtbNAdapter(client *http.Client, endpoint string) *GitHubAdapter {
	return newGitHubAdapter(PackageFFmpeg, client, endpoint, btbnAssets)
}
func newGitHubAdapter(kind PackageKind, client *http.Client, endpoint string, selectAssets func([]githubAsset, Platform) []Artifact) *GitHubAdapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &GitHubAdapter{kind: kind, client: client, endpoint: endpoint, selectAssets: selectAssets}
}
func (a *GitHubAdapter) Package() PackageKind { return a.kind }
func (a *GitHubAdapter) List(ctx context.Context, platform Platform) ([]Release, error) {
	if err := allowedHTTPS(a.endpoint, "github.com", "api.github.com"); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request release catalog: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release catalog returned %s", response.Status)
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode release catalog: %w", err)
	}
	result := make([]Release, 0, len(releases))
	for _, release := range releases {
		if release.Prerelease || strings.Contains(strings.ToLower(release.Tag), "master") {
			continue
		}
		artifacts := a.selectAssets(release.Assets, platform)
		if len(artifacts) > 0 && release.Tag != "" {
			result = append(result, Release{Identity: release.Tag, Artifacts: artifacts})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Identity > result[j].Identity })
	return result, nil
}

type githubRelease struct {
	Tag        string        `json:"tag_name"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}
type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func chromaprintAssets(assets []githubAsset, platform Platform) []Artifact {
	return selectOne(assets, platformTokens("chromaprint", platform), "")
}
func btbnAssets(assets []githubAsset, platform Platform) []Artifact {
	tokens := platformTokens("", platform)
	if platform.GOOS == "linux" && platform.GOARCH == "amd64" {
		tokens[1] = "linux64"
	}
	return selectOne(assets, tokens, "gpl")
}
func selectOne(assets []githubAsset, required []string, extra string) []Artifact {
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if extra != "" && !strings.Contains(name, extra) {
			continue
		}
		valid := asset.URL != ""
		for _, token := range required {
			valid = valid && strings.Contains(name, token)
		}
		if valid {
			return []Artifact{{Name: asset.Name, URL: asset.URL}}
		}
	}
	return nil
}
func platformTokens(prefix string, platform Platform) []string {
	os := map[string]string{"darwin": "macos"}[platform.GOOS]
	if os == "" {
		os = platform.GOOS
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[platform.GOARCH]
	tokens := []string{os}
	if prefix != "" {
		tokens = append(tokens, prefix)
	}
	return append(tokens, arch)
}

// MacOSAdapter consumes only release archive links, never snapshot links.
type MacOSAdapter struct {
	endpoint string
	client   *http.Client
}

func NewMacOSAdapter(client *http.Client, endpoint string) *MacOSAdapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &MacOSAdapter{client: client, endpoint: endpoint}
}
func (*MacOSAdapter) Package() PackageKind { return PackageFFmpeg }

var macURL = regexp.MustCompile(`https://[^"'<> ]+`)

func (a *MacOSAdapter) List(ctx context.Context, platform Platform) ([]Release, error) {
	if platform.GOOS != "darwin" {
		return nil, nil
	}
	if err := allowedHTTPS(a.endpoint, "ffmpeg.martin-riedl.de"); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release catalog returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	releases := []Release{}
	for _, raw := range macURL.FindAllString(string(body), -1) {
		u, err := url.Parse(raw)
		if err != nil || u.Host != "ffmpeg.martin-riedl.de" {
			continue
		}
		name := strings.ToLower(path.Base(u.Path))
		if strings.Contains(name, "snapshot") || !strings.Contains(name, "gpl") || !strings.Contains(name, platform.GOARCH) {
			continue
		}
		identity := strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
		releases = append(releases, Release{Identity: identity, Artifacts: []Artifact{{Name: path.Base(u.Path), URL: raw}}})
	}
	return releases, nil
}

func allowedHTTPS(raw string, hosts ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" {
		return fmt.Errorf("source URL must use HTTPS")
	}
	for _, host := range hosts {
		if u.Hostname() == host {
			return nil
		}
	}
	return fmt.Errorf("source host %q is not allowlisted", u.Host)
}
