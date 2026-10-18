package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"unicode/utf8"

	"go.senan.xyz/taglib"
)

const maxFLACMetadataBytes = 64 << 20

// FlacArtworkSnapshot is an opaque proof of a bounded FLAC artwork observation.
// It is not a restoration payload; TagLib's pinned API cannot reconstruct every
// FLAC PICTURE field. The caller must exclusively own path while writing it.
type FlacArtworkSnapshot struct {
	path        string
	identity    os.FileInfo
	streamInfo  [34]byte
	audioHash   [32]byte
	pictureHash [32]byte
	imageHash   [32]byte
	count       int
	description string
	mime        string
	pictureType uint32
	imageSize   int
}

// ApprovedCoverFacts is a detached description of the approved native cover.
type ApprovedCoverFacts struct {
	Count       int
	ImageSHA256 [32]byte
	MIME        string
	ImageSize   int
}

// ApprovedCoverFacts returns detached facts about the snapshot's TagLib image.
func (s FlacArtworkSnapshot) ApprovedCoverFacts() ApprovedCoverFacts {
	return ApprovedCoverFacts{Count: s.count, ImageSHA256: s.imageHash, MIME: s.mime, ImageSize: s.imageSize}
}

// VerifyEquivalent compares native artwork and audio facts, not filesystem identity.
func (s FlacArtworkSnapshot) VerifyEquivalent(ctx context.Context, otherPath string) error {
	if s.path == "" || s.identity == nil || s.streamInfo == ([34]byte{}) || s.audioHash == ([32]byte{}) {
		return errors.New("invalid FLAC artwork snapshot")
	}
	other, err := SnapshotFlacArtwork(ctx, otherPath)
	if err != nil {
		return fmt.Errorf("verify equivalent FLAC: %w", err)
	}
	if !sameFlacArtworkContents(s, other) {
		return errors.New("FLAC artwork or audio differs from snapshot")
	}
	return nil
}

type flacPicture struct {
	hash              [32]byte
	typ               uint32
	mime, description string
}

// SnapshotFlacArtwork verifies native FLAC metadata structure and obtains image
// descriptors/payload bytes from TagLib on the same open handle.
func SnapshotFlacArtwork(ctx context.Context, path string) (snapshot FlacArtworkSnapshot, retErr error) {
	if err := ctx.Err(); err != nil {
		return FlacArtworkSnapshot{}, err
	}
	if path == "" {
		return FlacArtworkSnapshot{}, errors.New("FLAC path is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return FlacArtworkSnapshot{}, fmt.Errorf("open FLAC: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close FLAC: %w", closeErr))
			snapshot = FlacArtworkSnapshot{}
		}
	}()
	openedInfo, err := f.Stat()
	if err != nil {
		return FlacArtworkSnapshot{}, fmt.Errorf("stat FLAC: %w", err)
	}
	var sig [4]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil || string(sig[:]) != "fLaC" {
		return FlacArtworkSnapshot{}, errors.New("not a native FLAC stream")
	}
	h := sha256.New()
	var info [34]byte
	var pictures []flacPicture
	var comments int
	metadataBytes := uint64(0)
	last := false
	for blockIndex := 0; !last; blockIndex++ {
		var head [4]byte
		if _, err := io.ReadFull(f, head[:]); err != nil {
			return FlacArtworkSnapshot{}, fmt.Errorf("read FLAC metadata header: %w", err)
		}
		last = head[0]&0x80 != 0
		typ := head[0] & 0x7f
		size := uint64(head[1])<<16 | uint64(head[2])<<8 | uint64(head[3])
		var withinBudget bool
		metadataBytes, withinBudget = addFLACMetadataBudget(metadataBytes, size)
		if !withinBudget {
			return FlacArtworkSnapshot{}, errors.New("FLAC metadata exceeds audit limit")
		}
		if blockIndex == 0 && (typ != 0 || size != 34) {
			return FlacArtworkSnapshot{}, errors.New("FLAC STREAMINFO must be first and 34 bytes")
		}
		if typ == 0 && (blockIndex != 0 || size != 34) {
			return FlacArtworkSnapshot{}, errors.New("invalid FLAC STREAMINFO block")
		}
		if typ == 127 {
			return FlacArtworkSnapshot{}, errors.New("reserved FLAC metadata block type")
		}
		if typ == 4 {
			comments++
		}
		if typ == 4 || typ == 6 {
			data := make([]byte, size)
			if _, err := io.ReadFull(f, data); err != nil {
				return FlacArtworkSnapshot{}, fmt.Errorf("read FLAC metadata block: %w", err)
			}
			if typ == 4 {
				found, err := parseVorbisComments(data)
				if err != nil {
					return FlacArtworkSnapshot{}, err
				}
				if found {
					return FlacArtworkSnapshot{}, errors.New("alternate FLAC artwork comment is unsupported")
				}
			}
			if typ == 6 {
				p, err := parseFLACPicture(data)
				if err != nil {
					return FlacArtworkSnapshot{}, err
				}
				pictures = append(pictures, p)
			}
			continue
		}
		if typ == 0 {
			if _, err := io.ReadFull(f, info[:]); err != nil {
				return FlacArtworkSnapshot{}, fmt.Errorf("read FLAC STREAMINFO: %w", err)
			}
			continue
		}
		if _, err := io.CopyN(io.Discard, f, int64(size)); err != nil {
			return FlacArtworkSnapshot{}, fmt.Errorf("skip FLAC metadata block: %w", err)
		}
	}
	if comments > 1 {
		return FlacArtworkSnapshot{}, errors.New("duplicate FLAC Vorbis-comment blocks")
	}
	if len(pictures) > 1 {
		return FlacArtworkSnapshot{}, errors.New("multiple FLAC PICTURE blocks are unsupported")
	}
	if _, err := io.Copy(h, f); err != nil {
		return FlacArtworkSnapshot{}, fmt.Errorf("hash FLAC audio frames: %w", err)
	}
	s := FlacArtworkSnapshot{path: path, identity: openedInfo, streamInfo: info, audioHash: sumHash(h), count: len(pictures)}
	if len(pictures) == 1 {
		p := pictures[0]
		s.pictureHash = p.hash
		s.pictureType = p.typ
		s.mime = p.mime
		s.description = p.description
	}
	if err := verifyTagLibFlac(ctx, path, &s, openedInfo); err != nil {
		return FlacArtworkSnapshot{}, err
	}
	return s, nil
}

func addFLACMetadataBudget(current, blockSize uint64) (uint64, bool) {
	if blockSize > maxFLACMetadataBytes || current > maxFLACMetadataBytes-4 || blockSize > maxFLACMetadataBytes-current-4 {
		return current, false
	}
	return current + 4 + blockSize, true
}

func WritePreservingFlacArtwork(ctx context.Context, path string, tags map[string][]string, before FlacArtworkSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" || before.path != path {
		return errors.New("FLAC artwork snapshot does not match path")
	}
	current, err := SnapshotFlacArtwork(ctx, path)
	if err != nil {
		return fmt.Errorf("verify FLAC artwork before write: %w", err)
	}
	if current.identity == nil || before.identity == nil || !os.SameFile(current.identity, before.identity) || !sameFlacArtworkFacts(current, before) {
		return errors.New("FLAC changed since artwork snapshot")
	}
	if err := safeWriteTags(taglib.WriteTags, path, nonEmptyTags(tags), taglib.Clear); err != nil {
		return fmt.Errorf("write FLAC tags: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	after, err := SnapshotFlacArtwork(ctx, path)
	if err != nil {
		return fmt.Errorf("verify FLAC after tag write: %w", err)
	}
	if after.count != before.count || after.pictureHash != before.pictureHash || after.imageHash != before.imageHash || after.pictureType != before.pictureType || after.description != before.description || after.mime != before.mime || after.streamInfo != before.streamInfo || after.audioHash != before.audioHash {
		return errors.New("FLAC artwork or audio changed during tag write; no restoration was attempted")
	}
	return nil
}

func sameFlacArtworkFacts(a, b FlacArtworkSnapshot) bool {
	return a.path == b.path && a.streamInfo == b.streamInfo && a.audioHash == b.audioHash &&
		a.pictureHash == b.pictureHash && a.imageHash == b.imageHash && a.count == b.count &&
		a.description == b.description && a.mime == b.mime && a.pictureType == b.pictureType && a.imageSize == b.imageSize
}

func verifyTagLibFlac(ctx context.Context, path string, s *FlacArtworkSnapshot, openedInfo os.FileInfo) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := taglib.OpenReadOnly(path)
	if err != nil {
		return fmt.Errorf("open FLAC with TagLib: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close TagLib FLAC: %w", closeErr))
		}
	}()
	if f.Format() != taglib.FormatFLAC {
		return errors.New("TagLib did not verify FLAC format")
	}
	// The pinned binding's handle Properties method discards native errors. Its
	// error-returning path reader is used instead, bracketed by identity checks
	// against this open handle so results cannot silently describe a replacement.
	opened, err := os.Stat(path)
	if err != nil || !sameFileSnapshot(openedInfo, opened) {
		return errors.New("FLAC path changed before TagLib properties read")
	}
	if _, err := taglib.ReadProperties(path); err != nil {
		return fmt.Errorf("read TagLib FLAC properties: %w", err)
	}
	after, err := os.Stat(path)
	if err != nil || !sameFileSnapshot(openedInfo, after) || !sameFileSnapshot(opened, after) {
		return errors.New("FLAC path changed during TagLib properties read")
	}
	p := f.Properties()
	if len(p.Images) != s.count {
		return errors.New("TagLib FLAC picture count disagrees with structural audit")
	}
	if s.count == 1 {
		if len(p.Images) != 1 {
			return errors.New("TagLib did not expose the FLAC picture")
		}
		d := p.Images[0]
		data, err := f.Image(0)
		if err != nil {
			return fmt.Errorf("read TagLib FLAC image: %w", err)
		}
		if len(data) == 0 {
			return errors.New("TagLib returned an empty FLAC picture payload")
		}
		if d.Type != flacPictureTypeName(s.pictureType) || d.Description != s.description || d.MIMEType != s.mime {
			return errors.New("TagLib FLAC picture descriptor disagrees with structural audit")
		}
		s.imageHash = sha256.Sum256(data)
		s.imageSize = len(data)
	}
	return ctx.Err()
}

// WriteFlacTagsPreservingArtwork updates metadata on the snapshot-bound FLAC and
// returns fresh TagLib readback. Unlike Writer.Write it permits only LYRICS to
// differ from the requested metadata, because TagLib may omit that optional field.
func WriteFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before FlacArtworkSnapshot) (WriteResult, error) {
	return writeFlacTagsPreservingArtwork(ctx, path, tags, before, taglib.WriteTags, taglib.ReadTags)
}

func writeFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before FlacArtworkSnapshot, write func(string, map[string][]string, taglib.WriteOption) error, read func(string) (map[string][]string, error)) (WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	if path == "" || before.path != path || before.identity == nil {
		return WriteResult{}, errors.New("FLAC artwork snapshot does not match path")
	}
	current, err := SnapshotFlacArtwork(ctx, path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("verify FLAC artwork before write: %w", err)
	}
	if current.identity == nil || !os.SameFile(current.identity, before.identity) || !sameFlacArtworkFacts(current, before) {
		return WriteResult{}, errors.New("FLAC changed since artwork snapshot")
	}
	if err := safeWriteTags(write, path, nonEmptyTags(tags), taglib.Clear); err != nil {
		return WriteResult{}, fmt.Errorf("write FLAC tags: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	actual, err := safeReadTags(read, path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("read back FLAC tags: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	expected := nonEmptyTags(tags)
	if !equalTagsExceptLyrics(actual, expected) {
		return WriteResult{}, errors.New("FLAC metadata tags did not round-trip")
	}
	after, err := SnapshotFlacArtwork(ctx, path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("verify FLAC after tag write: %w", err)
	}
	if !sameFlacArtworkContents(after, before) {
		return WriteResult{}, errors.New("FLAC artwork or audio changed during tag write; no restoration was attempted")
	}
	return WriteResult{Tags: cloneTags(actual)}, nil
}

func equalTagsExceptLyrics(actual, expected map[string][]string) bool {
	for k, v := range expected {
		if k != "LYRICS" {
			got, ok := actual[k]
			if !ok || !equalValues(got, v) {
				return false
			}
		}
	}
	for k, v := range actual {
		if k != "LYRICS" {
			want, ok := expected[k]
			if !ok || !equalValues(v, want) {
				return false
			}
		}
	}
	return true
}

func sameFlacArtworkContents(a, b FlacArtworkSnapshot) bool {
	return a.streamInfo == b.streamInfo && a.audioHash == b.audioHash && a.pictureHash == b.pictureHash && a.imageHash == b.imageHash && a.imageSize == b.imageSize && a.count == b.count && a.description == b.description && a.mime == b.mime && a.pictureType == b.pictureType
}

func sameFileSnapshot(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func flacPictureTypeName(t uint32) string {
	names := []string{"Other", "32x32 pixels 'file icon' (PNG only)", "Other file icon", "Front Cover", "Cover (back)", "Leaflet page", "Media (e.g. label side of CD)", "Lead artist/lead performer/soloist", "Artist/performer", "Conductor", "Band/Orchestra", "Composer", "Lyricist/text writer", "Recording Location", "During recording", "During performance", "Movie/video screen capture", "A bright coloured fish", "Illustration", "Band/artist logotype", "Publisher/Studio logotype"}
	if int(t) < len(names) {
		return names[t]
	}
	return ""
}

func parseFLACPicture(b []byte) (flacPicture, error) {
	bad := func() (flacPicture, error) { return flacPicture{}, errors.New("malformed FLAC PICTURE block") }
	i := 0
	read32 := func() (uint32, bool) {
		if len(b)-i < 4 {
			return 0, false
		}
		v := binary.BigEndian.Uint32(b[i : i+4])
		i += 4
		return v, true
	}
	t, ok := read32()
	if !ok {
		return bad()
	}
	mimeLen, ok := read32()
	if !ok || uint64(mimeLen) > uint64(len(b)-i) {
		return bad()
	}
	mime := string(b[i : i+int(mimeLen)])
	i += int(mimeLen)
	if mime == "-->" { // Linked-image PICTURE blocks are not supported.
		return bad()
	}
	descLen, ok := read32()
	if !ok || uint64(descLen) > uint64(len(b)-i) {
		return bad()
	}
	desc := string(b[i : i+int(descLen)])
	i += int(descLen)
	if len(b)-i < 16 {
		return bad()
	}
	if t != 3 || mime == "" {
		return bad()
	}
	i += 16
	imageLen, ok := read32()
	if !ok || imageLen == 0 || uint64(imageLen) != uint64(len(b)-i) {
		return bad()
	}
	return flacPicture{hash: sha256.Sum256(b), typ: t, mime: mime, description: desc}, nil
}

func hasPictureComment(b []byte) bool {
	found, err := parseVorbisComments(b)
	return err == nil && found
}

func parseVorbisComments(b []byte) (bool, error) {
	bad := func() (bool, error) { return false, errors.New("malformed FLAC Vorbis-comment block") }
	if len(b) < 8 {
		return bad()
	}
	i := 0
	readString := func() ([]byte, bool) {
		if len(b)-i < 4 {
			return nil, false
		}
		n := uint64(binary.LittleEndian.Uint32(b[i : i+4]))
		i += 4
		if n > uint64(len(b)-i) {
			return nil, false
		}
		out := b[i : i+int(n)]
		i += int(n)
		return out, true
	}
	vendor, ok := readString()
	if !ok || !utf8.Valid(vendor) || len(b)-i < 4 {
		return bad()
	}
	count := uint64(binary.LittleEndian.Uint32(b[i : i+4]))
	i += 4
	if count > uint64((len(b)-i)/4) {
		return bad()
	}
	found := false
	for range count {
		comment, ok := readString()
		if !ok || !utf8.Valid(comment) {
			return bad()
		}
		sep := -1
		for j, c := range comment {
			if c == '=' {
				sep = j
				break
			}
			if c < 0x20 || c > 0x7d {
				return bad()
			}
		}
		if sep <= 0 {
			return bad()
		}
		key := comment[:sep]
		if asciiEqualFold(key, "METADATA_BLOCK_PICTURE") || asciiEqualFold(key, "COVERART") || asciiEqualFold(key, "COVERARTMIME") {
			found = true
		}
	}
	if i != len(b) {
		return bad()
	}
	return found, nil
}

func asciiEqualFold(a []byte, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, c := range a {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c != b[i] {
			return false
		}
	}
	return true
}

func sumHash(h hash.Hash) (out [32]byte) { copy(out[:], h.Sum(nil)); return }
