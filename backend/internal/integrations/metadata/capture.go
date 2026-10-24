package metadata

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artwork"
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
	Format          string              `json:"format"`
	Provenance      Provenance          `json:"provenance"`
	Tags            map[string][]string `json:"tags"`
	Properties      *AudioProperties    `json:"properties,omitempty"`
	CoverInspection *CoverInspection    `json:"coverInspection,omitempty"`
}

// CoverInspection reports the first normalized picture returned by TagLib.
// It is not an exhaustive inventory: an observed nil cover can mean the first
// normalized picture was empty, and does not prove that no raw image exists or
// that the picture is a front cover or unique. Error reports a rejected
// oversized image while leaving the rest of metadata capture successful.
type CoverInspection struct {
	Observed bool   `json:"observed"`
	Cover    *Cover `json:"cover,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Reader reads metadata from the already-prepared path supplied by its caller.
// It does not derive a path from source identity or retain a file/goroutine.
type Reader struct {
	readTags       func(string) (map[string][]string, error)
	readProperties func(string) (taglib.Properties, error)
	readImage      func(string) ([]byte, error)
}

// ReadRequest explicitly controls optional metadata work. Properties are not
// read unless the caller requests them; a requested property-read failure is
// optional and does not invalidate successfully read tags.
type ReadRequest struct {
	Properties      bool
	CoverInspection bool
	// LimitSourceCover applies new-ingestion policy, not CURRENT observation.
	LimitSourceCover bool
}

var tagLibProvenance = Provenance{
	Name:     "github.com/deluan/go-taglib",
	Version:  "v0.0.0-20260926163408-32d7614f30ec (TagLib v2.3.2)",
	Contract: "ReadTags(path) normalized text map; optional ReadProperties(path) audio properties and ReadImage(path) first normalized picture; values/order as returned; no raw-tag completeness guarantee",
}

// Read captures normalized metadata from the path prepared by its caller.
// Context is checked immediately before and after each library call; TagLib does
// not offer context cancellation.
func (reader Reader) Read(ctx context.Context, preparedPath string, request ReadRequest) (Capture, error) {
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	if preparedPath == "" {
		return Capture{}, errors.New("prepared metadata path is required")
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
	result := Capture{
		Provenance: tagLibProvenance, Tags: cloneTags(tags),
	}
	if err := reader.readOptionalProperties(ctx, preparedPath, request.Properties, &result); err != nil {
		return Capture{}, err
	}
	if err := reader.readCoverInspection(ctx, preparedPath, request.CoverInspection, request.LimitSourceCover, &result); err != nil {
		return Capture{}, err
	}
	return result, nil
}

func (reader Reader) readCoverInspection(ctx context.Context, path string, requested, limitSource bool, result *Capture) error {
	if !requested {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Read is reached only after ReadTags has validated the prepared path. The
	// separately opened image read is still subject to a same-path external race.
	readImage := reader.readImage
	if readImage == nil {
		readImage = taglib.ReadImage
	}
	data, readErr := safeReadImage(readImage, path)
	if err := ctx.Err(); err != nil {
		return err
	}
	if readErr != nil {
		return fmt.Errorf("inspect metadata cover: %w", readErr)
	}
	inspection := &CoverInspection{Observed: true}
	if len(data) != 0 {
		if _, _, _, err := artwork.Inspect(data); !limitSource || !errors.Is(err, artwork.ErrDimensionsExceeded) {
			inspection.Cover = &Cover{Data: append([]byte(nil), data...), MIME: coverMIME(data)}
		} else {
			inspection.Error = err.Error()
		}
	}
	result.CoverInspection = inspection
	return nil
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
	if propertyErr != nil || uint64(properties.Bitrate) > math.MaxUint64/1000 {
		return nil
	}
	format, formatErr := safeReadFormat(path)
	if err := ctx.Err(); err != nil {
		return err
	}
	if formatErr != nil {
		return nil
	}
	result.Format = format
	depth := properties.BitsPerSample
	var bitDepth *uint
	if depth > 0 {
		bitDepth = &depth
	}
	result.Properties = &AudioProperties{
		Format: result.Format, InnerCodec: properties.Codec, Length: properties.Length,
		Channels: properties.Channels, SampleRateHz: properties.SampleRate,
		BitRateBitsPerSecond: uint64(properties.Bitrate) * 1000, BitDepth: bitDepth,
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

func cloneTags(tags map[string][]string) map[string][]string {
	copyOfTags := make(map[string][]string, len(tags))
	for key, values := range tags {
		copyOfTags[key] = append([]string{}, values...)
	}
	return copyOfTags
}

func safeReadFormat(path string) (format string, err error) {
	defer func() {
		if recover() != nil {
			format = ""
			err = errors.New("metadata format reader panicked")
		}
	}()
	file, err := taglib.OpenReadOnly(path)
	if err != nil {
		return "", err
	}
	if detected := file.Format(); detected != taglib.FormatUnknown {
		format = detected.String()
	}
	return format, file.Close()
}

// coverMIME classifies cover bytes without filesystem metadata. net/http does
// not recognize TIFF, so its reliable byte-order/magic signature is checked
// before falling back to HTTP sniffing; unknown formats keep their exact bytes.
func coverMIME(data []byte) string {
	if isTIFF(data) {
		return "image/tiff"
	}
	return http.DetectContentType(data)
}

func isTIFF(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	var order binary.ByteOrder
	switch {
	case data[0] == 'I' && data[1] == 'I':
		order = binary.LittleEndian
	case data[0] == 'M' && data[1] == 'M':
		order = binary.BigEndian
	default:
		return false
	}
	switch order.Uint16(data[2:4]) {
	case 42:
		return true
	case 43:
		// BigTIFF carries an 8-byte offset size after its 43 magic.
		return len(data) >= 16 && order.Uint16(data[4:6]) == 8
	default:
		return false
	}
}
