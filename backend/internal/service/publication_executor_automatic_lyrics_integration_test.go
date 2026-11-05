//go:build integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// automaticLyricsExecutorCommands retains the stale-result fixture's verified
// version banners while reusing the real-file probe and stream-copy transport.
type automaticLyricsExecutorCommands struct {
	versions  *staleResultPreparationCommand
	transport *automaticRetryCommands
}

type automaticLyricsRetryScenario struct {
	name          string
	mismatch      bool
	authorityRace bool
}

func (commands *automaticLyricsExecutorCommands) Run(ctx context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	if len(args) == 1 && args[0] == "-version" {
		return commands.versions.Run(ctx, executable, args...)
	}
	return commands.transport.Run(ctx, executable, args...)
}

func (commands *automaticLyricsExecutorCommands) RunFile(ctx context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	return commands.transport.RunFile(ctx, request)
}

func testPublicationExecutorAutomaticLyricsRetryNativeReadback(t *testing.T, scenario automaticLyricsRetryScenario) {
	t.Helper()
	ctx := context.Background()
	transport := &automaticRetryCommands{probeJSON: automaticRetryProbeFixture(t)}
	fixture := newQuiescenceExecutorFixture(t, &automaticLyricsExecutorCommands{
		versions:  &staleResultPreparationCommand{},
		transport: transport,
	})
	currentPath := filepath.Join(fixture.outputPath, "Track.flac")
	cover := &metadata.Cover{Data: simpleCoverPNG(t, color.RGBA{R: 255, A: 255}), MIME: "image/png"}
	currentLyrics := "[00:00.500]old current lyrics\n"
	if _, err := (metadata.Writer{}).Write(ctx, currentPath, map[string][]string{
		"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {currentLyrics},
	}, cover); err != nil {
		t.Fatal(err)
	}
	artwork, err := metadata.SnapshotFlacArtwork(ctx, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if facts := artwork.ApprovedCoverFacts(); facts.Count != 1 || facts.MIME != "image/png" || facts.ImageSize == 0 {
		t.Fatalf("native CURRENT cover facts=%+v; want one encoded PNG", facts)
	}
	info, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	input, found, err := fixture.repository.preparationInputs(ctx, persistence.PublicationDeliveryGuard{})
	if err != nil || !found || input.Rewrite == nil {
		t.Fatalf("read scripted frozen input: found=%t rewrite=%t err=%v", found, input.Rewrite != nil, err)
	}
	digest := sha256SumHex(contents)
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	input.Rewrite.Ownership = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest}
	capture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capture.Tags["LYRICS"], []string{currentLyrics}) {
		t.Fatalf("CURRENT lyrics fixture=%#v; want frozen lyrics %q", capture.Tags["LYRICS"], currentLyrics)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	input.AutomaticLyricsRetry = &persistence.PublicationAutomaticLyricsRetryInput{
		ExpectedMetadataRevision: 7, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration, CurrentAssets: assets,
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate automatic-retry frozen input: %v", err)
	}
	attempt, err := fixture.repository.readAttempt(ctx, fixture.operationID)
	if err != nil || attempt == nil {
		t.Fatalf("read scripted attempt: attempt=%+v err=%v", attempt, err)
	}
	attempt.FrozenInputs, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	fixture.repository.preparationInputs = func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
		return input, true, nil
	}
	lookup := &automaticRetryLookup{}
	fixture.executor.lyrics = NewPublicationLyricsResolver(automaticRetryEnabled{}, lookup)
	if scenario.mismatch {
		fixture.executor.metadata = &automaticRetryMissingLyricsWriter{}
	}
	currentAllowed := true
	if scenario.authorityRace {
		lookup.onLookup = func() error {
			currentAllowed = false
			return nil
		}
	}
	fixture.repository.obsoleteOutput = func(context.Context, persistence.PublicationDeliveryGuard, string) (*persistence.PublicationObsoleteOutputCandidate, error) {
		return nil, nil
	}
	currentChecks := 0
	fixture.repository.resultCurrent = func(_ context.Context, guard persistence.PublicationDeliveryGuard) (bool, error) {
		currentChecks++
		if guard.OperationID != fixture.operationID || guard.ExpectedPositionID != fixture.positionID {
			t.Fatalf("result currentness received unexpected delivery guard: %+v", guard)
		}
		return currentAllowed, nil
	}
	commitCalls := 0
	fixture.repository.commit = func(_ context.Context, guard persistence.PublicationDeliveryGuard, result persistence.PublicationResult) (bool, error) {
		commitCalls++
		if guard.OperationID != fixture.operationID || result.InitialLyrics == nil {
			t.Fatalf("commit did not receive guarded automatic-lyrics result: guard=%+v result=%+v", guard, result.InitialLyrics)
		}
		return true, nil
	}

	deliveryErr := fixture.executor.ExecutePublicationDelivery(ctx, fixture.operationID, fixture.riverJobID, 1)
	if scenario.authorityRace {
		if deliveryErr == nil || !strings.Contains(deliveryErr.Error(), "publication inputs or current generation changed before installation") {
			t.Fatalf("authority race did not reject delivery at the late currentness check: %v", deliveryErr)
		}
	} else if deliveryErr != nil {
		t.Fatalf("execute real automatic-lyrics retry: %v", deliveryErr)
	}
	if len(lookup.requests) != 1 || lookup.requests[0].Artist != "Artist" || lookup.requests[0].Title != "Track" || lookup.requests[0].Duration <= 0 || lookup.requests[0].Duration == math.Trunc(lookup.requests[0].Duration) {
		t.Fatalf("automatic lookup calls/metadata do not match frozen CURRENT: %+v", lookup.requests)
	}
	if scenario.authorityRace {
		if currentChecks != 1 || commitCalls != 0 {
			t.Fatalf("authority-race currentness/commit calls=%d/%d; want one check and no commit", currentChecks, commitCalls)
		}
		got, readErr := os.ReadFile(currentPath)
		if readErr != nil || sha256SumHex(got) != digest {
			t.Fatalf("denied authority race changed CURRENT bytes: read error=%v", readErr)
		}
		finalCapture, readErr := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
		if readErr != nil || !reflect.DeepEqual(finalCapture.Tags["LYRICS"], []string{currentLyrics}) {
			t.Fatalf("denied authority race changed CURRENT lyrics=%#v err=%v", finalCapture.Tags["LYRICS"], readErr)
		}
		if err := artwork.VerifyEquivalent(ctx, currentPath); err != nil {
			t.Fatalf("denied authority race changed CURRENT audio/artwork: %v", err)
		}
		return
	}
	finalCapture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
	wantLyrics := []string{automaticRetryLyrics}
	if scenario.mismatch {
		wantLyrics = nil
		writer, ok := fixture.executor.metadata.(*automaticRetryMissingLyricsWriter)
		if !ok || writer.calls != 2 {
			t.Fatalf("fallback metadata writer=%T calls=%d; want native write, failed readback, and rebuild", fixture.executor.metadata, writer.calls)
		}
	}
	if err != nil || !reflect.DeepEqual(finalCapture.Tags["LYRICS"], wantLyrics) {
		t.Fatalf("native candidate LYRICS=%#v err=%v; want %#v", finalCapture.Tags["LYRICS"], err, wantLyrics)
	}
	if err := artwork.VerifyEquivalent(ctx, currentPath); err != nil {
		t.Fatalf("automatic lyrics retry changed native audio/artwork: %v", err)
	}
	if currentChecks != 1 || commitCalls != 1 || fixture.repository.committedResult == nil {
		t.Fatalf("late currentness/commit calls=%d/%d result=%+v", currentChecks, commitCalls, fixture.repository.committedResult)
	}
	projection := fixture.repository.committedResult.InitialLyrics
	if projection.Authority != "automatic" || projection.Source != "lrclib" || projection.AuthorityMetadataRevision != input.AutomaticLyricsRetry.ExpectedMetadataRevision {
		t.Fatalf("committed automatic lyrics authority projection=%+v", projection)
	}
	if scenario.mismatch && (projection.Outcome != persistence.PublicationOptionalLyricsRebuiltWithoutLyrics || projection.OutcomeWarningCode != "readback_mismatch") {
		t.Fatalf("mismatch result did not record rebuilt-without-lyrics projection: %+v", projection)
	}
	if !scenario.mismatch && projection.Outcome != persistence.PublicationOptionalLyricsExactRoundTrip {
		t.Fatalf("successful result did not record exact round-trip projection: %+v", projection)
	}
	if len(fixture.repository.guards) == 0 {
		t.Fatal("executor did not propagate guarded repository calls")
	}
	for _, guard := range fixture.repository.guards {
		if guard.OperationID != fixture.operationID {
			t.Fatalf("unexpected guarded repository call: %+v", guard)
		}
	}
}

func sha256SumHex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

var _ PublicationFileCommandRunner = (*automaticLyricsExecutorCommands)(nil)
