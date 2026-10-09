package metadata

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/remko/go-mkvparse"
	"go.senan.xyz/taglib"
)

// Provenance identifies the library and public API contract used for capture.
type Provenance struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Contract string `json:"contract"`
}

// AudioProperties contains optional properties directly returned by TagLib.
// BitRateBitsPerSecond is converted from TagLib's kbit/s value.
type AudioProperties struct {
	Format               string        `json:"format"`
	InnerCodec           string        `json:"innerCodec"`
	Length               time.Duration `json:"length"`
	Channels             uint          `json:"channels"`
	SampleRateHz         uint          `json:"sampleRateHz"`
	BitRateBitsPerSecond uint64        `json:"bitRateBitsPerSecond"`
	BitDepth             *uint         `json:"bitDepth"`
}

// Capture is detached from the prepared path. Tags are normalized text values;
// this is not a promise that every raw or format-specific tag is represented.
type Capture struct {
	Format                        string              `json:"format"`
	Provenance                    Provenance          `json:"provenance"`
	Tags                          map[string][]string `json:"tags"`
	Properties                    *AudioProperties    `json:"properties,omitempty"`
	Matroska                      *MatroskaMetadata   `json:"matroska,omitempty"`
	MatroskaProjectionUnresolved  bool                `json:"matroskaProjectionUnresolved,omitempty"`
	MatroskaProjectionDiagnostics []string            `json:"matroskaProjectionDiagnostics,omitempty"`
}

// MatroskaMetadata preserves the native tag hierarchy, including targets,
// repeated tags, languages, nested SimpleTags, and binary values.
type MatroskaMetadata struct {
	Tracks []MatroskaTrack `json:"tracks"`
	Tags   []MatroskaTag   `json:"tags"`
}

type MatroskaTrack struct {
	UID  *int64 `json:"uid,omitempty"`
	Type *int64 `json:"type,omitempty"`
}

type MatroskaTargets struct {
	Type           string  `json:"type,omitempty"`
	TypeValue      *int64  `json:"typeValue,omitempty"`
	TrackUIDs      []int64 `json:"trackUIDs,omitempty"`
	EditionUIDs    []int64 `json:"editionUIDs,omitempty"`
	ChapterUIDs    []int64 `json:"chapterUIDs,omitempty"`
	AttachmentUIDs []int64 `json:"attachmentUIDs,omitempty"`
}

type MatroskaTag struct {
	Targets    MatroskaTargets     `json:"targets"`
	SimpleTags []MatroskaSimpleTag `json:"simpleTags"`
}

type MatroskaSimpleTag struct {
	Name         string              `json:"name"`
	Language     string              `json:"language,omitempty"`
	LanguageIETF string              `json:"languageIETF,omitempty"`
	Default      *int64              `json:"default,omitempty"`
	Value        *string             `json:"value,omitempty"`
	Binary       []string            `json:"binary,omitempty"`
	Children     []MatroskaSimpleTag `json:"children,omitempty"`
}

// Reader reads metadata from the already-prepared path supplied by its caller.
// It does not derive a path from source identity or retain a file/goroutine.
type Reader struct {
	readTags       func(string) (map[string][]string, error)
	readProperties func(string) (taglib.Properties, error)
	readMatroska   func(string) (*MatroskaMetadata, error)
}

// ReadRequest explicitly controls optional metadata work. Properties are not
// read unless the caller requests them; a requested property-read failure is
// optional and does not invalidate successfully read tags.
type ReadRequest struct {
	Properties bool
}

var tagLibProvenance = Provenance{
	Name: "go.senan.xyz/taglib", Version: "v0.14.0",
	Contract: "ReadTags(path) normalized text map; values/order as returned; no raw-tag completeness guarantee",
}

var mkvProvenance = Provenance{
	Name: "github.com/remko/go-mkvparse", Version: "v0.14.0",
	Contract: "event-based Matroska Tracks/Tags tree; projection is global plus sole-audio-track tags",
}

// Read captures metadata from a path that has already been prepared by the
// caller. Context is checked immediately before and after each library call;
// neither upstream parser offers context cancellation.
func (reader Reader) Read(ctx context.Context, preparedPath string, request ReadRequest) (Capture, error) {
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	if preparedPath == "" {
		return Capture{}, errors.New("prepared metadata path is required")
	}

	if isMatroska(preparedPath) {
		if err := ctx.Err(); err != nil {
			return Capture{}, err
		}
		read := reader.readMatroska
		if read == nil {
			read = readMatroska
		}
		metadata, err := safeReadMatroska(read, preparedPath)
		if contextErr := ctx.Err(); contextErr != nil {
			return Capture{}, contextErr
		}
		if err != nil {
			return Capture{}, fmt.Errorf("read Matroska tags: %w", err)
		}
		if metadata == nil {
			return Capture{}, errors.New("read Matroska tags: parser returned no metadata")
		}
		tags, diagnostics := projectMatroska(metadata)
		result := Capture{
			Format: "mka", Provenance: mkvProvenance, Tags: tags,
			Matroska: metadata, MatroskaProjectionUnresolved: len(diagnostics) > 0,
			MatroskaProjectionDiagnostics: diagnostics,
		}
		if err := reader.readOptionalProperties(ctx, preparedPath, request.Properties, &result); err != nil {
			return Capture{}, err
		}
		return result, nil
	}

	readTags := reader.readTags
	if readTags == nil {
		readTags = taglib.ReadTags
	}
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	tags, err := safeReadTags(readTags, preparedPath)
	if contextErr := ctx.Err(); contextErr != nil {
		return Capture{}, contextErr
	}
	if err != nil {
		return Capture{}, fmt.Errorf("read normalized metadata tags: %w", err)
	}
	ownedTags := cloneTags(tags)
	result := Capture{Provenance: tagLibProvenance, Tags: ownedTags}
	if err := reader.readOptionalProperties(ctx, preparedPath, request.Properties, &result); err != nil {
		return Capture{}, err
	}
	return result, nil
}

func (reader Reader) readOptionalProperties(ctx context.Context, path string, requested bool, result *Capture) error {
	if !requested {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	readProperties := reader.readProperties
	if readProperties == nil {
		readProperties = taglib.ReadProperties
	}
	properties, propertyErr := safeReadProperties(readProperties, path)
	if err := ctx.Err(); err != nil {
		return err
	}
	if propertyErr != nil || uint64(properties.BitRate) > ^uint64(0)/1000 {
		return nil
	}
	depth := properties.BitDepth
	var bitDepth *uint
	if depth > 0 {
		bitDepth = &depth
	}
	result.Properties = &AudioProperties{
		Format: properties.Format, InnerCodec: properties.InnerCodec, Length: properties.Length,
		Channels: properties.Channels, SampleRateHz: properties.SampleRate,
		BitRateBitsPerSecond: uint64(properties.BitRate) * 1000, BitDepth: bitDepth,
	}
	if result.Format == "" {
		result.Format = properties.Format
	}
	return nil
}

func safeReadTags(read func(string) (map[string][]string, error), path string) (tags map[string][]string, err error) {
	defer func() {
		if recover() != nil {
			tags = nil
			err = errors.New("metadata tag reader panicked")
		}
	}()
	return read(path)
}

func safeReadProperties(read func(string) (taglib.Properties, error), path string) (properties taglib.Properties, err error) {
	defer func() {
		if recover() != nil {
			properties = taglib.Properties{}
			err = errors.New("metadata properties reader panicked")
		}
	}()
	return read(path)
}

func safeReadMatroska(read func(string) (*MatroskaMetadata, error), path string) (metadata *MatroskaMetadata, err error) {
	defer func() {
		if recover() != nil {
			metadata = nil
			err = errors.New("matroska parser panicked")
		}
	}()
	return read(path)
}

func cloneTags(tags map[string][]string) map[string][]string {
	copyOfTags := make(map[string][]string, len(tags))
	for key, values := range tags {
		copyOfTags[key] = append([]string{}, values...)
	}
	return copyOfTags
}

func isMatroska(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	var header [4]byte
	_, err = file.Read(header[:])
	return err == nil && header == [4]byte{0x1a, 0x45, 0xdf, 0xa3}
}

func readMatroska(path string) (metadata *MatroskaMetadata, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	handler := &matroskaTagsHandler{}
	if err := mkvparse.Parse(file, handler); err != nil {
		return nil, err
	}
	return &MatroskaMetadata{Tracks: handler.tracks, Tags: handler.tags}, nil
}

type matroskaTagsHandler struct {
	tags       []MatroskaTag
	tracks     []MatroskaTrack
	currentTag *MatroskaTag
	track      *MatroskaTrack
	tagPath    []int
	active     bool
}

func (h *matroskaTagsHandler) HandleMasterBegin(id mkvparse.ElementID, _ mkvparse.ElementInfo) (bool, error) {
	switch id {
	case mkvparse.ClusterElement, mkvparse.AttachmentsElement, mkvparse.ChaptersElement, mkvparse.CuesElement:
		return false, nil
	case mkvparse.TagElement:
		h.currentTag = &MatroskaTag{SimpleTags: []MatroskaSimpleTag{}}
		h.tagPath = nil
		h.active = true
	case mkvparse.TrackEntryElement:
		h.track = &MatroskaTrack{}
	case mkvparse.SimpleTagElement:
		if h.active {
			value := MatroskaSimpleTag{}
			if len(h.tagPath) == 0 {
				h.currentTag.SimpleTags = append(h.currentTag.SimpleTags, value)
				h.tagPath = append(h.tagPath, len(h.currentTag.SimpleTags)-1)
			} else {
				parent := h.currentSimpleTag()
				parent.Children = append(parent.Children, value)
				h.tagPath = append(h.tagPath, len(parent.Children)-1)
			}
		}
	}
	return true, nil
}

func (h *matroskaTagsHandler) HandleMasterEnd(id mkvparse.ElementID, _ mkvparse.ElementInfo) error {
	switch id {
	case mkvparse.SimpleTagElement:
		if h.active && len(h.tagPath) > 0 {
			h.tagPath = h.tagPath[:len(h.tagPath)-1]
		}
	case mkvparse.TagElement:
		if h.active && h.currentTag != nil {
			h.tags = append(h.tags, *h.currentTag)
		}
		h.currentTag = nil
		h.tagPath = nil
		h.active = false
	case mkvparse.TrackEntryElement:
		if h.track != nil {
			h.tracks = append(h.tracks, *h.track)
		}
		h.track = nil
	}
	return nil
}

func (h *matroskaTagsHandler) HandleString(id mkvparse.ElementID, value string, _ mkvparse.ElementInfo) error {
	if !h.active {
		return nil
	}
	switch id {
	case mkvparse.TargetTypeElement:
		h.currentTag.Targets.Type = value
	case mkvparse.TagNameElement:
		if current := h.currentSimpleTag(); current != nil {
			current.Name = value
		}
	case mkvparse.TagLanguageElement:
		if current := h.currentSimpleTag(); current != nil {
			current.Language = value
		}
	case mkvparse.TagLanguageIETFElement:
		if current := h.currentSimpleTag(); current != nil {
			current.LanguageIETF = value
		}
	case mkvparse.TagStringElement:
		if current := h.currentSimpleTag(); current != nil {
			copyValue := value
			current.Value = &copyValue
		}
	}
	return nil
}

func (h *matroskaTagsHandler) HandleInteger(id mkvparse.ElementID, value int64, _ mkvparse.ElementInfo) error {
	if h.track != nil {
		switch id {
		case mkvparse.TrackUIDElement:
			h.track.UID = int64Pointer(value)
		case mkvparse.TrackTypeElement:
			h.track.Type = int64Pointer(value)
		}
	}
	if !h.active {
		return nil
	}
	switch id {
	case mkvparse.TargetTypeValueElement:
		h.currentTag.Targets.TypeValue = int64Pointer(value)
	case mkvparse.TagTrackUIDElement:
		h.currentTag.Targets.TrackUIDs = append(h.currentTag.Targets.TrackUIDs, value)
	case mkvparse.TagEditionUIDElement:
		h.currentTag.Targets.EditionUIDs = append(h.currentTag.Targets.EditionUIDs, value)
	case mkvparse.TagChapterUIDElement:
		h.currentTag.Targets.ChapterUIDs = append(h.currentTag.Targets.ChapterUIDs, value)
	case mkvparse.TagAttachmentUIDElement:
		h.currentTag.Targets.AttachmentUIDs = append(h.currentTag.Targets.AttachmentUIDs, value)
	case mkvparse.TagDefaultElement:
		if current := h.currentSimpleTag(); current != nil {
			current.Default = int64Pointer(value)
		}
	}
	return nil
}

func (h *matroskaTagsHandler) HandleBinary(id mkvparse.ElementID, value []byte, _ mkvparse.ElementInfo) error {
	if h.active && id == mkvparse.TagBinaryElement {
		if current := h.currentSimpleTag(); current != nil {
			current.Binary = append(current.Binary, base64.StdEncoding.EncodeToString(append([]byte(nil), value...)))
		}
	}
	return nil
}

func (*matroskaTagsHandler) HandleFloat(mkvparse.ElementID, float64, mkvparse.ElementInfo) error {
	return nil
}
func (*matroskaTagsHandler) HandleDate(mkvparse.ElementID, time.Time, mkvparse.ElementInfo) error {
	return nil
}
func (h *matroskaTagsHandler) currentSimpleTag() *MatroskaSimpleTag {
	if len(h.tagPath) == 0 || h.currentTag == nil {
		return nil
	}
	current := &h.currentTag.SimpleTags[h.tagPath[0]]
	for _, index := range h.tagPath[1:] {
		current = &current.Children[index]
	}
	return current
}
func int64Pointer(value int64) *int64 { return &value }

func projectMatroska(metadata *MatroskaMetadata) (map[string][]string, []string) {
	result := map[string][]string{}
	for _, tag := range metadata.Tags {
		if !hasTargets(tag.Targets) {
			projectMatroskaTag(result, tag)
		}
	}
	if !hasScopedTags(metadata) {
		return result, nil
	}

	var audioTracks []MatroskaTrack
	for _, track := range metadata.Tracks {
		if track.Type != nil && *track.Type == 2 {
			audioTracks = append(audioTracks, track)
		}
	}

	diagnostics := make([]string, 0)
	if len(audioTracks) > 1 && hasTrackScopedTags(metadata) {
		diagnostics = append(diagnostics, "multiple audio tracks; track-scoped tags were not projected")
	}
	for _, tag := range metadata.Tags {
		if !hasTargets(tag.Targets) {
			continue
		}
		if isTrackOnlyTarget(tag.Targets) {
			if len(audioTracks) == 1 && audioTracks[0].UID != nil &&
				(isSoleTrackTarget(tag.Targets, *audioTracks[0].UID) || isAllTracksTarget(tag.Targets)) {
				projectMatroskaTag(result, tag)
				continue
			}
			if len(audioTracks) > 1 {
				continue
			}
			if !trackUIDExists(metadata.Tracks, tag.Targets.TrackUIDs[0]) {
				diagnostics = append(diagnostics, "track-scoped tags reference an unknown track UID")
			} else {
				diagnostics = append(diagnostics, "track-scoped tags do not target the unique audio track")
			}
			continue
		}
		diagnostics = append(diagnostics, "mixed or non-track scoped tags were not projected")
	}
	if len(audioTracks) == 1 && audioTracks[0].UID == nil && hasTrackScopedTags(metadata) {
		diagnostics = append(diagnostics, "audio track has no UID; track-scoped tags were not projected")
	}
	return result, diagnostics
}

func projectMatroskaTag(target map[string][]string, tag MatroskaTag) {
	for _, simple := range tag.SimpleTags {
		projectSimpleTag(target, simple)
	}
}

func projectSimpleTag(target map[string][]string, simple MatroskaSimpleTag) {
	if simple.Value != nil && simple.Name != "" {
		key := strings.ToUpper(simple.Name)
		target[key] = append(target[key], *simple.Value)
	}
	for _, child := range simple.Children {
		projectSimpleTag(target, child)
	}
}

func hasTargets(targets MatroskaTargets) bool {
	return len(targets.TrackUIDs)+len(targets.EditionUIDs)+len(targets.ChapterUIDs)+len(targets.AttachmentUIDs) > 0
}

func hasScopedTags(metadata *MatroskaMetadata) bool {
	for _, tag := range metadata.Tags {
		if hasTargets(tag.Targets) {
			return true
		}
	}
	return false
}

func hasTrackScopedTags(metadata *MatroskaMetadata) bool {
	for _, tag := range metadata.Tags {
		if len(tag.Targets.TrackUIDs) > 0 {
			return true
		}
	}
	return false
}

func isTrackOnlyTarget(targets MatroskaTargets) bool {
	return len(targets.TrackUIDs) > 0 && len(targets.EditionUIDs) == 0 && len(targets.ChapterUIDs) == 0 && len(targets.AttachmentUIDs) == 0
}

func isSoleTrackTarget(targets MatroskaTargets, uid int64) bool {
	return isTrackOnlyTarget(targets) && len(targets.TrackUIDs) == 1 && targets.TrackUIDs[0] == uid
}

// isAllTracksTarget reports whether a track-only target addresses every track
// through the TagTrackUID 0 wildcard defined by the Matroska specification.
func isAllTracksTarget(targets MatroskaTargets) bool {
	return isTrackOnlyTarget(targets) && len(targets.TrackUIDs) == 1 && targets.TrackUIDs[0] == 0
}

func trackUIDExists(tracks []MatroskaTrack, uid int64) bool {
	for _, track := range tracks {
		if track.UID != nil && *track.UID == uid {
			return true
		}
	}
	return false
}
