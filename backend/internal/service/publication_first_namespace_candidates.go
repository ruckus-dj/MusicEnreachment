package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// ErrFirstPublicationNamespaceLayoutUnavailable reports that a parsed
// publication template is outside the bounded first-publication namespace
// layouts. Callers must surface it as a preview blocker instead of silently
// substituting a default layout.
var ErrFirstPublicationNamespaceLayoutUnavailable = errors.New("first publication namespace layout is unavailable")

// unresolvedFirstPublicationAlbumType mirrors the default template literal for
// a release whose album type did not resolve.
const unresolvedFirstPublicationAlbumType = "Unclassified"

const (
	firstPublicationNodeDirectory = "directory"
	firstPublicationNodeFile      = "file"
)

// buildFirstPublicationNamespaceCandidates derives namespace candidates for
// one first-publication attempt from an already rendered template. Directory
// candidates may share a rendered directory; file collisions remain resolved
// separately. The helper never sanitizes, truncates, or substitutes a path.
func buildFirstPublicationNamespaceCandidates(templateSource string, components []string, artistID, releaseID, positionID uuid.UUID, resolvedAlbumType string) ([]publication.NamespaceCandidate, error) {
	if positionID == uuid.Nil {
		return nil, errors.New("build first publication namespace candidates: position ID is required")
	}
	for _, component := range components {
		if !validFirstPublicationNamespaceComponent(component) {
			return nil, fmt.Errorf("%w: unsafe path component %q", ErrFirstPublicationNamespaceLayoutUnavailable, component)
		}
	}
	if templateSource == "" || len(components) == 0 {
		return nil, fmt.Errorf("%w: template must render at least one path component", ErrFirstPublicationNamespaceLayoutUnavailable)
	}
	if templateSource == publication.DefaultTemplate && (len(components) != 4 || artistID == uuid.Nil || releaseID == uuid.Nil) {
		return nil, fmt.Errorf("%w: default template requires three directories, a file, and complete identity", ErrFirstPublicationNamespaceLayoutUnavailable)
	}
	candidates := make([]publication.NamespaceCandidate, 0, len(components))
	for i := range components {
		name := strings.Join(components[:i+1], "/")
		if i == len(components)-1 {
			candidates = append(candidates, publication.NamespaceCandidate{RelativePath: name, NodeKind: firstPublicationNodeFile, PositionID: positionID})
			continue
		}
		scope, err := firstPublicationDirectoryScope("rendered-directory", name)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, publication.NamespaceCandidate{RelativePath: name, NodeKind: firstPublicationNodeDirectory, DirectoryScope: scope, PositionID: positionID, ShareDirectory: true})
	}
	if templateSource == publication.DefaultTemplate {
		albumType := resolvedAlbumType
		if albumType == "" {
			albumType = unresolvedFirstPublicationAlbumType
		}
		artistScope, err := firstPublicationDirectoryScope("artist", artistID.String())
		if err != nil {
			return nil, err
		}
		typeScope, err := firstPublicationDirectoryScope("artist-type", artistID.String(), albumType)
		if err != nil {
			return nil, err
		}
		releaseScope, err := firstPublicationDirectoryScope("release", releaseID.String())
		if err != nil {
			return nil, err
		}
		candidates[0].DirectoryScope = artistScope
		candidates[1].DirectoryScope = typeScope
		candidates[2].DirectoryScope = releaseScope
		return candidates, nil
	}
	return candidates, nil
}

// firstPublicationDirectoryScope returns the unambiguous JSON scope key for a
// directory owned by the given identity parts.
func firstPublicationDirectoryScope(parts ...string) (string, error) {
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("encode first publication directory scope: %w", err)
	}
	return string(encoded), nil
}

// validFirstPublicationNamespaceComponent rejects rendered components that are
// not a single safe relative path component. Colon is rejected because it is
// reserved by the publication target platforms.
func validFirstPublicationNamespaceComponent(component string) bool {
	if component == "" || component == "." || component == ".." {
		return false
	}
	return !strings.ContainsAny(component, "/\\:\x00")
}

func firstPublicationNamespaceCandidates(snapshot *persistence.LibraryFirstPublicationContext, template string, components []string, releaseID, positionID uuid.UUID) ([]publication.NamespaceCandidate, error) {
	var artistID uuid.UUID
	if template == publication.DefaultTemplate {
		token, handled := firstPublicationPrimaryArtistToken(snapshot, "ALBUM_PRIMARY_ARTIST_ID")
		if !handled || token.Err != nil || !token.Present {
			return nil, errors.Join(errors.New("publication namespace requires a resolved primary artist"), token.Err)
		}
		var err error
		artistID, err = uuid.Parse(token.Value)
		if err != nil {
			return nil, fmt.Errorf("resolve namespace primary artist identity: %w", err)
		}
	}
	return buildFirstPublicationNamespaceCandidates(template, components, artistID, releaseID, positionID, snapshot.TemplateTokens["ALBUM_TYPE"])
}
