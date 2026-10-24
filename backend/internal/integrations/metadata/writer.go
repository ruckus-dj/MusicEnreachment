package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
	"go.senan.xyz/taglib"
)

// Cover is an optional requested cover image. MIME is checked against the
// image bytes; TagLib determines the container-specific storage encoding.
type Cover struct {
	Data []byte
	MIME string
}

// CoverObservation describes the independently reopened cover after writing.
// Error is populated when a requested cover could not be written/read back.
type CoverObservation struct {
	MIME  string
	Data  []byte
	Error string
}

// WriteResult contains detached, freshly reopened observations. A non-nil
// result with an error can indicate optional cover failure; callers must not
// treat that staged file as a complete success.
type WriteResult struct {
	Tags  map[string][]string
	Cover *CoverObservation
}

// OptionalLyricsWriteStatus describes only the optional LYRICS readback.
type OptionalLyricsWriteStatus uint8

const (
	OptionalLyricsUnobserved OptionalLyricsWriteStatus = iota
	OptionalLyricsVerified
	OptionalLyricsAbsent
	OptionalLyricsMismatch
	OptionalLyricsNotRequested
)

// OptionalLyricsWriteResult contains detached tags from the final readback.
type OptionalLyricsWriteResult struct {
	Tags   map[string][]string
	Status OptionalLyricsWriteStatus
}

// OptionalCoverWriteStatus is a typed result for the isolated optional image
// mutation. It is meaningful only when WriteWithOptionalAssets returns nil error.
type OptionalCoverWriteStatus uint8

const (
	OptionalCoverNotRequested OptionalCoverWriteStatus = iota
	OptionalCoverVerified
	OptionalCoverWriteFailed
	OptionalCoverReadFailed
	OptionalCoverMismatch
)

// OptionalAssetsWriteResult reports final fresh-read tags and independent
// outcomes for the optional lyrics and cover on a first-audio candidate.
type OptionalAssetsWriteResult struct {
	Tags         map[string][]string
	LyricsStatus OptionalLyricsWriteStatus
	CoverStatus  OptionalCoverWriteStatus
}

// Writer writes only the owned, already-prepared staged path provided by its
// caller. TagLib edits that file in place (not atomically); it never derives a
// destination from or modifies an original source path.
type Writer struct {
	writeTags  func(string, map[string][]string, taglib.WriteOption) error
	readTags   func(string) (map[string][]string, error)
	writeImage func(string, []byte) error
	readImage  func(string) ([]byte, error)
}

// Write clears normalized text tags, writes the requested normalized map, and
// verifies it with a fresh TagLib read. Empty values are omitted; a requested
// key with no non-empty value is an explicit deletion and must be absent, not
// merely empty, after the write. Cover data is optional and independently
// observed; a requested cover failure is returned as an error, never hidden.
func (writer Writer) Write(ctx context.Context, preparedPath string, requested map[string][]string, cover *Cover) (WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	if preparedPath == "" {
		return WriteResult{}, errors.New("prepared metadata path is required")
	}
	deletions := emptyTagKeys(requested)
	expected := nonEmptyTags(requested)
	writeTags := writer.writeTags
	if writeTags == nil {
		writeTags = taglib.WriteTags
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	err := safeWriteTags(writeTags, preparedPath, expected, taglib.Clear)
	if contextErr := ctx.Err(); contextErr != nil {
		return WriteResult{}, contextErr
	}
	if err != nil {
		return WriteResult{}, fmt.Errorf("write normalized metadata tags: %w", err)
	}

	readTags := writer.readTags
	if readTags == nil {
		readTags = taglib.ReadTags
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	actual, err := safeReadTags(readTags, preparedPath)
	if contextErr := ctx.Err(); contextErr != nil {
		return WriteResult{}, contextErr
	}
	if err != nil {
		return WriteResult{}, fmt.Errorf("read back normalized metadata tags: %w", err)
	}
	for key, values := range expected {
		if got, ok := actual[key]; !ok || !equalValues(got, values) {
			return WriteResult{}, fmt.Errorf("metadata tag %q did not round-trip", key)
		}
	}
	for _, key := range deletions {
		if _, present := actual[key]; present {
			return WriteResult{}, fmt.Errorf("metadata tag %q was not deleted", key)
		}
	}
	result := WriteResult{Tags: cloneTags(actual)}
	if cover == nil {
		return result, nil
	}
	observation, coverErr := writer.writeAndObserveCover(ctx, preparedPath, *cover)
	result.Cover = observation
	if coverErr != nil {
		return result, coverErr
	}
	return result, nil
}

// WriteWithOptionalLyrics writes normalized tags and treats only LYRICS
// readback mismatch as an optional outcome. All other write, readback, and
// cover failures remain fatal. The returned tags are from a fresh read after
// every requested mutation has completed.
func (writer Writer) WriteWithOptionalLyrics(ctx context.Context, preparedPath string, requested map[string][]string, cover *Cover) (OptionalLyricsWriteResult, error) {
	if err := ctx.Err(); err != nil {
		return OptionalLyricsWriteResult{}, err
	}
	if preparedPath == "" {
		return OptionalLyricsWriteResult{}, errors.New("prepared metadata path is required")
	}
	lyrics, ok := requested["LYRICS"]
	if !ok || len(lyrics) != 1 || strings.TrimSpace(lyrics[0]) == "" {
		return OptionalLyricsWriteResult{}, errors.New("optional lyrics must contain one non-empty text value")
	}
	expected := nonEmptyTags(requested)
	delete(expected, "LYRICS")
	writeTags := writer.writeTags
	if writeTags == nil {
		writeTags = taglib.WriteTags
	}
	if err := ctx.Err(); err != nil {
		return OptionalLyricsWriteResult{}, err
	}
	if err := safeWriteTags(writeTags, preparedPath, nonEmptyTags(requested), taglib.Clear); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return OptionalLyricsWriteResult{}, contextErr
		}
		return OptionalLyricsWriteResult{}, fmt.Errorf("write normalized metadata tags: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return OptionalLyricsWriteResult{}, err
	}
	if cover != nil {
		if _, err := writer.writeAndObserveCover(ctx, preparedPath, *cover); err != nil {
			return OptionalLyricsWriteResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return OptionalLyricsWriteResult{}, err
	}
	readTags := writer.readTags
	if readTags == nil {
		readTags = taglib.ReadTags
	}
	actual, err := safeReadTags(readTags, preparedPath)
	if contextErr := ctx.Err(); contextErr != nil {
		return OptionalLyricsWriteResult{}, contextErr
	}
	if err != nil {
		return OptionalLyricsWriteResult{}, fmt.Errorf("read back normalized metadata tags: %w", err)
	}
	actualMandatory := cloneTags(actual)
	delete(actualMandatory, "LYRICS")
	if !equalTagMaps(actualMandatory, expected) {
		return OptionalLyricsWriteResult{}, errors.New("required metadata tags did not round-trip")
	}
	status := OptionalLyricsMismatch
	gotLyrics, present := actual["LYRICS"]
	if !present {
		status = OptionalLyricsAbsent
	} else if equalValues(gotLyrics, lyrics) {
		status = OptionalLyricsVerified
	}
	return OptionalLyricsWriteResult{Tags: cloneTags(actual), Status: status}, nil
}

// WriteWithOptionalAssets writes mandatory tags and any requested optional LYRICS
// and cover to the caller-owned first-audio candidate. Only normally returned
// failures from the isolated cover write/readback are represented as statuses.
// Every non-nil error is fatal, and no status in a non-nil result grants recovery
// authority when an error is returned.
func (writer Writer) WriteWithOptionalAssets(ctx context.Context, preparedPath string, requested map[string][]string, cover *Cover) (OptionalAssetsWriteResult, error) {
	if err := ctx.Err(); err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	if preparedPath == "" {
		return OptionalAssetsWriteResult{}, errors.New("prepared metadata path is required")
	}
	lyrics, lyricsRequested := requested["LYRICS"]
	if lyricsRequested && (len(lyrics) != 1 || strings.TrimSpace(lyrics[0]) == "") {
		return OptionalAssetsWriteResult{}, errors.New("optional lyrics must contain one non-empty text value")
	}
	if cover != nil {
		if err := validateOptionalCover(*cover); err != nil {
			return OptionalAssetsWriteResult{}, fmt.Errorf("validate requested publication cover: %w", err)
		}
	}

	expected := nonEmptyTags(requested)
	mandatory := cloneTags(expected)
	delete(mandatory, "LYRICS")
	writeTags := writer.writeTags
	if writeTags == nil {
		writeTags = taglib.WriteTags
	}
	if err := ctx.Err(); err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	if err := safeWriteTags(writeTags, preparedPath, expected, taglib.Clear); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return OptionalAssetsWriteResult{}, contextErr
		}
		return OptionalAssetsWriteResult{}, fmt.Errorf("write normalized metadata tags: %w", err)
	}
	readTags := writer.readTags
	if readTags == nil {
		readTags = taglib.ReadTags
	}
	initialTags, err := readOptionalAssetsTags(ctx, readTags, preparedPath)
	if err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	if err := validateOptionalAssetsMandatoryTags(initialTags, mandatory, requested); err != nil {
		return OptionalAssetsWriteResult{}, err
	}

	result := OptionalAssetsWriteResult{LyricsStatus: OptionalLyricsNotRequested, CoverStatus: OptionalCoverNotRequested}
	if lyricsRequested {
		result.LyricsStatus = classifyOptionalLyrics(initialTags, lyrics)
	}
	if cover != nil {
		result.CoverStatus, err = writer.writeOptionalCover(ctx, preparedPath, *cover)
		if err != nil {
			return OptionalAssetsWriteResult{}, err
		}
	}
	finalTags, err := readOptionalAssetsTags(ctx, readTags, preparedPath)
	if err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	if err := validateOptionalAssetsMandatoryTags(finalTags, mandatory, requested); err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	if lyricsRequested {
		result.LyricsStatus = classifyOptionalLyrics(finalTags, lyrics)
	}
	result.Tags = cloneTags(finalTags)
	return result, nil
}

func validateOptionalCover(cover Cover) error {
	if len(cover.Data) == 0 {
		return errors.New("requested cover image is empty")
	}
	format, _, _, err := artwork.Inspect(cover.Data)
	if err != nil {
		return errors.New("requested cover image is not decodable")
	}
	wantFormat := ""
	switch cover.MIME {
	case "image/png":
		wantFormat = "png"
	case "image/jpeg":
		wantFormat = "jpeg"
	default:
		return errors.New("requested cover MIME is unsupported")
	}
	if format != wantFormat || http.DetectContentType(cover.Data) != cover.MIME {
		return errors.New("requested cover MIME does not match image bytes")
	}
	return nil
}

func (writer Writer) writeOptionalCover(ctx context.Context, path string, cover Cover) (OptionalCoverWriteStatus, error) {
	if err := ctx.Err(); err != nil {
		return OptionalCoverNotRequested, err
	}
	writeImage := writer.writeImage
	if writeImage == nil {
		writeImage = taglib.WriteImage
	}
	writeErr, panicked := safeWriteImageOutcome(writeImage, path, cover.Data)
	if contextErr := ctx.Err(); contextErr != nil {
		return OptionalCoverNotRequested, contextErr
	}
	if panicked {
		return OptionalCoverNotRequested, errors.New("metadata cover writer panicked")
	}
	if writeErr != nil {
		return OptionalCoverWriteFailed, nil
	}
	if err := ctx.Err(); err != nil {
		return OptionalCoverNotRequested, err
	}
	readImage := writer.readImage
	if readImage == nil {
		readImage = taglib.ReadImage
	}
	data, readErr, panicked := safeReadImageOutcome(readImage, path)
	if contextErr := ctx.Err(); contextErr != nil {
		return OptionalCoverNotRequested, contextErr
	}
	if panicked {
		return OptionalCoverNotRequested, errors.New("metadata cover reader panicked")
	}
	if readErr != nil {
		return OptionalCoverReadFailed, nil
	}
	if !bytes.Equal(data, cover.Data) {
		return OptionalCoverMismatch, nil
	}
	return OptionalCoverVerified, nil
}

func readOptionalAssetsTags(ctx context.Context, read func(string) (map[string][]string, error), path string) (map[string][]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	actual, err := safeReadTags(read, path)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, fmt.Errorf("read back normalized metadata tags: %w", err)
	}
	return actual, nil
}

func validateOptionalAssetsMandatoryTags(actual, mandatory, requested map[string][]string) error {
	actualMandatory := cloneTags(actual)
	delete(actualMandatory, "LYRICS")
	if !equalTagMaps(actualMandatory, mandatory) {
		return errors.New("required metadata tags did not round-trip")
	}
	for _, key := range emptyTagKeys(requested) {
		if key == "LYRICS" {
			continue
		}
		if _, present := actual[key]; present {
			return fmt.Errorf("metadata tag %q was not deleted", key)
		}
	}
	return nil
}

func classifyOptionalLyrics(actual map[string][]string, requested []string) OptionalLyricsWriteStatus {
	got, present := actual["LYRICS"]
	if !present {
		return OptionalLyricsAbsent
	}
	if equalValues(got, requested) {
		return OptionalLyricsVerified
	}
	return OptionalLyricsMismatch
}

func equalTagMaps(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, values := range right {
		got, ok := left[key]
		if !ok || !equalValues(got, values) {
			return false
		}
	}
	return true
}

func (writer Writer) writeAndObserveCover(ctx context.Context, path string, cover Cover) (*CoverObservation, error) {
	fail := func(err error) (*CoverObservation, error) {
		return &CoverObservation{Error: err.Error()}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mime := http.DetectContentType(cover.Data)
	if (mime != "image/png" && mime != "image/jpeg") || cover.MIME != mime {
		return fail(errors.New("requested cover MIME does not match supported image bytes"))
	}
	writeImage := writer.writeImage
	if writeImage == nil {
		writeImage = taglib.WriteImage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	err := safeWriteImage(writeImage, path, cover.Data)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return fail(fmt.Errorf("write metadata cover: %w", err))
	}
	readImage := writer.readImage
	if readImage == nil {
		readImage = taglib.ReadImage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := safeReadImage(readImage, path)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return fail(fmt.Errorf("read back metadata cover: %w", err))
	}
	if !bytes.Equal(data, cover.Data) {
		return fail(errors.New("metadata cover did not round-trip"))
	}
	return &CoverObservation{MIME: mime, Data: append([]byte(nil), data...)}, nil
}

// emptyTagKeys reports explicitly requested deletions: keys whose requested
// values are all empty. Under Clear semantics those keys must be absent after
// the write, so they are collected before nonEmptyTags drops them.
func emptyTagKeys(tags map[string][]string) []string {
	deletions := make([]string, 0)
	for key, values := range tags {
		empty := true
		for _, value := range values {
			if value != "" {
				empty = false
				break
			}
		}
		if empty {
			deletions = append(deletions, key)
		}
	}
	return deletions
}

func nonEmptyTags(tags map[string][]string) map[string][]string {
	filtered := make(map[string][]string, len(tags))
	for key, values := range tags {
		kept := make([]string, 0, len(values))
		for _, value := range values {
			if value != "" {
				kept = append(kept, value)
			}
		}
		if len(kept) != 0 {
			filtered[key] = kept
		}
	}
	return filtered
}

func equalValues(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func safeWriteTags(write func(string, map[string][]string, taglib.WriteOption) error, path string, tags map[string][]string, options taglib.WriteOption) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("metadata tag writer panicked")
		}
	}()
	return write(path, tags, options)
}

func safeWriteImage(write func(string, []byte) error, path string, data []byte) (err error) {
	err, _ = safeWriteImageOutcome(write, path, data)
	return err
}

func safeWriteImageOutcome(write func(string, []byte) error, path string, data []byte) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			err = errors.New("metadata cover writer panicked")
			panicked = true
		}
	}()
	err = write(path, data)
	return err, false
}

func safeReadImage(read func(string) ([]byte, error), path string) (data []byte, err error) {
	data, err, _ = safeReadImageOutcome(read, path)
	return data, err
}

func safeReadImageOutcome(read func(string) ([]byte, error), path string) (data []byte, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			data = nil
			err = errors.New("metadata cover reader panicked")
			panicked = true
		}
	}()
	data, err = read(path)
	return data, err, false
}
