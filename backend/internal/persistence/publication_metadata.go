package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type PublicationCommitMetadata struct {
	Version int                 `json:"version"`
	Tags    map[string][]string `json:"tags"`
}

func strictMetadata(raw json.RawMessage, target *PublicationCommitMetadata) error {
	if target == nil || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("decode publication metadata: object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode publication metadata: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("decode publication metadata: trailing JSON")
	}
	if target.Tags == nil {
		return errors.New("decode publication metadata: tags must be an object")
	}
	for _, values := range target.Tags {
		if values == nil {
			return errors.New("decode publication metadata: tag values must be arrays")
		}
	}
	return nil
}
