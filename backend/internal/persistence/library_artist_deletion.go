package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type libraryArtistReleaseProof struct {
	ReleaseID uuid.UUID                   `json:"release_id"`
	Scope     LibraryDeletionScope        `json:"scope"`
	Depends   libraryDeletionDependencies `json:"dependencies"`
}

type libraryArtistReference struct {
	Kind       string    `bun:"kind" json:"kind"`
	OwnerID    uuid.UUID `bun:"owner_id" json:"owner_id"`
	Position   int       `bun:"position" json:"position"`
	ArtistID   uuid.UUID `bun:"artist_id" json:"artist_id"`
	Name       string    `bun:"name" json:"name"`
	JoinPhrase string    `bun:"join_phrase" json:"join_phrase"`
}

func readLibraryArtistDeletionPreview(ctx context.Context, tx bun.IDB, artistID uuid.UUID) (LibraryDeletionPreview, error) {
	var preview LibraryDeletionPreview
	var artistName string
	var visible bool
	if err := tx.NewRaw(`SELECT name,catalog_visible FROM local_artist WHERE id=?`, artistID).Scan(ctx, &artistName, &visible); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return preview, ErrLibraryReleaseNotFound
		}
		return preview, fmt.Errorf("preview library artist deletion: read artist: %w", err)
	}
	if !visible {
		return preview, ErrLibraryReleaseNotFound
	}
	var references []libraryArtistReference
	if err := tx.NewRaw(`SELECT 'release' AS kind,ra.release_id AS owner_id,ra.position,ra.artist_id,ra.name,ra.name_join_phrase AS join_phrase
		FROM release_artist ra WHERE ra.artist_id=?
		UNION ALL
		SELECT 'release_primary',r.id,0,r.primary_artist_id,'','' FROM local_release r WHERE r.primary_artist_id=?
		UNION ALL
		SELECT 'position_primary',rt.id,rt.position,rt.primary_artist_id,'','' FROM release_track rt WHERE rt.primary_artist_id=?
		UNION ALL
		SELECT 'collection',ca.collection_id,ca.position,ca.artist_id,ca.name,ca.name_join_phrase FROM collection_artist ca WHERE ca.artist_id=?
		UNION ALL
		SELECT 'track',ta.track_id,ta.position,ta.artist_id,ta.name,ta.name_join_phrase FROM track_artist ta WHERE ta.artist_id=?
		UNION ALL
		SELECT 'recording',ra.recording_id,ra.position,ra.artist_id,ra.name,ra.name_join_phrase FROM recording_artist ra WHERE ra.artist_id=?
		ORDER BY kind,owner_id,position`, artistID, artistID, artistID, artistID, artistID, artistID).Scan(ctx, &references); err != nil {
		return preview, fmt.Errorf("preview library artist deletion: read artist references: %w", err)
	}
	targetReleases := make([]uuid.UUID, 0)
	if err := tx.NewRaw(`SELECT r.id FROM local_release r
		WHERE (r.primary_artist_id=? AND EXISTS (SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?)) OR (r.primary_artist_id IS NULL AND EXISTS (
			SELECT 1 FROM release_artist ra WHERE ra.release_id=r.id AND ra.artist_id=?
		) AND (SELECT count(DISTINCT ra.artist_id) FROM release_artist ra WHERE ra.release_id=r.id)=1)
		ORDER BY r.id`, artistID, artistID, artistID).Scan(ctx, &targetReleases); err != nil {
		return preview, fmt.Errorf("preview library artist deletion: resolve primary releases: %w", err)
	}
	referenceReleases := make([]uuid.UUID, 0)
	if err := tx.NewRaw(`SELECT release_id FROM (
		SELECT ra.release_id FROM release_artist ra WHERE ra.artist_id=?
		UNION SELECT r.id FROM local_release r WHERE r.primary_artist_id=?
		UNION SELECT rt.release_id FROM release_track rt WHERE rt.primary_artist_id=?
		UNION SELECT rt.release_id FROM release_track rt JOIN track_artist ta ON ta.track_id=rt.track_id WHERE ta.artist_id=?
		UNION SELECT rt.release_id FROM release_track rt JOIN recording_artist ra ON ra.recording_id=rt.recording_id WHERE ra.artist_id=?
	) refs ORDER BY release_id`, artistID, artistID, artistID, artistID, artistID).Scan(ctx, &referenceReleases); err != nil {
		return preview, fmt.Errorf("preview library artist deletion: read referenced releases: %w", err)
	}
	warningReleases := make([]uuid.UUID, 0)
	if len(targetReleases) > 0 {
		if err := tx.NewRaw(`SELECT release_id FROM release_artist WHERE release_id IN (?) GROUP BY release_id
			HAVING count(DISTINCT artist_id)>1 ORDER BY release_id`, bun.List(targetReleases)).Scan(ctx, &warningReleases); err != nil {
			return preview, fmt.Errorf("preview library artist deletion: read coartist warnings: %w", err)
		}
	}
	type artistReleasePresentation struct {
		ReleaseID    uuid.UUID `bun:"release_id"`
		Title        string    `bun:"title"`
		CoartistName *string   `bun:"coartist_name"`
	}
	presentations := make([]artistReleasePresentation, 0)
	if len(targetReleases) > 0 {
		if err := tx.NewRaw(`SELECT r.id AS release_id,r.title,
			CASE WHEN ra.artist_id IS NULL THEN NULL ELSE COALESCE(NULLIF(btrim(ra.name),''),NULLIF(btrim(a.name),''),'Unknown artist') END AS coartist_name
			FROM local_release r
			LEFT JOIN release_artist ra ON ra.release_id=r.id AND ra.artist_id<>?
			LEFT JOIN local_artist a ON a.id=ra.artist_id
			WHERE r.id IN (?) ORDER BY r.id,ra.position`, artistID, bun.List(targetReleases)).Scan(ctx, &presentations); err != nil {
			return preview, fmt.Errorf("preview library artist deletion: read release presentation: %w", err)
		}
	}
	warningSet := make(map[uuid.UUID]struct{}, len(warningReleases))
	for _, releaseID := range warningReleases {
		warningSet[releaseID] = struct{}{}
	}
	includedReleases := make([]LibraryDeletionArtistRelease, 0, len(targetReleases))
	warnings := make([]LibraryDeletionCoartistWarning, 0, len(warningReleases))
	releaseSeen := make(map[uuid.UUID]struct{}, len(targetReleases))
	warningIndex := make(map[uuid.UUID]int, len(warningReleases))
	warningNames := make(map[uuid.UUID]map[string]struct{}, len(warningReleases))
	for _, presentation := range presentations {
		if _, ok := releaseSeen[presentation.ReleaseID]; !ok {
			releaseSeen[presentation.ReleaseID] = struct{}{}
			includedReleases = append(includedReleases, LibraryDeletionArtistRelease{Title: presentation.Title})
			if _, warns := warningSet[presentation.ReleaseID]; warns {
				warningIndex[presentation.ReleaseID] = len(warnings)
				warningNames[presentation.ReleaseID] = make(map[string]struct{})
				warnings = append(warnings, LibraryDeletionCoartistWarning{ReleaseTitle: presentation.Title, ArtistNames: make([]string, 0)})
			}
		}
		if presentation.CoartistName != nil {
			warning, warns := warningIndex[presentation.ReleaseID]
			if warns {
				name := *presentation.CoartistName
				if _, duplicate := warningNames[presentation.ReleaseID][name]; !duplicate {
					warningNames[presentation.ReleaseID][name] = struct{}{}
					warnings[warning].ArtistNames = append(warnings[warning].ArtistNames, name)
				}
			}
		}
	}
	dependency := libraryDeletionDependencies{
		PositionIDs: []uuid.UUID{}, PositionTrackLinks: []json.RawMessage{}, SurvivingTrackLinks: []json.RawMessage{},
		SurvivingRecordingLinks: []json.RawMessage{}, CandidateTrackIDs: []uuid.UUID{}, OrphanTrackIDs: []uuid.UUID{},
		CandidateRecordingIDs: []uuid.UUID{}, OrphanRecordingIDs: []uuid.UUID{}, TrackMappings: []json.RawMessage{},
		TrackCredits: []json.RawMessage{}, TrackSources: []json.RawMessage{}, RecordingMappings: []json.RawMessage{},
		RecordingCredits: []json.RawMessage{}, MediumIDs: []uuid.UUID{}, ReleaseCredits: []json.RawMessage{},
		CollectionLinks: []json.RawMessage{}, ProviderLinks: []json.RawMessage{}, ArtistReleaseProofs: []json.RawMessage{},
		ArtistReferenceProofs: []json.RawMessage{},
	}
	positions := make([]uuid.UUID, 0)
	publicationByPosition := make(map[uuid.UUID]TrackPublication)
	receiptIDs := make([]uuid.UUID, 0)
	trackSet, recordingSet := map[uuid.UUID]struct{}{}, map[uuid.UUID]struct{}{}
	for _, releaseID := range targetReleases {
		release, err := readLibraryDeletionPreview(ctx, tx, "release", releaseID, uuid.Nil)
		if err != nil {
			return preview, err
		}
		proof, err := json.Marshal(libraryArtistReleaseProof{ReleaseID: releaseID, Scope: release.Scope, Depends: release.dependencies})
		if err != nil {
			return preview, fmt.Errorf("preview library artist deletion: encode release proof: %w", err)
		}
		dependency.ArtistReleaseProofs = append(dependency.ArtistReleaseProofs, proof)
		positions = append(positions, release.PositionIDs...)
		for _, publication := range release.Publications {
			publicationByPosition[publication.PositionID] = publication
		}
		for _, id := range release.dependencies.CandidateTrackIDs {
			trackSet[id] = struct{}{}
		}
		for _, id := range release.dependencies.CandidateRecordingIDs {
			recordingSet[id] = struct{}{}
		}
		dependency.MediumIDs = append(dependency.MediumIDs, release.dependencies.MediumIDs...)
		dependency.ReleaseCredits = append(dependency.ReleaseCredits, release.dependencies.ReleaseCredits...)
		dependency.CollectionLinks = append(dependency.CollectionLinks, release.dependencies.CollectionLinks...)
		dependency.ProviderLinks = append(dependency.ProviderLinks, release.dependencies.ProviderLinks...)
		releaseReceipts := make([]uuid.UUID, 0)
		if err := tx.NewRaw(`SELECT request_id FROM manual_matching_receipt WHERE original_local_release_id=? ORDER BY request_id`, releaseID).Scan(ctx, &releaseReceipts); err != nil {
			return preview, fmt.Errorf("preview library artist deletion: read release receipts: %w", err)
		}
		receiptIDs = append(receiptIDs, releaseReceipts...)
		dependency.PositionTrackLinks = append(dependency.PositionTrackLinks, release.dependencies.PositionTrackLinks...)
	}
	slices.SortFunc(positions, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	slices.SortFunc(receiptIDs, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	dependency.PositionIDs = slices.Clone(positions)
	dependency.CandidateTrackIDs = sortedUUIDSet(trackSet)
	dependency.CandidateRecordingIDs = sortedUUIDSet(recordingSet)
	trackSurvivors := make([]libraryDeletionPositionTrackLink, 0)
	if len(dependency.CandidateTrackIDs) > 0 {
		if err := tx.NewRaw(`SELECT id AS position_id,release_id,track_id,recording_id FROM release_track WHERE track_id IN (?) AND release_id NOT IN (?) ORDER BY id`, bun.List(dependency.CandidateTrackIDs), bun.List(targetReleases)).Scan(ctx, &trackSurvivors); err != nil {
			return preview, fmt.Errorf("preview library artist deletion: read surviving tracks: %w", err)
		}
	}
	recordingSurvivors := make([]libraryDeletionPositionTrackLink, 0)
	if len(dependency.CandidateRecordingIDs) > 0 {
		if err := tx.NewRaw(`SELECT id AS position_id,release_id,track_id,recording_id FROM release_track WHERE recording_id IN (?) AND release_id NOT IN (?) ORDER BY id`, bun.List(dependency.CandidateRecordingIDs), bun.List(targetReleases)).Scan(ctx, &recordingSurvivors); err != nil {
			return preview, fmt.Errorf("preview library artist deletion: read surviving recordings: %w", err)
		}
	}
	survivingTracks, survivingRecordings := map[uuid.UUID]struct{}{}, map[uuid.UUID]struct{}{}
	for _, link := range trackSurvivors {
		dependency.SurvivingTrackLinks = appendDeletionLinkJSON(dependency.SurvivingTrackLinks, link)
		survivingTracks[link.TrackID] = struct{}{}
	}
	for _, link := range recordingSurvivors {
		dependency.SurvivingRecordingLinks = appendDeletionLinkJSON(dependency.SurvivingRecordingLinks, link)
		if link.RecordingID != nil {
			survivingRecordings[*link.RecordingID] = struct{}{}
		}
	}
	for _, id := range dependency.CandidateTrackIDs {
		if _, ok := survivingTracks[id]; !ok {
			dependency.OrphanTrackIDs = append(dependency.OrphanTrackIDs, id)
		}
	}
	for _, id := range dependency.CandidateRecordingIDs {
		if _, ok := survivingRecordings[id]; !ok {
			dependency.OrphanRecordingIDs = append(dependency.OrphanRecordingIDs, id)
		}
	}
	if err := readLibraryDeletionOrphanIdentities(ctx, tx, &dependency); err != nil {
		return preview, err
	}
	for _, reference := range references {
		raw, err := json.Marshal(reference)
		if err != nil {
			return preview, fmt.Errorf("preview library artist deletion: encode artist reference: %w", err)
		}
		dependency.ArtistReferenceProofs = append(dependency.ArtistReferenceProofs, raw)
	}
	publications := make([]TrackPublication, 0, len(publicationByPosition))
	for _, id := range positions {
		if publication, ok := publicationByPosition[id]; ok {
			publications = append(publications, publication)
		}
	}
	blockers := make([]string, 0)
	for _, releaseID := range targetReleases {
		release, err := readLibraryDeletionPreview(ctx, tx, "release", releaseID, uuid.Nil)
		if err != nil {
			return preview, err
		}
		blockers = append(blockers, release.Blockers...)
	}
	dependencyDigest, err := libraryDeletionDependencyDigest(dependency)
	if err != nil {
		return preview, fmt.Errorf("preview library artist deletion: encode dependencies: %w", err)
	}
	scope := LibraryDeletionScope{
		Kind: "artist", ArtistID: artistID, ArtistReleaseIDs: targetReleases,
		ArtistReferenceReleaseIDs: referenceReleases,
		WarningReleaseIDs:         warningReleases, PublicationIDs: positions,
		ManualReceiptRequestIDs: receiptIDs, DependencyDigest: dependencyDigest,
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return preview, fmt.Errorf("preview library artist deletion: encode scope: %w", err)
	}
	scopeHash := sha256.Sum256(scopeJSON)
	return LibraryDeletionPreview{
		Scope: scope, ScopeDigest: hex.EncodeToString(scopeHash[:]), Title: artistName,
		ArtistReleases: includedReleases, CoartistWarnings: warnings,
		PositionIDs: positions, Publications: publications, Blockers: blockers,
		RecordingIDs: slices.Clone(dependency.OrphanRecordingIDs), RecordingCount: len(dependency.OrphanRecordingIDs),
		orphanTrackIDs: slices.Clone(dependency.OrphanTrackIDs), dependencies: dependency,
	}, nil
}

func appendDeletionLinkJSON(values []json.RawMessage, value libraryDeletionPositionTrackLink) []json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return values
	}
	return append(values, raw)
}

func lockLibraryDeletionArtistReferences(ctx context.Context, tx bun.IDB, artistID uuid.UUID) error {
	var releaseCredits, releasePrimaries, positionPrimaries, trackCredits, recordingCredits []uuid.UUID
	if err := tx.NewRaw(`SELECT release_id FROM release_artist WHERE artist_id=? ORDER BY release_id,position FOR UPDATE`, artistID).Scan(ctx, &releaseCredits); err != nil {
		return fmt.Errorf("lock library deletion artist release credits: %w", err)
	}
	if err := tx.NewRaw(`SELECT id FROM local_release WHERE primary_artist_id=? ORDER BY id FOR UPDATE`, artistID).Scan(ctx, &releasePrimaries); err != nil {
		return fmt.Errorf("lock library deletion artist primary releases: %w", err)
	}
	if err := tx.NewRaw(`SELECT id FROM release_track WHERE primary_artist_id=? ORDER BY id FOR UPDATE`, artistID).Scan(ctx, &positionPrimaries); err != nil {
		return fmt.Errorf("lock library deletion artist primary positions: %w", err)
	}
	var collections []uuid.UUID
	if err := tx.NewRaw(`SELECT collection_id FROM collection_artist WHERE artist_id=? ORDER BY collection_id,position FOR UPDATE`, artistID).Scan(ctx, &collections); err != nil {
		return fmt.Errorf("lock library deletion artist collection references: %w", err)
	}
	if err := tx.NewRaw(`SELECT track_id FROM track_artist WHERE artist_id=? ORDER BY track_id,position FOR UPDATE`, artistID).Scan(ctx, &trackCredits); err != nil {
		return fmt.Errorf("lock library deletion artist track credits: %w", err)
	}
	if err := tx.NewRaw(`SELECT recording_id FROM recording_artist WHERE artist_id=? ORDER BY recording_id,position FOR UPDATE`, artistID).Scan(ctx, &recordingCredits); err != nil {
		return fmt.Errorf("lock library deletion artist recording credits: %w", err)
	}
	return nil
}
