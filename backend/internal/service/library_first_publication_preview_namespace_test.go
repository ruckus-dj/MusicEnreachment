package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestBuildFirstPublicationPreviewChecksSharedNamespaceLayouts(t *testing.T) {
	releaseID, positionID, artistID := uuid.New(), uuid.New(), uuid.New()
	snapshot := &persistence.LibraryFirstPublicationContext{
		Library: &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: uuid.New()},
		TemplateTokens: map[string]string{
			"ALBUM_NAME": "Album", "ALBUM_ID": releaseID.String(), "ALBUM_YEAR": "2024",
			"DISC_NUMBER": "1", "TRACK_NUMBER": "1", "TRACK_NAME": "Track",
		},
		ReleasePrimaryCredits: []librarymetadata.ArtistCredit{{ArtistID: artistID.String(), ArtistName: "Artist"}},
	}
	selectedTools := []persistence.SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: uuid.New(), Executable: "ffmpeg", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0"}}

	defaultPreview, err := buildFirstPublicationPreview(snapshot, settings.RuntimeSettings{
		PublicationFormat: "mka", PublicationPathTemplate: publication.DefaultTemplate,
	}, selectedTools, releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(defaultPreview.Blockers, " "), "namespace layout is unsupported") {
		t.Fatalf("supported default namespace was rejected: %#v", defaultPreview.Blockers)
	}

	customPreview, err := buildFirstPublicationPreview(snapshot, settings.RuntimeSettings{
		PublicationFormat: "mka", PublicationPathTemplate: "literal/shared/{ALBUM_NAME}/{TRACK_NAME}.{EXT}",
	}, selectedTools, releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(customPreview.Blockers, " "), "namespace layout is unsupported") {
		t.Fatalf("supported custom nested namespace was rejected: %#v", customPreview.Blockers)
	}
}

func TestFirstPublicationDefaultPathUsesUnbracketedEightCharacterMBIDParts(t *testing.T) {
	releaseID, positionID, artistID := uuid.New(), uuid.New(), uuid.New()
	artistMBID := "11111111-2222-3333-4444-555555555555"
	credit := librarymetadata.ArtistCredit{ArtistID: artistID.String(), ArtistName: "Артист", MBID: &artistMBID}
	lossless := true
	snapshot := &persistence.LibraryFirstPublicationContext{
		Library: &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: uuid.New()},
		TemplateTokens: map[string]string{
			"ALBUM_NAME": "Альбом", "ALBUM_ID": releaseID.String(), "ALBUM_YEAR": "2024", "ALBUM_MBID_PART": "22222222",
			"DISC_NUMBER": "1", "TRACK_NUMBER": "1", "TRACK_NAME": "Трек",
		},
		ReleasePrimaryCredits: []librarymetadata.ArtistCredit{credit},
		Tags: publication.TagProjectionInput{
			Release:  publication.ReleaseTags{Title: "Альбом", Artist: []librarymetadata.ArtistCredit{credit}},
			Position: publication.PositionTags{Title: "Трек", Artist: []librarymetadata.ArtistCredit{credit}},
		},
		Sources: []persistence.FirstPublicationSource{{
			Selection: persistence.PublicationSourceSelection{RootID: uuid.New(), LocationID: uuid.New(), WorkID: uuid.New(), MediaVariantID: uuid.New()}, Selected: true,
			Quality: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true},
		}},
	}
	preview, err := buildFirstPublicationPreview(snapshot, settings.RuntimeSettings{
		PublicationFormat: "source", PublicationPathTemplate: publication.DefaultTemplate,
	}, []persistence.SourceAnalysisToolSelection{{Executable: "ffmpeg"}, {Executable: "ffprobe"}}, releaseID, positionID)
	if err != nil || len(preview.Blockers) != 0 {
		t.Fatalf("default preview blockers = %v, error = %v", preview.Blockers, err)
	}
	want := "Артист [11111111]/Unclassified/2024 - Альбом [22222222]/01-01 - Трек.flac"
	if actual := strings.Join(preview.PathComponents, "/"); actual != want {
		t.Fatalf("default path = %q, want %q", actual, want)
	}
	if actual := preview.DesiredTags.Tags["MUSICBRAINZ_ARTISTID"]; len(actual) != 1 || actual[0] != artistMBID {
		t.Fatalf("full artist MBID tag changed: %v", actual)
	}
}
