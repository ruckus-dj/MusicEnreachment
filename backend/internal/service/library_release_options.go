package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrLibraryReleaseOptionsInvalid  = errors.New("library picker request is invalid")
	ErrLibraryCoverCandidateNotFound = errors.New("library cover candidate not found")
	ErrLibraryCoverCandidateStale    = errors.New("library cover candidate digest changed")
)

type LibraryReleaseOptionsFilter struct {
	Query         string
	Limit, Offset int
}
type LibraryReleaseOptionsPage[T any] struct {
	Items  []T `json:"items" nullable:"false"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}
type LibraryReleaseOptionCredit struct {
	ArtistID   uuid.UUID `json:"artist_id"`
	Name       string    `json:"name"`
	JoinPhrase string    `json:"join_phrase"`
}
type LibraryReleaseOptionProviderLink struct {
	ProviderCode string `json:"provider_code"`
	ProviderKey  string `json:"provider_key"`
}
type LibraryRecordingOption struct {
	ID            uuid.UUID                          `json:"id"`
	Title         string                             `json:"title"`
	Artists       []LibraryReleaseOptionCredit       `json:"artists" nullable:"false"`
	ProviderLinks []LibraryReleaseOptionProviderLink `json:"provider_links" nullable:"false"`
	DurationMS    *int64                             `json:"duration_ms" nullable:"true"`
}
type LibrarySourceVariantLocation struct {
	LocationID      uuid.UUID `json:"location_id"`
	SourceRootID    uuid.UUID `json:"source_root_id"`
	RelativePath    string    `json:"relative_path"`
	RootDisplayName string    `json:"root_display_name"`
	RootStatus      string    `json:"root_status"`
	RootEnabled     bool      `json:"root_enabled"`
}
type LibrarySourceVariantOption struct {
	MediaVariantID   uuid.UUID                      `json:"media_variant_id"`
	MetadataResultID *uuid.UUID                     `json:"metadata_result_id" nullable:"true"`
	Title            string                         `json:"title"`
	Filename         string                         `json:"filename"`
	Codec            string                         `json:"codec"`
	Container        string                         `json:"container"`
	DurationMS       *int64                         `json:"duration_ms" nullable:"true"`
	Locations        []LibrarySourceVariantLocation `json:"locations" nullable:"false"`
}
type LibraryCoverCandidateOption struct {
	MetadataResultID uuid.UUID `json:"metadata_result_id"`
	MediaVariantID   uuid.UUID `json:"media_variant_id"`
	SHA256           string    `json:"sha256"`
	MIME             string    `json:"mime"`
	Width            *int      `json:"width" nullable:"true"`
	Height           *int      `json:"height" nullable:"true"`
	Size             int64     `json:"size"`
	Label            string    `json:"label"`
}
type LibraryCoverCandidateImage struct {
	MIME string
	Data []byte
}

type libraryReleaseOptionsRepository interface {
	ReadLibraryRecordingOptions(context.Context, persistence.LibraryOptionPageRequest) (persistence.LibraryRecordingOptions, error)
	ReadLibrarySourceVariantOptions(context.Context, persistence.LibraryOptionPageRequest) (persistence.LibrarySourceVariantOptions, error)
	ReadLibraryCoverCandidateOptions(context.Context, persistence.LibraryOptionPageRequest) (persistence.LibraryCoverCandidateOptions, error)
	ReadLibraryCoverCandidateImage(context.Context, uuid.UUID) (*persistence.LibraryCoverCandidateOption, error)
}
type LibraryReleaseOptions struct {
	repository libraryReleaseOptionsRepository
}

func NewLibraryReleaseOptions(repository libraryReleaseOptionsRepository) *LibraryReleaseOptions {
	return &LibraryReleaseOptions{repository: repository}
}

func (options *LibraryReleaseOptions) Recordings(ctx context.Context, filter LibraryReleaseOptionsFilter) (LibraryReleaseOptionsPage[LibraryRecordingOption], error) {
	if err := validateLibraryOptionFilter(&filter); err != nil {
		return LibraryReleaseOptionsPage[LibraryRecordingOption]{}, err
	}
	repo, err := options.repo()
	if err != nil {
		return LibraryReleaseOptionsPage[LibraryRecordingOption]{}, err
	}
	page, err := repo.ReadLibraryRecordingOptions(ctx, optionRequest(filter))
	if err != nil {
		return LibraryReleaseOptionsPage[LibraryRecordingOption]{}, fmt.Errorf("read library recording picker: %w", err)
	}
	items := make([]LibraryRecordingOption, 0, len(page.Items))
	for _, r := range page.Items {
		artists := make([]LibraryReleaseOptionCredit, 0, len(r.Artists))
		for _, a := range r.Artists {
			artists = append(artists, LibraryReleaseOptionCredit{ArtistID: a.ArtistID, Name: a.Name, JoinPhrase: a.JoinPhrase})
		}
		links := make([]LibraryReleaseOptionProviderLink, 0, len(r.ProviderLinks))
		for _, link := range r.ProviderLinks {
			links = append(links, LibraryReleaseOptionProviderLink{ProviderCode: link.ProviderCode, ProviderKey: link.ProviderKey})
		}
		items = append(items, LibraryRecordingOption{ID: r.ID, Title: r.Title, Artists: artists, ProviderLinks: links, DurationMS: r.DurationMS})
	}
	return LibraryReleaseOptionsPage[LibraryRecordingOption]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset}, nil
}
func (options *LibraryReleaseOptions) SourceVariants(ctx context.Context, filter LibraryReleaseOptionsFilter) (LibraryReleaseOptionsPage[LibrarySourceVariantOption], error) {
	if err := validateLibraryOptionFilter(&filter); err != nil {
		return LibraryReleaseOptionsPage[LibrarySourceVariantOption]{}, err
	}
	repo, err := options.repo()
	if err != nil {
		return LibraryReleaseOptionsPage[LibrarySourceVariantOption]{}, err
	}
	page, err := repo.ReadLibrarySourceVariantOptions(ctx, optionRequest(filter))
	if err != nil {
		return LibraryReleaseOptionsPage[LibrarySourceVariantOption]{}, fmt.Errorf("read library source variant picker: %w", err)
	}
	items := make([]LibrarySourceVariantOption, 0, len(page.Items))
	for _, r := range page.Items {
		locations := make([]LibrarySourceVariantLocation, 0, len(r.Locations))
		for _, l := range r.Locations {
			locations = append(locations, LibrarySourceVariantLocation{LocationID: l.LocationID, SourceRootID: l.SourceRootID, RelativePath: l.RelativePath, RootDisplayName: l.RootDisplayName, RootStatus: l.RootStatus, RootEnabled: l.RootEnabled})
		}
		items = append(items, LibrarySourceVariantOption{MediaVariantID: r.MediaVariantID, MetadataResultID: r.MetadataResultID, Title: r.Title, Filename: r.Filename, Codec: r.Codec, Container: r.Container, DurationMS: r.DurationMS, Locations: locations})
	}
	return LibraryReleaseOptionsPage[LibrarySourceVariantOption]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset}, nil
}
func (options *LibraryReleaseOptions) CoverCandidates(ctx context.Context, filter LibraryReleaseOptionsFilter) (LibraryReleaseOptionsPage[LibraryCoverCandidateOption], error) {
	if err := validateLibraryOptionFilter(&filter); err != nil {
		return LibraryReleaseOptionsPage[LibraryCoverCandidateOption]{}, err
	}
	repo, err := options.repo()
	if err != nil {
		return LibraryReleaseOptionsPage[LibraryCoverCandidateOption]{}, err
	}
	page, err := repo.ReadLibraryCoverCandidateOptions(ctx, optionRequest(filter))
	if err != nil {
		return LibraryReleaseOptionsPage[LibraryCoverCandidateOption]{}, fmt.Errorf("read library cover candidate picker: %w", err)
	}
	items := make([]LibraryCoverCandidateOption, 0, len(page.Items))
	for _, r := range page.Items {
		items = append(items, LibraryCoverCandidateOption{MetadataResultID: r.MetadataResultID, MediaVariantID: r.MediaVariantID, SHA256: r.SHA256, MIME: r.MIME, Width: r.Width, Height: r.Height, Size: r.Size, Label: r.Label})
	}
	return LibraryReleaseOptionsPage[LibraryCoverCandidateOption]{Items: items, Total: page.Total, Limit: filter.Limit, Offset: filter.Offset}, nil
}
func (options *LibraryReleaseOptions) CoverCandidateImage(ctx context.Context, id uuid.UUID, expectedSHA256 string) (LibraryCoverCandidateImage, error) {
	if id == uuid.Nil || len(expectedSHA256) != 64 || strings.ToLower(expectedSHA256) != expectedSHA256 {
		return LibraryCoverCandidateImage{}, fmt.Errorf("read library cover candidate image: %w", ErrLibraryReleaseOptionsInvalid)
	}
	repo, err := options.repo()
	if err != nil {
		return LibraryCoverCandidateImage{}, err
	}
	item, err := repo.ReadLibraryCoverCandidateImage(ctx, id)
	if err != nil {
		return LibraryCoverCandidateImage{}, fmt.Errorf("read library cover candidate image: %w", err)
	}
	if item == nil {
		return LibraryCoverCandidateImage{}, fmt.Errorf("read library cover candidate image: %w", ErrLibraryCoverCandidateNotFound)
	}
	actual := sha256.Sum256(item.Data)
	if hex.EncodeToString(actual[:]) != expectedSHA256 || item.SHA256 != expectedSHA256 {
		return LibraryCoverCandidateImage{}, fmt.Errorf("read library cover candidate image: %w", ErrLibraryCoverCandidateStale)
	}
	return LibraryCoverCandidateImage{MIME: item.MIME, Data: item.Data}, nil
}
func (options *LibraryReleaseOptions) repo() (libraryReleaseOptionsRepository, error) {
	if options == nil || options.repository == nil {
		return nil, errors.New("library release options repository is unavailable")
	}
	return options.repository, nil
}
func validateLibraryOptionFilter(filter *LibraryReleaseOptionsFilter) error {
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return fmt.Errorf("validate library picker page: %w", ErrLibraryReleaseOptionsInvalid)
	}
	filter.Query = normalizeOptionQuery(filter.Query)
	return nil
}
func normalizeOptionQuery(value string) string {
	value = norm.NFKC.String(value)
	var out strings.Builder
	space := false
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsSpace(r) {
			if !space {
				out.WriteByte(' ')
			}
			space = true
			continue
		}
		out.WriteRune(unicode.ToLower(r))
		space = false
	}
	return strings.TrimSpace(out.String())
}
func optionRequest(filter LibraryReleaseOptionsFilter) persistence.LibraryOptionPageRequest {
	return persistence.LibraryOptionPageRequest{Query: filter.Query, Limit: filter.Limit, Offset: filter.Offset}
}
func IsLibraryReleaseOptionsInvalid(err error) bool {
	return errors.Is(err, ErrLibraryReleaseOptionsInvalid)
}
func IsLibraryCoverCandidateNotFound(err error) bool {
	return errors.Is(err, ErrLibraryCoverCandidateNotFound)
}
func IsLibraryCoverCandidateStale(err error) bool {
	return errors.Is(err, ErrLibraryCoverCandidateStale)
}
