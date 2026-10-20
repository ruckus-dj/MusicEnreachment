package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
)

// OptionalLyricsWriteResult contains detached tags from the final readback.
type OptionalLyricsWriteResult struct {
	Tags   map[string][]string
	Status OptionalLyricsWriteStatus
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
	defer func() {
		if recover() != nil {
			err = errors.New("metadata cover writer panicked")
		}
	}()
	return write(path, data)
}

func safeReadImage(read func(string) ([]byte, error), path string) (data []byte, err error) {
	defer func() {
		if recover() != nil {
			data = nil
			err = errors.New("metadata cover reader panicked")
		}
	}()
	return read(path)
}
