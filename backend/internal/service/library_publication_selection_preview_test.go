package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestSelectionPreviewFirstDispositionReasons(t *testing.T) {
	position := LibraryPublicationSelectionPosition{ReleaseID: uuid.New(), PositionID: uuid.New()}
	ready := FirstPublicationPreview{
		SelectedSource:         &FirstPublicationPreviewSource{},
		Transform:              &publication.AudioTransformPlan{},
		PathComponents:         []string{"Artist", "Album", "Track.flac"},
		ResolvedPathComponents: []string{"Artist", "Album", "Track.flac"},
	}
	item := selectionPreviewItemFromFirstPreview(position, ready, true)
	if item.Disposition != LibraryPublicationSelectionDispositionCreate || item.Reason != "" || item.ResolvedPath != "Artist/Album/Track.flac" {
		t.Fatalf("ready first preview = %+v", item)
	}

	tests := []struct {
		name  string
		alter func(*FirstPublicationPreview)
		tools bool
		want  string
	}{
		{name: "active attempt", alter: func(preview *FirstPublicationPreview) {
			preview.ActiveAttempt = &persistence.LibraryPublicationOperation{}
		}, tools: true, want: LibraryPublicationSelectionReasonAttemptActive},
		{name: "tools unavailable", alter: func(*FirstPublicationPreview) {}, tools: false, want: LibraryPublicationSelectionReasonToolsUnavailable},
		{name: "source selection required", alter: func(preview *FirstPublicationPreview) { preview.SelectedSource = nil }, tools: true, want: LibraryPublicationSelectionReasonSourceSelection},
		{name: "format unavailable", alter: func(preview *FirstPublicationPreview) { preview.Transform = nil }, tools: true, want: LibraryPublicationSelectionReasonFormatUnavailable},
		{name: "path unavailable", alter: func(preview *FirstPublicationPreview) { preview.PathComponents = nil }, tools: true, want: LibraryPublicationSelectionReasonPathUnavailable},
		{name: "context blocked", alter: func(preview *FirstPublicationPreview) { preview.Blockers = []string{"snapshot blocker"} }, tools: true, want: LibraryPublicationSelectionReasonContextBlocked},
		{name: "unsafe resolved path", alter: func(preview *FirstPublicationPreview) { preview.ResolvedPathComponents = []string{"..", "escape.flac"} }, tools: true, want: LibraryPublicationSelectionReasonResolvedPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preview := ready
			test.alter(&preview)
			item := selectionPreviewItemFromFirstPreview(position, preview, test.tools)
			if item.Disposition != LibraryPublicationSelectionDispositionBlocked || item.Reason != test.want {
				t.Fatalf("blocked first preview = %+v, want reason %q", item, test.want)
			}
			if item.ResolvedPath != "" {
				t.Fatalf("blocked first preview leaked path %q", item.ResolvedPath)
			}
		})
	}
}

func TestSelectionPreviewRetainedDispositionReasons(t *testing.T) {
	position := LibraryPublicationSelectionPosition{ReleaseID: uuid.New(), PositionID: uuid.New()}
	resolved := []string{"Artist", "Album", "Track.flac"}

	ready := FirstPublicationPreview{
		ResolvedPathComponents: resolved,
		ConfirmationCapability: &SourceRepublishConfirmationCapability{Kind: "retained_same_path", Status: "ready"},
	}
	item := selectionPreviewItemFromRetainedPreview(position, ready)
	if item.Disposition != LibraryPublicationSelectionDispositionUpdate || item.ResolvedPath != "Artist/Album/Track.flac" || item.Reason != "" {
		t.Fatalf("ready retained preview = %+v", item)
	}

	unchanged := ready
	unchanged.ConfirmationCapability = &SourceRepublishConfirmationCapability{Status: "unchanged", ReasonCodes: []string{"content_unchanged"}}
	item = selectionPreviewItemFromRetainedPreview(position, unchanged)
	if item.Disposition != LibraryPublicationSelectionDispositionUnchanged || item.Reason != LibraryPublicationSelectionReasonContentUnchanged || item.ResolvedPath == "" {
		t.Fatalf("unchanged retained preview = %+v", item)
	}

	notEligible := ready
	notEligible.ConfirmationCapability = &SourceRepublishConfirmationCapability{Status: "blocked", ReasonCodes: []string{"source_republish_not_eligible"}}
	for name, preview := range map[string]FirstPublicationPreview{
		"blocked capability": notEligible,
		"missing capability": {ResolvedPathComponents: resolved},
	} {
		item := selectionPreviewItemFromRetainedPreview(position, preview)
		if item.Disposition != LibraryPublicationSelectionDispositionBlocked || item.Reason != LibraryPublicationSelectionReasonNotEligible {
			t.Fatalf("%s retained preview = %+v", name, item)
		}
	}

	active := notEligible
	active.ActiveAttempt = &persistence.LibraryPublicationOperation{}
	if item := selectionPreviewItemFromRetainedPreview(position, active); item.Reason != LibraryPublicationSelectionReasonAttemptActive {
		t.Fatalf("active retained preview = %+v", item)
	}

	blockedPlan := ready
	blockedPlan.Blockers = []string{"planner blocker"}
	if item := selectionPreviewItemFromRetainedPreview(position, blockedPlan); item.Reason != LibraryPublicationSelectionReasonContextBlocked {
		t.Fatalf("blocked retained plan = %+v", item)
	}
}

func TestSelectionPreviewPathClashes(t *testing.T) {
	items := []LibraryPublicationSelectionPreviewItem{
		{ReleaseID: uuid.New(), PositionID: uuid.New(), Disposition: LibraryPublicationSelectionDispositionCreate, ResolvedPath: "Artist/Album/Track.flac"},
		{ReleaseID: uuid.New(), PositionID: uuid.New(), Disposition: LibraryPublicationSelectionDispositionUpdate, ResolvedPath: "Artist/Album/Track.flac"},
		{ReleaseID: uuid.New(), PositionID: uuid.New(), Disposition: LibraryPublicationSelectionDispositionCreate, ResolvedPath: "Artist/Album/Other.flac"},
		{ReleaseID: uuid.New(), PositionID: uuid.New(), Disposition: LibraryPublicationSelectionDispositionUnchanged, ResolvedPath: "Artist/Album/Track.flac"},
	}
	markSelectionPreviewPathClashes(items)
	if !items[0].PathClash || !items[1].PathClash || items[2].PathClash || items[3].PathClash {
		t.Fatalf("clash flags = %+v", items)
	}
	if len(items[0].PathClashPositions) != 1 || items[0].PathClashPositions[0].PositionID != items[1].PositionID {
		t.Fatalf("clash references = %+v", items[0].PathClashPositions)
	}
	if len(items[1].PathClashPositions) != 1 || items[1].PathClashPositions[0].PositionID != items[0].PositionID {
		t.Fatalf("clash references = %+v", items[1].PathClashPositions)
	}
}

func TestSelectionPreviewRejectsInvalidRequests(t *testing.T) {
	service := &LibraryPublications{}
	valid := LibraryPublicationSelectionPosition{ReleaseID: uuid.New(), PositionID: uuid.New()}
	tests := []struct {
		name    string
		request LibraryPublicationSelectionPreviewRequest
	}{
		{name: "missing mode", request: LibraryPublicationSelectionPreviewRequest{Mode: "unknown", Positions: []LibraryPublicationSelectionPosition{valid}}},
		{name: "empty positions", request: LibraryPublicationSelectionPreviewRequest{}},
		{name: "too many positions", request: LibraryPublicationSelectionPreviewRequest{Positions: repeatSelectionPosition(valid, LibraryPublicationSelectionPreviewMaxPositions+1)}},
		{name: "zero identity", request: LibraryPublicationSelectionPreviewRequest{Positions: []LibraryPublicationSelectionPosition{{ReleaseID: uuid.Nil, PositionID: uuid.New()}}}},
		{name: "duplicate pair", request: LibraryPublicationSelectionPreviewRequest{Positions: []LibraryPublicationSelectionPosition{valid, valid}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.PreviewPublicationSelection(context.Background(), test.request); !errors.Is(err, ErrLibraryPublicationSelectionPreviewInvalid) {
				t.Fatalf("error = %v, want invalid request", err)
			}
		})
	}
}

func repeatSelectionPosition(position LibraryPublicationSelectionPosition, count int) []LibraryPublicationSelectionPosition {
	positions := make([]LibraryPublicationSelectionPosition, 0, count)
	for index := 0; index < count; index++ {
		position.PositionID = uuid.New()
		positions = append(positions, position)
	}
	return positions
}
