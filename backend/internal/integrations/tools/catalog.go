package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
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

type Artifact struct {
	Name, URL, ChecksumURL, ChecksumSHA256 string
}
type Release struct {
	Identity  string
	Artifacts []Artifact
}
type Adapter interface {
	Package() PackageKind
	Supports(Platform) bool
	List(context.Context, Platform) ([]Release, error)
	Resolve(context.Context, Platform, string) (Release, error)
	Download(context.Context, Platform, string, string, io.Writer, func(int64)) (Artifact, int64, error)
	Checksum(context.Context, Platform, string, string) (string, error)
}

// Catalog is intentionally stateless: callers retain its response in memory.
type Catalog struct{ adapters map[PackageKind][]Adapter }

func NewCatalog(adapters ...Adapter) *Catalog {
	c := &Catalog{adapters: map[PackageKind][]Adapter{}}
	for _, adapter := range adapters {
		c.adapters[adapter.Package()] = append(c.adapters[adapter.Package()], adapter)
	}
	return c
}

func NewDefaultCatalog(client *http.Client) *Catalog {
	return NewCatalog(
		NewBtbNAdapter(client, "https://api.github.com/repos/BtbN/FFmpeg-Builds/releases"),
		NewMacOSAdapter(client, "https://ffmpeg.martin-riedl.de/"),
		NewChromaprintAdapter(client, "https://api.github.com/repos/acoustid/chromaprint/releases"),
	)
}

func (c *Catalog) List(ctx context.Context, kind PackageKind, platform Platform) ([]Release, error) {
	if !platform.Supported() {
		return nil, fmt.Errorf("unsupported platform %s/%s", platform.GOOS, platform.GOARCH)
	}
	for _, adapter := range c.adapters[kind] {
		if adapter.Supports(platform) {
			return adapter.List(ctx, platform)
		}
	}
	if len(c.adapters[kind]) == 0 {
		return nil, fmt.Errorf("no adapter for package %q", kind)
	}
	return nil, fmt.Errorf("no compatible source for package %q on %s/%s", kind, platform.GOOS, platform.GOARCH)
}

func (c *Catalog) Resolve(ctx context.Context, kind PackageKind, platform Platform, identity string) (Release, error) {
	if !platform.Supported() {
		return Release{}, fmt.Errorf("unsupported platform %s/%s", platform.GOOS, platform.GOARCH)
	}
	for _, adapter := range c.adapters[kind] {
		if adapter.Supports(platform) {
			return adapter.Resolve(ctx, platform, identity)
		}
	}
	if len(c.adapters[kind]) == 0 {
		return Release{}, fmt.Errorf("no adapter for package %q", kind)
	}
	return Release{}, fmt.Errorf("no compatible source for package %q on %s/%s", kind, platform.GOOS, platform.GOARCH)
}

func (c *Catalog) Download(ctx context.Context, kind PackageKind, platform Platform, releaseIdentity, artifactName string, destination io.Writer, progress func(int64)) (Artifact, int64, error) {
	if !platform.Supported() {
		return Artifact{}, 0, fmt.Errorf("unsupported platform %s/%s", platform.GOOS, platform.GOARCH)
	}
	for _, adapter := range c.adapters[kind] {
		if adapter.Supports(platform) {
			return adapter.Download(ctx, platform, releaseIdentity, artifactName, destination, progress)
		}
	}
	return Artifact{}, 0, fmt.Errorf("no compatible source for package %q on %s/%s", kind, platform.GOOS, platform.GOARCH)
}

func (c *Catalog) Checksum(ctx context.Context, kind PackageKind, platform Platform, releaseIdentity, artifactName string) (string, error) {
	if !platform.Supported() {
		return "", fmt.Errorf("unsupported platform %s/%s", platform.GOOS, platform.GOARCH)
	}
	for _, adapter := range c.adapters[kind] {
		if adapter.Supports(platform) {
			return adapter.Checksum(ctx, platform, releaseIdentity, artifactName)
		}
	}
	return "", fmt.Errorf("no compatible source for package %q on %s/%s", kind, platform.GOOS, platform.GOARCH)
}

type GitHubAdapter struct {
	kind         PackageKind
	endpoint     string
	client       *http.Client
	selectAssets func([]githubAsset, Platform) []Artifact
}

const (
	maxReleaseDownloadBytes = 4 << 30
	maxChecksumResponseSize = 1 << 20
)

func (a *GitHubAdapter) Download(ctx context.Context, platform Platform, releaseIdentity, artifactName string, destination io.Writer, progress func(int64)) (Artifact, int64, error) {
	release, err := a.Resolve(ctx, platform, releaseIdentity)
	if err != nil {
		return Artifact{}, 0, err
	}
	artifact, ok := findArtifact(release, artifactName)
	if !ok {
		return Artifact{}, 0, fmt.Errorf("selected artifact is no longer available")
	}
	count, err := downloadHTTPS(ctx, a.client, artifact.URL, []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}, destination, progress, maxReleaseDownloadBytes)
	if err != nil {
		return Artifact{}, count, err
	}
	return artifact, count, nil
}

func (a *GitHubAdapter) Checksum(ctx context.Context, platform Platform, releaseIdentity, artifactName string) (string, error) {
	release, err := a.Resolve(ctx, platform, releaseIdentity)
	if err != nil {
		return "", err
	}
	artifact, ok := findArtifact(release, artifactName)
	if !ok {
		return "", fmt.Errorf("selected artifact is no longer available")
	}
	if artifact.ChecksumSHA256 != "" {
		return artifact.ChecksumSHA256, nil
	}
	if artifact.ChecksumURL == "" {
		return "", nil
	}
	var contents boundedBuffer
	if _, err := downloadHTTPS(ctx, a.client, artifact.ChecksumURL, []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}, &contents, nil, maxChecksumResponseSize); err != nil {
		return "", err
	}
	return checksumForAsset(contents.String(), artifact.Name)
}

func NewChromaprintAdapter(client *http.Client, endpoint string) *GitHubAdapter {
	return newGitHubAdapter(PackageFPCalc, client, endpoint, chromaprintAssets)
}
func NewBtbNAdapter(client *http.Client, endpoint string) *GitHubAdapter {
	return newGitHubAdapter(PackageFFmpeg, client, endpoint, btbnAssets)
}
func newGitHubAdapter(kind PackageKind, client *http.Client, endpoint string, selectAssets func([]githubAsset, Platform) []Artifact) *GitHubAdapter {
	client = sourceHTTPClient(client, "github.com", "api.github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com")
	return &GitHubAdapter{kind: kind, client: client, endpoint: endpoint, selectAssets: selectAssets}
}

func (a *GitHubAdapter) Supports(platform Platform) bool {
	return platform.Supported() && (a.kind != PackageFFmpeg || platform.GOOS != "darwin")
}

func sourceHTTPClient(client *http.Client, hosts ...string) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	if client.Timeout <= 0 {
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || allowedHTTPS(request.URL.String(), hosts...) != nil {
			return fmt.Errorf("release source redirect is not allowlisted")
		}
		return nil
	}
	return client
}

func (a *GitHubAdapter) Package() PackageKind { return a.kind }
func (a *GitHubAdapter) Resolve(ctx context.Context, platform Platform, identity string) (Release, error) {
	releases, err := a.List(ctx, platform)
	if err != nil {
		return Release{}, err
	}
	for _, release := range releases {
		if release.Identity == identity {
			return release, nil
		}
	}
	return Release{}, fmt.Errorf("release %q is not available for %s/%s", identity, platform.GOOS, platform.GOARCH)
}

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
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read release catalog: %w", err)
	}
	if len(body) > 4<<20 {
		return nil, fmt.Errorf("release catalog exceeds size limit")
	}
	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("decode release catalog: %w", err)
	}
	result := make([]Release, 0, len(releases))
	for _, release := range releases {
		if release.Prerelease || !numberedRelease.MatchString(release.Tag) {
			continue
		}
		artifacts := a.selectAssets(release.Assets, platform)
		artifacts = validateArtifacts(artifacts, "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com")
		artifacts = attachGitHubChecksums(artifacts, release.Assets)
		if len(artifacts) > 0 && release.Tag != "" {
			result = append(result, Release{Identity: release.Tag, Artifacts: artifacts})
		}
	}
	sort.Slice(result, func(i, j int) bool { return compareVersions(result[i].Identity, result[j].Identity) > 0 })
	return result, nil
}

var numberedRelease = regexp.MustCompile(`^v?\d+(?:\.\d+){1,3}$`)

func attachGitHubChecksums(artifacts []Artifact, assets []githubAsset) []Artifact {
	for index := range artifacts {
		if artifacts[index].ChecksumSHA256 != "" {
			continue
		}
		for _, asset := range assets {
			if strings.EqualFold(asset.Name, "checksums.sha256") &&
				allowedHTTPS(asset.URL, "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com") == nil {
				artifacts[index].ChecksumURL = asset.URL
				break
			}
		}
	}
	return artifacts
}

func validateArtifacts(artifacts []Artifact, hosts ...string) []Artifact {
	valid := make([]Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if allowedHTTPS(artifact.URL, hosts...) != nil {
			continue
		}
		if artifact.ChecksumURL != "" && allowedHTTPS(artifact.ChecksumURL, hosts...) != nil {
			artifact.ChecksumURL = ""
		}
		valid = append(valid, artifact)
	}
	return valid
}

func findArtifact(release Release, name string) (Artifact, bool) {
	for _, artifact := range release.Artifacts {
		if artifact.Name == name {
			return artifact, true
		}
	}
	return Artifact{}, false
}

type downloadProgressWriter struct {
	destination io.Writer
	completed   int64
	progress    func(int64)
}

func (writer *downloadProgressWriter) Write(data []byte) (int, error) {
	written, err := writer.destination.Write(data)
	writer.completed += int64(written)
	if writer.progress != nil && written > 0 {
		writer.progress(writer.completed)
	}
	return written, err
}

func downloadHTTPS(ctx context.Context, baseClient *http.Client, rawURL string, hosts []string, destination io.Writer, progress func(int64), maxBytes int64) (int64, error) {
	if err := allowedHTTPS(rawURL, hosts...); err != nil {
		return 0, fmt.Errorf("release artifact URL is not allowlisted")
	}
	client := *baseClient
	client.Timeout = 30 * time.Minute
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create release artifact request")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("download release artifact")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("release artifact returned status %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return 0, fmt.Errorf("release artifact exceeds size limit")
	}
	counter := &downloadProgressWriter{destination: destination, progress: progress}
	_, err = io.Copy(counter, io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return counter.completed, fmt.Errorf("write release artifact")
	}
	if counter.completed > maxBytes {
		return counter.completed, fmt.Errorf("release artifact exceeds size limit")
	}
	return counter.completed, nil
}

type boundedBuffer struct {
	data []byte
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	if len(data) > maxChecksumResponseSize-len(buffer.data) {
		return 0, fmt.Errorf("checksum response exceeds size limit")
	}
	buffer.data = append(buffer.data, data...)
	return len(data), nil
}

func (buffer *boundedBuffer) String() string { return string(buffer.data) }

func checksumForAsset(contents, name string) (string, error) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		assetName := strings.TrimPrefix(fields[len(fields)-1], "*")
		if assetName == name && len(fields[0]) == sha256HexLength {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum response did not contain selected artifact")
}

const sha256HexLength = 64

type githubRelease struct {
	Tag        string        `json:"tag_name"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}
type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

func chromaprintAssets(assets []githubAsset, platform Platform) []Artifact {
	return selectOne(assets, platformTokens("chromaprint", platform), "")
}
func btbnAssets(assets []githubAsset, platform Platform) []Artifact {
	tokens := platformTokens("", platform)
	archiveSuffix := ".tar.xz"
	if platform.GOOS == "linux" && platform.GOARCH == "amd64" {
		tokens[1] = "linux64"
	} else if platform.GOOS == "windows" {
		tokens = []string{"win64"}
		archiveSuffix = ".zip"
	}
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasSuffix(name, archiveSuffix) || strings.Contains(name, "shared") || !strings.Contains(name, "-gpl") {
			continue
		}
		matches := true
		for _, token := range tokens {
			matches = matches && strings.Contains(name, token)
		}
		if matches && asset.URL != "" {
			return []Artifact{{Name: asset.Name, URL: asset.URL, ChecksumSHA256: sha256Digest(asset.Digest)}}
		}
	}
	return nil
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
			return []Artifact{{Name: asset.Name, URL: asset.URL, ChecksumSHA256: sha256Digest(asset.Digest)}}
		}
	}
	return nil
}

func sha256Digest(digest string) string {
	if strings.HasPrefix(digest, "sha256:") {
		return strings.TrimPrefix(digest, "sha256:")
	}
	return ""
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
	client = sourceHTTPClient(client, "ffmpeg.martin-riedl.de")
	return &MacOSAdapter{client: client, endpoint: endpoint}
}
func (*MacOSAdapter) Package() PackageKind { return PackageFFmpeg }
func (*MacOSAdapter) Supports(platform Platform) bool {
	return platform.GOOS == "darwin" && platform.Supported()
}

func (a *MacOSAdapter) Download(ctx context.Context, platform Platform, releaseIdentity, artifactName string, destination io.Writer, progress func(int64)) (Artifact, int64, error) {
	release, err := a.Resolve(ctx, platform, releaseIdentity)
	if err != nil {
		return Artifact{}, 0, err
	}
	artifact, ok := findArtifact(release, artifactName)
	if !ok {
		return Artifact{}, 0, fmt.Errorf("selected artifact is no longer available")
	}
	count, err := downloadHTTPS(ctx, a.client, artifact.URL, []string{"ffmpeg.martin-riedl.de"}, destination, progress, maxReleaseDownloadBytes)
	if err != nil {
		return Artifact{}, count, err
	}
	return artifact, count, nil
}

func (a *MacOSAdapter) Checksum(ctx context.Context, platform Platform, releaseIdentity, artifactName string) (string, error) {
	release, err := a.Resolve(ctx, platform, releaseIdentity)
	if err != nil {
		return "", err
	}
	artifact, ok := findArtifact(release, artifactName)
	if !ok {
		return "", fmt.Errorf("selected artifact is no longer available")
	}
	if artifact.ChecksumSHA256 != "" {
		return artifact.ChecksumSHA256, nil
	}
	if artifact.ChecksumURL == "" {
		return "", nil
	}
	var contents boundedBuffer
	if _, err := downloadHTTPS(ctx, a.client, artifact.ChecksumURL, []string{"ffmpeg.martin-riedl.de"}, &contents, nil, maxChecksumResponseSize); err != nil {
		return "", err
	}
	return checksumForAsset(contents.String(), artifact.Name)
}

func (a *MacOSAdapter) Resolve(ctx context.Context, platform Platform, identity string) (Release, error) {
	releases, err := a.List(ctx, platform)
	if err != nil {
		return Release{}, err
	}
	for _, release := range releases {
		if release.Identity == identity {
			return release, nil
		}
	}
	return Release{}, fmt.Errorf("release %q is not available for %s/%s", identity, platform.GOOS, platform.GOARCH)
}

var macHref = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)
var macReleasePath = regexp.MustCompile(`^/download/macos/(amd64|arm64)/\d+_([^/]+)/(ffmpeg|ffprobe)\.zip(\.sha256)?$`)

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
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 4<<20 {
		return nil, fmt.Errorf("release catalog exceeds size limit")
	}
	artifactsByIdentity := make(map[string]map[string]Artifact)
	baseURL, err := url.Parse(a.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse release catalog endpoint: %w", err)
	}
	for _, match := range macHref.FindAllSubmatch(body, -1) {
		reference, err := url.Parse(string(match[1]))
		if err != nil {
			continue
		}
		u := baseURL.ResolveReference(reference)
		if allowedHTTPS(u.String(), "ffmpeg.martin-riedl.de") != nil {
			continue
		}
		parts := macReleasePath.FindStringSubmatch(u.Path)
		if len(parts) != 5 || parts[1] != platform.GOARCH || !numberedRelease.MatchString(parts[2]) {
			continue
		}
		identity, kind := parts[2], parts[3]
		if artifactsByIdentity[identity] == nil {
			artifactsByIdentity[identity] = make(map[string]Artifact)
		}
		artifact := artifactsByIdentity[identity][kind]
		if artifact.Name == "" {
			artifact.Name = kind + ".zip"
		}
		if parts[4] == ".sha256" {
			artifact.ChecksumURL = u.String()
		} else {
			artifact.URL = u.String()
		}
		artifactsByIdentity[identity][kind] = artifact
	}
	releases := make([]Release, 0, len(artifactsByIdentity))
	for identity, artifacts := range artifactsByIdentity {
		ffmpeg, hasFFmpeg := artifacts["ffmpeg"]
		ffprobe, hasFFprobe := artifacts["ffprobe"]
		if hasFFmpeg && hasFFprobe && ffmpeg.URL != "" && ffprobe.URL != "" {
			releases = append(releases, Release{Identity: identity, Artifacts: []Artifact{ffmpeg, ffprobe}})
		}
	}
	sort.Slice(releases, func(i, j int) bool { return compareVersions(releases[i].Identity, releases[j].Identity) > 0 })
	return releases, nil
}

func compareVersions(first, second string) int {
	firstParts := strings.Split(strings.TrimPrefix(first, "v"), ".")
	secondParts := strings.Split(strings.TrimPrefix(second, "v"), ".")
	for index := 0; index < len(firstParts) || index < len(secondParts); index++ {
		firstPart, secondPart := 0, 0
		if index < len(firstParts) {
			firstPart, _ = strconv.Atoi(firstParts[index])
		}
		if index < len(secondParts) {
			secondPart, _ = strconv.Atoi(secondParts[index])
		}
		if firstPart > secondPart {
			return 1
		}
		if firstPart < secondPart {
			return -1
		}
	}
	return 0
}

func allowedHTTPS(raw string, hosts ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("source URL must use HTTPS")
	}
	for _, host := range hosts {
		if u.Hostname() == host {
			return nil
		}
	}
	return fmt.Errorf("source host %q is not allowlisted", u.Host)
}
