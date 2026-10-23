package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"go.senan.xyz/taglib"
)

// WriteCoverOnly replaces embedded artwork without rewriting any native text
// tags. It verifies tags and audio properties both before and after mutation.
func (writer Writer) WriteCoverOnly(ctx context.Context, preparedPath string, cover *Cover) (OptionalAssetsWriteResult, error) {
	if err := ctx.Err(); err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	if preparedPath == "" || cover == nil {
		return OptionalAssetsWriteResult{}, errors.New("prepared metadata path and cover are required")
	}
	if err := validateOptionalCover(*cover); err != nil {
		return OptionalAssetsWriteResult{}, fmt.Errorf("validate requested publication cover: %w", err)
	}

	readTags := writer.readTags
	if readTags == nil {
		readTags = taglib.ReadTags
	}
	readProperties := func() (taglib.Properties, error) { return safeReadProperties(taglib.ReadProperties, preparedPath) }
	beforeTags, err := readOptionalAssetsTags(ctx, readTags, preparedPath)
	if err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	beforeProperties, err := readProperties()
	if contextErr := ctx.Err(); contextErr != nil {
		return OptionalAssetsWriteResult{}, contextErr
	}
	if err != nil {
		return OptionalAssetsWriteResult{}, fmt.Errorf("read candidate audio properties before cover rewrite: %w", err)
	}

	status, err := writer.writeCoverOnlyImage(ctx, preparedPath, *cover, len(beforeProperties.Images))
	if err != nil {
		return OptionalAssetsWriteResult{}, err
	}

	afterTags, err := readOptionalAssetsTags(ctx, readTags, preparedPath)
	if err != nil {
		return OptionalAssetsWriteResult{}, err
	}
	afterProperties, err := readProperties()
	if contextErr := ctx.Err(); contextErr != nil {
		return OptionalAssetsWriteResult{}, contextErr
	}
	if err != nil {
		return OptionalAssetsWriteResult{}, fmt.Errorf("read candidate audio properties after cover rewrite: %w", err)
	}
	if !equalTagMaps(beforeTags, afterTags) {
		return OptionalAssetsWriteResult{}, errors.New("cover-only rewrite changed metadata tags")
	}
	if !equalAudioProperties(beforeProperties, afterProperties) {
		return OptionalAssetsWriteResult{}, errors.New("cover-only rewrite changed audio properties")
	}

	if status == OptionalCoverVerified {
		if err := ctx.Err(); err != nil {
			return OptionalAssetsWriteResult{}, err
		}
		readImage := writer.readImage
		if readImage == nil {
			readImage = taglib.ReadImage
		}
		data, readErr, panicked := safeReadImageOutcome(readImage, preparedPath)
		if contextErr := ctx.Err(); contextErr != nil {
			return OptionalAssetsWriteResult{}, contextErr
		}
		if panicked {
			return OptionalAssetsWriteResult{}, errors.New("metadata cover reader panicked")
		}
		if readErr != nil {
			status = OptionalCoverReadFailed
		} else if !bytes.Equal(data, cover.Data) {
			status = OptionalCoverMismatch
		}
	}
	return OptionalAssetsWriteResult{Tags: cloneTags(afterTags), LyricsStatus: OptionalLyricsNotRequested, CoverStatus: status}, nil
}

func (writer Writer) writeCoverOnlyImage(ctx context.Context, path string, cover Cover, existingImages int) (OptionalCoverWriteStatus, error) {
	for range existingImages {
		if err := ctx.Err(); err != nil {
			return OptionalCoverNotRequested, err
		}
		if err, panicked := safeClearCoverArtworkImage(path); panicked {
			return OptionalCoverNotRequested, errors.New("metadata artwork writer panicked")
		} else if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return OptionalCoverNotRequested, contextErr
			}
			return OptionalCoverWriteFailed, nil
		}
	}
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
	return OptionalCoverVerified, nil
}

func safeClearCoverArtworkImage(path string) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			err = errors.New("metadata artwork writer panicked")
			panicked = true
		}
	}()
	return taglib.WriteImageOptions(path, nil, 0, "", "", ""), false
}

func equalAudioProperties(left, right taglib.Properties) bool {
	// Some containers report bitrate from total file size, including artwork.
	// Replacing an image changes that estimate without changing encoded audio.
	return left.Length == right.Length && left.Channels == right.Channels &&
		left.SampleRate == right.SampleRate && left.BitsPerSample == right.BitsPerSample && left.Codec == right.Codec
}
