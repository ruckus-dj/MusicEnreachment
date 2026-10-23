package metadata

import (
	"context"
	"errors"
	"fmt"

	"go.senan.xyz/taglib"
)

// ClearArtwork removes every embedded image from a private metadata candidate.
// TagLib does not offer context cancellation, so the context is checked around
// each native call. Callers must only pass a candidate they own and may safely
// modify.
func ClearArtwork(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" {
		return errors.New("artwork candidate path is required")
	}

	properties, err := safeReadProperties(taglib.ReadProperties, path)
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if err != nil {
		return fmt.Errorf("read candidate artwork properties: %w", err)
	}
	for range properties.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := safeClearArtworkImage(path); err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			return fmt.Errorf("clear candidate artwork: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	properties, err = safeReadProperties(taglib.ReadProperties, path)
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if err != nil {
		return fmt.Errorf("verify cleared candidate artwork: %w", err)
	}
	if len(properties.Images) != 0 {
		return fmt.Errorf("candidate artwork remains after clearing: %d images", len(properties.Images))
	}
	return nil
}

func safeClearArtworkImage(path string) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("metadata artwork writer panicked")
		}
	}()
	return taglib.WriteImageOptions(path, nil, 0, "", "", "")
}
