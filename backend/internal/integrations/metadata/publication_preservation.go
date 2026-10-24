package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"go.senan.xyz/taglib"
)

// ArtworkSnapshot is a detached, ordered copy of every embedded picture and
// its native descriptor. Data is opaque: preservation does not decode it.
type ArtworkSnapshot struct {
	Images []ArtworkSnapshotImage
}

// ArtworkSnapshotImage describes one picture at its original index.
type ArtworkSnapshotImage struct {
	Index       int
	Type        string
	Description string
	MIME        string
	Data        []byte
}

// ArtworkInventory returns descriptor and content witnesses without exposing
// image bytes to frozen metadata records.
func (snapshot ArtworkSnapshot) ArtworkInventory() []ArtworkInventoryEntry {
	entries := make([]ArtworkInventoryEntry, len(snapshot.Images))
	for i, image := range snapshot.Images {
		entries[i] = ArtworkInventoryEntry{
			Index: image.Index, Type: image.Type, Description: image.Description,
			MIME: image.MIME, Size: len(image.Data), SHA256: sha256.Sum256(image.Data),
		}
	}
	return entries
}

// ArtworkInventoryEntry is the stable descriptor/hash/size witness for an
// artwork image; it intentionally excludes encoded image bytes.
type ArtworkInventoryEntry struct {
	Index       int
	Type        string
	Description string
	MIME        string
	Size        int
	SHA256      [sha256.Size]byte
}

// ReadArtworkSnapshot reads every image through TagLib without imposing source
// ingestion limits or decoding image pixels.
func ReadArtworkSnapshot(ctx context.Context, path string) (ArtworkSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ArtworkSnapshot{}, err
	}
	if path == "" {
		return ArtworkSnapshot{}, errors.New("prepared metadata path is required")
	}
	file, err := taglib.OpenReadOnly(path)
	if err != nil {
		return ArtworkSnapshot{}, fmt.Errorf("open metadata artwork: %w", err)
	}
	snapshot := ArtworkSnapshot{}
	properties := file.Properties()
	snapshot.Images = make([]ArtworkSnapshotImage, 0, len(properties.Images))
	for index, descriptor := range properties.Images {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return ArtworkSnapshot{}, err
		}
		data, readErr := file.Image(index)
		if readErr != nil {
			_ = file.Close()
			return ArtworkSnapshot{}, fmt.Errorf("read metadata artwork %d: %w", index, readErr)
		}
		snapshot.Images = append(snapshot.Images, ArtworkSnapshotImage{
			Index: index, Type: descriptor.Type, Description: descriptor.Description,
			MIME: descriptor.MIMEType, Data: append([]byte(nil), data...),
		})
	}
	if err := file.Close(); err != nil {
		return ArtworkSnapshot{}, fmt.Errorf("close metadata artwork reader: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ArtworkSnapshot{}, err
	}
	return snapshot, nil
}

// RestoreArtworkSnapshot replaces all candidate artwork with the exact ordered
// snapshot and requires a complete descriptor and byte-for-byte readback.
func RestoreArtworkSnapshot(ctx context.Context, path string, snapshot ArtworkSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" {
		return errors.New("prepared metadata path is required")
	}
	for index, image := range snapshot.Images {
		if image.Index != index {
			return fmt.Errorf("artwork snapshot index %d is out of order", image.Index)
		}
	}
	properties, err := taglib.ReadProperties(path)
	if err != nil {
		return fmt.Errorf("read candidate artwork properties: %w", err)
	}
	for range properties.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := taglib.WriteImageOptions(path, nil, 0, "", "", ""); err != nil {
			return fmt.Errorf("clear candidate artwork: %w", err)
		}
	}
	for _, image := range snapshot.Images {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := taglib.WriteImageOptions(path, image.Data, image.Index, image.Type, image.Description, image.MIME); err != nil {
			return fmt.Errorf("restore candidate artwork %d: %w", image.Index, err)
		}
	}
	actual, err := ReadArtworkSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("verify restored candidate artwork: %w", err)
	}
	if !equalArtworkSnapshots(snapshot, actual) {
		return errors.New("restored candidate artwork does not match snapshot")
	}
	return nil
}

func equalArtworkSnapshots(left, right ArtworkSnapshot) bool {
	if len(left.Images) != len(right.Images) {
		return false
	}
	for index, image := range left.Images {
		other := right.Images[index]
		if image.Index != other.Index || image.Type != other.Type || image.Description != other.Description ||
			image.MIME != other.MIME || !bytes.Equal(image.Data, other.Data) {
			return false
		}
	}
	return true
}
