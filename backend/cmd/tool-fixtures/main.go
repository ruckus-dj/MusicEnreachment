// Command tool-fixtures provisions approved managed FFmpeg binaries and a
// seek-dependent MP4 used by the managed transport evidence lane.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

type artifactEvidence struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	ChecksumURL string `json:"checksum_url,omitempty"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
}

type fixtureManifest struct {
	Platform    string             `json:"platform"`
	Source      string             `json:"source"`
	Release     string             `json:"release"`
	Artifacts   []artifactEvidence `json:"artifacts"`
	Versions    map[string]string  `json:"versions"`
	FFmpegPath  string             `json:"ffmpeg_path"`
	FFprobePath string             `json:"ffprobe_path"`
	FixturePath string             `json:"fixture_path"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("tool-fixtures", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	release := flags.String("release", "latest", "approved numbered FFmpeg release, or latest numbered release")
	output := flags.String("output", "", "output directory for managed binaries and fixture")
	manifestPath := flags.String("manifest", "", "path to write evidence manifest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" || *manifestPath == "" || *release == "" {
		return errors.New("-release, -output, and -manifest are required; positional arguments are not accepted")
	}
	platform := tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if !platform.Supported() {
		return fmt.Errorf("unsupported fixture provisioning platform %s/%s", platform.GOOS, platform.GOARCH)
	}
	outputAbs, err := filepath.Abs(*output)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	manifestAbs, err := filepath.Abs(*manifestPath)
	if err != nil {
		return fmt.Errorf("resolve manifest path: %w", err)
	}
	if err := os.MkdirAll(outputAbs, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	catalog := tools.NewDefaultCatalog(nil)
	selected := *release
	if selected == "latest" {
		releases, err := catalog.List(ctx, tools.PackageFFmpeg, platform)
		if err != nil {
			return fmt.Errorf("list approved FFmpeg releases: %w", err)
		}
		if len(releases) == 0 {
			return errors.New("approved catalog contains no numbered FFmpeg releases")
		}
		selected = releases[0].Identity
	}
	resolved, err := catalog.Resolve(ctx, tools.PackageFFmpeg, platform, selected)
	if err != nil {
		return fmt.Errorf("resolve approved FFmpeg release %q: %w", selected, err)
	}
	if resolved.Identity != selected || len(resolved.Artifacts) == 0 {
		return errors.New("approved catalog returned an invalid release")
	}
	if platform.GOOS == "darwin" && (len(resolved.Artifacts) != 2 || !hasArtifact(resolved.Artifacts, "ffmpeg.zip") || !hasArtifact(resolved.Artifacts, "ffprobe.zip")) {
		return errors.New("approved macOS release must provide separate ffmpeg.zip and ffprobe.zip artifacts")
	}

	work, err := os.MkdirTemp(outputAbs, ".fixture-provision-")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	archivesDir := filepath.Join(work, "archives")
	extractDir := filepath.Join(work, "extracted")
	if err := os.MkdirAll(archivesDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}

	evidence := make([]artifactEvidence, 0, len(resolved.Artifacts))
	for index, listed := range resolved.Artifacts {
		if listed.Name == "" || listed.URL == "" {
			return errors.New("approved release artifact is missing its name or URL")
		}
		checksum, err := catalog.Checksum(ctx, tools.PackageFFmpeg, platform, selected, listed.Name)
		if err != nil {
			return fmt.Errorf("retrieve SHA-256 for %s: %w", listed.Name, err)
		}
		checksum = strings.ToLower(strings.TrimSpace(checksum))
		if len(checksum) != sha256.Size*2 {
			return fmt.Errorf("approved source has no usable SHA-256 evidence for %s", listed.Name)
		}
		if _, err := hex.DecodeString(checksum); err != nil {
			return fmt.Errorf("approved source returned invalid SHA-256 for %s", listed.Name)
		}
		archivePath := filepath.Join(archivesDir, fmt.Sprintf("%02d-%s", index, filepath.Base(listed.Name)))
		archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("create archive file: %w", err)
		}
		downloaded, size, downloadErr := catalog.Download(ctx, tools.PackageFFmpeg, platform, selected, listed.Name, archive, nil)
		closeErr := archive.Close()
		if downloadErr != nil {
			return fmt.Errorf("download %s: %w", listed.Name, downloadErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", listed.Name, closeErr)
		}
		if size <= 0 || downloaded.Name != listed.Name || downloaded.URL != listed.URL {
			return fmt.Errorf("downloaded artifact does not match resolved catalog entry %s", listed.Name)
		}
		actual, err := hashFile(archivePath)
		if err != nil {
			return fmt.Errorf("hash %s: %w", listed.Name, err)
		}
		if actual != checksum {
			return fmt.Errorf("SHA-256 mismatch for %s", listed.Name)
		}
		artifactExtract := filepath.Join(extractDir, fmt.Sprintf("%02d", index))
		if err := os.MkdirAll(artifactExtract, 0o755); err != nil {
			return err
		}
		if err := extract(archivePath, artifactExtract); err != nil {
			return fmt.Errorf("extract %s: %w", listed.Name, err)
		}
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if path, err := locate(artifactExtract, executable(name, platform.GOOS)); err == nil {
				target := filepath.Join(work, "staging", executable(name, platform.GOOS))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				if err := copyFile(path, target); err != nil {
					return fmt.Errorf("stage %s: %w", name, err)
				}
			}
		}
		evidence = append(evidence, artifactEvidence{Name: listed.Name, URL: listed.URL, ChecksumURL: listed.ChecksumURL, SHA256: checksum, Bytes: size})
	}

	staging := filepath.Join(work, "staging")
	stagingFFmpeg := filepath.Join(staging, executable("ffmpeg", platform.GOOS))
	stagingFFprobe := filepath.Join(staging, executable("ffprobe", platform.GOOS))
	if _, err := os.Stat(stagingFFmpeg); err != nil {
		return errors.New("approved release archives do not contain ffmpeg")
	}
	if _, err := os.Stat(stagingFFprobe); err != nil {
		return errors.New("approved release archives do not contain ffprobe")
	}
	root := filepath.Join(outputAbs, "tools")
	lifecycle := tools.NewLifecycle(nil)
	relative, versions, err := lifecycle.Materialize(ctx, staging, root, tools.PackageFFmpeg, selected, tools.MaterializeOptions{GOOS: platform.GOOS, GOARCH: platform.GOARCH})
	if err != nil {
		return fmt.Errorf("materialize verified FFmpeg release: %w", err)
	}
	if _, err := lifecycle.VerifyInstallation(ctx, root, relative, tools.PackageFFmpeg, selected, platform.GOOS); err != nil {
		return fmt.Errorf("verify managed installation: %w", err)
	}
	fixturePath := filepath.Join(outputAbs, "seek-dependent.mp4")
	if err := createFixture(ctx, filepath.Join(root, relative, executable("ffmpeg", platform.GOOS)), fixturePath); err != nil {
		return err
	}
	manifest := fixtureManifest{
		Platform:    platform.GOOS + "/" + platform.GOARCH,
		Source:      sourceFor(platform),
		Release:     selected,
		Artifacts:   evidence,
		Versions:    versions,
		FFmpegPath:  filepath.Join(root, relative, executable("ffmpeg", platform.GOOS)),
		FFprobePath: filepath.Join(root, relative, executable("ffprobe", platform.GOOS)),
		FixturePath: fixturePath,
	}
	if err := writeManifest(manifestAbs, manifest); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "provisioned %s FFmpeg %s and %s\n", manifest.Platform, selected, fixturePath)
	return err
}

func hasArtifact(artifacts []tools.Artifact, name string) bool {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return true
		}
	}
	return false
}

func sourceFor(platform tools.Platform) string {
	if platform.GOOS == "darwin" {
		return "https://ffmpeg.martin-riedl.de/"
	}
	return "https://github.com/BtbN/FFmpeg-Builds/releases"
}

func executable(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

func extract(archive, destination string) error {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return tools.ExtractZip(archive, destination)
	case strings.HasSuffix(archive, ".tar.xz"):
		return tools.ExtractTarXz(archive, destination)
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"):
		return tools.ExtractTarGzip(archive, destination)
	default:
		return fmt.Errorf("unsupported approved archive format: %s", filepath.Base(archive))
	}
}

func locate(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	return found, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return os.Chmod(target, 0o755)
}

func createFixture(ctx context.Context, ffmpeg, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	command := tools.SystemRunner{}
	output, err := command.Run(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "anoisesrc=color=white:sample_rate=48000:duration=90",
		"-c:a", "alac", "-vn", destination,
	)
	if err != nil {
		return fmt.Errorf("generate seek-dependent ALAC MP4: %s: %w", strings.TrimSpace(string(output)), err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		return fmt.Errorf("inspect generated fixture: %w", err)
	}
	if info.Size() < 1<<20 {
		return fmt.Errorf("generated MP4 is unexpectedly small (%d bytes)", info.Size())
	}
	if err := verifyTailMoov(destination); err != nil {
		return fmt.Errorf("generated MP4 is not seek-dependent: %w", err)
	}
	return nil
}

func verifyTailMoov(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	var offset int64
	var sawMedia bool
	for offset < info.Size() {
		var header [16]byte
		if _, err := file.ReadAt(header[:8], offset); err != nil {
			return fmt.Errorf("read MP4 atom header: %w", err)
		}
		size := int64(uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3]))
		headerSize := int64(8)
		switch size {
		case 1:
			if _, err := file.ReadAt(header[8:], offset+8); err != nil {
				return fmt.Errorf("read extended MP4 atom header: %w", err)
			}
			size = int64(uint64(header[8])<<56 | uint64(header[9])<<48 | uint64(header[10])<<40 | uint64(header[11])<<32 |
				uint64(header[12])<<24 | uint64(header[13])<<16 | uint64(header[14])<<8 | uint64(header[15]))
			headerSize = 16
		case 0:
			size = info.Size() - offset
		}
		if size < headerSize || size > info.Size()-offset {
			return errors.New("invalid MP4 atom size")
		}
		kind := string(header[4:8])
		if kind == "mdat" {
			sawMedia = true
		}
		if kind == "moov" {
			if !sawMedia || offset+size != info.Size() {
				return errors.New("moov atom does not follow media data at end of file")
			}
			return nil
		}
		offset += size
	}
	return errors.New("MP4 has no trailing moov atom")
}

func writeManifest(path string, manifest fixtureManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(manifest)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("write manifest: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close manifest: %w", closeErr)
	}
	return nil
}
