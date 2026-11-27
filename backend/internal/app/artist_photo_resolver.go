package app

import (
	"context"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/artistphoto"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// libraryArtistPhotoResolver adapts the integration photo projection to the
// service's detached projection. An empty license URL means "not available",
// matching the optional API field.
type libraryArtistPhotoResolver struct {
	resolver *artistphoto.Resolver
}

func newLibraryArtistPhotoResolver() service.ArtistPhotoResolver {
	return libraryArtistPhotoResolver{resolver: artistphoto.NewResolver(nil)}
}

func (adapter libraryArtistPhotoResolver) Resolve(ctx context.Context, provider *musicbrainz.Provider, mbid string) (*service.LibraryArtistPhoto, error) {
	photo, err := adapter.resolver.Resolve(ctx, provider, mbid)
	if err != nil || photo == nil {
		return nil, err
	}
	result := &service.LibraryArtistPhoto{
		ImageURL: photo.ImageURL, SourceURL: photo.SourceURL,
		Author: photo.Author, License: photo.License,
	}
	if licenseURL := strings.TrimSpace(photo.LicenseURL); licenseURL != "" {
		result.LicenseURL = &licenseURL
	}
	return result, nil
}

func (adapter libraryArtistPhotoResolver) DownloadImage(ctx context.Context, imageURL string) ([]byte, string, error) {
	image, err := adapter.resolver.DownloadImage(ctx, imageURL)
	if err != nil {
		return nil, "", err
	}
	return image.Data, image.MIME, nil
}
