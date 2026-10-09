package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestReconcileIncomingGroupsPreservesOnlyUnchangedManualMembers(t *testing.T) {
	manualID := uuid.New()
	unchanged := incomingTestFile("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000011", "00000000-0000-4000-8000-000000000021", "album/unchanged.flac", nil)
	unchanged.Locations[0].Mtime = "2026-10-09T11:00:00Z"
	unchanged.CaptureIdentity = "new-selected-metadata-winner"
	duplicate := unchanged.Locations[0]
	duplicate.RootID = uuid.New()
	duplicate.RelativePath = "backup/unchanged.flac"
	unchanged.Locations = append(unchanged.Locations, duplicate)
	changed := incomingTestFile("00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000012", "00000000-0000-4000-8000-000000000022", "album/changed.flac", nil)
	changed.Locations[0].Mtime = "2026-10-09T11:01:00Z"
	newcomer := incomingTestFile("00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000013", "00000000-0000-4000-8000-000000000023", "album/new.flac", nil)
	oldLocation := incomingMemberLocations(unchanged.VariantID, []IncomingLocation{incomingTestLocation("00000000-0000-4000-8000-000000000021", "album/unchanged.flac")})
	oldLocation = append(oldLocation, incomingMemberLocations(changed.VariantID, []IncomingLocation{incomingTestLocation("00000000-0000-4000-8000-000000000022", "album/changed.flac")})...)
	manual, _ := json.Marshal([]IncomingGroupingDiagnostic(nil))
	persisted := []persistence.IncomingGroupingGroup{{ID: manualID, Manual: true, Revision: "old", Members: []uuid.UUID{unchanged.VariantID, changed.VariantID}, Diagnostics: manual, Locations: oldLocation}}
	files := []IncomingFile{unchanged, changed, newcomer}
	physical := make(map[uuid.UUID][]IncomingLocation, len(files))
	ready := make(map[uuid.UUID]bool, len(files))
	for _, file := range files {
		physical[file.VariantID], ready[file.VariantID] = file.Locations, true
	}
	groups, err := reconcileIncomingGroups(files, physical, ready, persisted)
	if err != nil {
		t.Fatal(err)
	}
	var retained, reset, newAuto bool
	var retainedRevision string
	for _, group := range groups {
		for _, member := range group.Members {
			if member == unchanged.VariantID {
				retained = group.ID == manualID && group.Manual
				retainedRevision = group.Revision
			}
			if member == changed.VariantID {
				reset = !group.Manual && group.ID != manualID
			}
			if member == newcomer.VariantID {
				newAuto = !group.Manual
			}
		}
	}
	if !retained || !reset || !newAuto {
		t.Fatalf("manual reconciliation retained=%v changed-reset=%v newcomer-auto=%v groups=%+v", retained, reset, newAuto, groups)
	}
	wantRevision, err := incomingGroupRevision([]uuid.UUID{unchanged.VariantID}, incomingFilesByVariant(files))
	if err != nil || retainedRevision != wantRevision || retainedRevision == "old" {
		t.Fatalf("canonical capture revision = %q, want refreshed %q (%v)", retainedRevision, wantRevision, err)
	}
}

func TestIncomingCaptureIdentityIncludesPersistedWinnerValues(t *testing.T) {
	winner := uuid.New()
	capture := persistence.IncomingGroupingCapture{
		MetadataWinner: &winner, MetadataObservedAt: timePointer(time.Date(2026, 10, 9, 11, 0, 0, 123456000, time.UTC)),
		MetadataProvenance: []byte(`{"reader":"test"}`), Tags: []byte(`{"ALBUM":["A"]}`),
		ProbeResult: []byte(`{"duration":120}`), Fingerprint: stringPointer("fp"),
	}
	first, err := incomingCaptureIdentity(capture)
	if err != nil {
		t.Fatal(err)
	}
	capture.Tags = []byte(`{"ALBUM":["B"]}`)
	second, err := incomingCaptureIdentity(capture)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("persisted metadata values must fence capture revision even with a stable winner ID")
	}
	capture.Tags = []byte(`{"ALBUM":["A"]}`)
	nextWinner := uuid.New()
	capture.MetadataWinner = &nextWinner
	third, err := incomingCaptureIdentity(capture)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("selected metadata winner UUID must fence capture revision")
	}
}

func TestIncomingCapturesCoalesceSameSHAMetadataButRetainTechnicalPerLocation(t *testing.T) {
	variantID, workOne, workTwo, rootID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	metadataID, metadataWinner := uuid.New(), uuid.New()
	base := persistence.IncomingGroupingCapture{
		WorkID: workOne, VariantID: variantID, CaptureAnalysisID: &metadataID,
		RootID: rootID, LocationID: uuid.New(), ConfiguredPath: "/music", InventoryPath: "/music",
		RelativePath: "a.flac", SizeBytes: 100, Mtime: time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC),
		Tags: []byte(`{"ALBUM":["A"]}`), ProbeResult: []byte(`{"duration":120}`), SourceSHA256: []byte("digest"),
		MetadataWinner: &metadataWinner, MetadataObservedAt: timePointer(time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC)),
		MetadataProvenance: []byte(`{"reader":"one"}`), Ready: true,
	}
	other := base
	other.WorkID = workTwo
	other.LocationID = uuid.New()
	other.RelativePath = "backup/a.flac"
	other.ProbeResult = []byte(`{"duration":121}`)
	other.Fingerprint = stringPointer("other-fingerprint")
	view, err := incomingFilesFromCaptures([]persistence.IncomingGroupingCapture{base, other})
	if err != nil {
		t.Fatalf("coalesce same SHA metadata: %v", err)
	}
	if len(view.Files) != 1 || len(view.Files[0].Locations) != 2 || view.Files[0].Locations[0].TechnicalIdentity == view.Files[0].Locations[1].TechnicalIdentity {
		t.Fatalf("same SHA should coalesce metadata but retain per-location technical selections: %+v", view)
	}
}

func TestUnreadyManualMemberRetainsCorrectionAndReturnsWhenReady(t *testing.T) {
	variantID, workID, rootID, locationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	metadataID, metadataWinner := uuid.New(), uuid.New()
	capture := persistence.IncomingGroupingCapture{
		WorkID: workID, VariantID: variantID, CaptureAnalysisID: &metadataID, RootID: rootID, LocationID: locationID,
		ConfiguredPath: "/music", InventoryPath: "/music", RelativePath: "album/track.flac", SizeBytes: 100,
		Mtime: time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC), Tags: []byte(`{"ALBUM":["A"]}`),
		MetadataWinner: &metadataWinner, MetadataObservedAt: timePointer(time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC)),
		MetadataProvenance: []byte(`{"reader":"test"}`), Fingerprint: stringPointer("fp-current"),
		FingerprintWinner: uuidPointer(uuid.New()), Ready: false,
	}
	groupID := uuid.New()
	fence := incomingMemberLocations(variantID, []IncomingLocation{{
		RootID: rootID, WorkID: workID, LocationID: locationID, ConfiguredPath: "/music", InventoryPath: "/music",
		RelativePath: capture.RelativePath, SizeBytes: capture.SizeBytes, Mtime: normalizedIncomingMtime(capture.Mtime),
	}})
	persisted := []persistence.IncomingGroupingGroup{{ID: groupID, Manual: true, Members: []uuid.UUID{variantID}, Locations: fence, Revision: "prior"}}
	view, err := incomingFilesFromCaptures([]persistence.IncomingGroupingCapture{capture})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := reconcileIncomingGroups(view.Files, view.Locations, view.Ready, persisted)
	if err != nil || len(visibleIncomingGroups(retained)) != 0 || len(retained) != 1 || len(retained[0].UnreadyMembers) != 1 {
		t.Fatalf("unready physical member should be hidden but retained: groups=%+v err=%v", retained, err)
	}
	stored, err := persistableIncomingGroups(retained, view.Locations, view.Files)
	if err != nil || len(stored) != 1 || len(stored[0].UnreadyMembers) != 1 {
		t.Fatalf("unready correction fence was not persisted: groups=%+v err=%v", stored, err)
	}
	capture.Ready = true
	newcomer := capture
	newcomer.WorkID, newcomer.LocationID, newcomer.CaptureAnalysisID = uuid.New(), uuid.New(), uuidPointer(uuid.New())
	newcomer.VariantID = uuid.New()
	newcomer.RelativePath = "album/new.flac"
	newcomer.MetadataWinner = uuidPointer(uuid.New())
	readyView, err := incomingFilesFromCaptures([]persistence.IncomingGroupingCapture{capture, newcomer})
	if err != nil {
		t.Fatal(err)
	}
	returned, err := reconcileIncomingGroups(readyView.Files, readyView.Locations, readyView.Ready, stored)
	if err != nil || len(visibleIncomingGroups(returned)) != 2 {
		t.Fatalf("manual group should return after analysis readiness: groups=%+v err=%v", returned, err)
	}
	var manualReturned, newcomerAuto bool
	for _, group := range returned {
		for _, member := range group.Members {
			if member == variantID {
				manualReturned = group.ID == groupID && group.Manual
			}
			if member == newcomer.VariantID {
				newcomerAuto = !group.Manual
			}
		}
	}
	if !manualReturned || !newcomerAuto {
		t.Fatalf("manual protection/newcomer regrouping failed: %+v", returned)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func uuidPointer(value uuid.UUID) *uuid.UUID { return &value }

func TestApplyIncomingDraftKeepsUnaffectedGroupKinds(t *testing.T) {
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	auto := IncomingGroup{ID: uuid.New(), Members: []uuid.UUID{first, second}}
	manual := IncomingGroup{ID: uuid.New(), Manual: true, Members: []uuid.UUID{third}}
	proposed := []IncomingGroup{
		{ID: uuid.New(), Members: []uuid.UUID{first}},
		{ID: uuid.New(), Members: []uuid.UUID{second}},
		manual,
	}
	result, err := applyIncomingDraft([]IncomingGroup{auto, manual}, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if result[0].ID == proposed[0].ID || !result[0].Manual || result[1].ID == proposed[1].ID || !result[1].Manual {
		t.Fatalf("changed groups must be freshly namespaced manual groups: %+v", result)
	}
	if result[2].ID != manual.ID || !result[2].Manual {
		t.Fatalf("unaffected manual group was overwritten: %+v", result[2])
	}
}

func TestManualCorrectionPhysicalFenceIncludesConfiguredInventoryNamespace(t *testing.T) {
	member, root := uuid.New(), uuid.New()
	old := incomingMemberLocations(member, []IncomingLocation{{RootID: root, ConfiguredPath: "/music-a", InventoryPath: "/music-a", RelativePath: "album/a.flac", SizeBytes: 10, Mtime: "2026-10-09T11:00:00Z"}})
	current := []IncomingLocation{{RootID: root, ConfiguredPath: "/music-b", InventoryPath: "/music-b", RelativePath: "album/a.flac", SizeBytes: 10, Mtime: "2026-10-09T11:00:00Z"}}
	if hasSurvivingLocation(member, old, current) {
		t.Fatal("same path/stat in a different configured inventory namespace must not retain a correction")
	}
}

func incomingTestLocation(root, relative string) IncomingLocation {
	return IncomingLocation{RootID: uuid.MustParse(root), RelativePath: relative, SizeBytes: 10, Mtime: "2026-10-09T11:00:00Z"}
}
