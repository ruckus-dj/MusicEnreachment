package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// IncomingLocation describes one current inventory location for a file
// variant. SHA256 is deliberately not required to identify incoming files.
type IncomingLocation struct {
	RootID       uuid.UUID
	RelativePath string
	SizeBytes    int64
	Mtime        string
}

// IncomingFile is the service-layer snapshot needed for pure grouping. Tags
// are authoritative captured analysis results; grouping performs no source IO.
type IncomingFile struct {
	VariantID         uuid.UUID
	CaptureAnalysisID uuid.UUID
	Locations         []IncomingLocation
	Tags              map[string][]string
}

type IncomingGroup struct {
	ID          uuid.UUID
	Revision    string
	Members     []uuid.UUID
	Diagnostics []IncomingGroupingDiagnostic
}

type IncomingGroupingDiagnostic struct {
	VariantID uuid.UUID
	Code      string
	Values    []string
}

type incomingTagFields struct {
	Album       []string `json:"album"`
	Artist      []string `json:"artist"`
	Date        []string `json:"date"`
	Catalog     []string `json:"catalog"`
	Country     []string `json:"country"`
	ReleaseMBID []string `json:"release_mbid"`
}

// sufficientForTagGroup reports whether the captured fields carry enough
// album, artist, and date/catalog evidence to group by tags instead of falling
// back to folder location. Artist is the effective artist already resolved by
// incomingFields (album artist when present, otherwise artist).
func (fields incomingTagFields) sufficientForTagGroup() bool {
	return fields.Album != nil && fields.Artist != nil && (fields.Date != nil || fields.Catalog != nil)
}

var incomingTagAliases = map[string]string{
	"ALBUM": "album", "ARTIST": "artist", "ALBUMARTIST": "album_artist", "ALBUM ARTIST": "album_artist", "ALBUM_ARTIST": "album_artist",
	"DATE": "date", "YEAR": "date", "ALBUMDATE": "date",
	"CATALOGNUMBER": "catalog", "CATALOG": "catalog", "CATALOG NO": "catalog",
	"RELEASECOUNTRY": "country", "RELEASE_COUNTRY": "country", "MUSICBRAINZ ALBUM RELEASE COUNTRY": "country", "MUSICBRAINZ_RELEASECOUNTRY": "country", "MUSICBRAINZ_ALBUM_RELEASE_COUNTRY": "country",
	"RELEASEMBID": "release_mbid", "MUSICBRAINZ_ALBUMID": "release_mbid", "MUSICBRAINZ ALBUM ID": "release_mbid", "MUSICBRAINZ_ALBUM_ID": "release_mbid", "MUSICBRAINZ_RELEASEID": "release_mbid",
	"MUSICBRAINZ RELEASE ID": "release_mbid", "MUSICBRAINZ_RELEASE_ID": "release_mbid", "MUSICBRAINZ_RELEASEMBID": "release_mbid",
}

type incomingGroupKey struct {
	Kind    string            `json:"kind"`
	Root    string            `json:"root,omitempty"`
	Folder  string            `json:"folder,omitempty"`
	Variant string            `json:"variant,omitempty"`
	Fields  incomingTagFields `json:"fields,omitempty"`
	MBID    string            `json:"mbid,omitempty"`
}

// GroupIncomingFiles builds a deterministic, derived grouping from current
// file/analysis/location identities and captured tags.
func GroupIncomingFiles(files []IncomingFile) ([]IncomingGroup, error) {
	keys := make(map[string][]uuid.UUID)
	diagnostics := make(map[uuid.UUID][]IncomingGroupingDiagnostic)
	seenVariants := make(map[uuid.UUID]bool, len(files))
	for _, file := range files {
		if file.VariantID == uuid.Nil {
			return nil, fmt.Errorf("incoming file variant ID is required")
		}
		if seenVariants[file.VariantID] {
			return nil, fmt.Errorf("incoming file variant %s is duplicated", file.VariantID)
		}
		seenVariants[file.VariantID] = true
		if file.CaptureAnalysisID == uuid.Nil {
			return nil, fmt.Errorf("incoming file capture analysis ID is required")
		}
		if len(file.Locations) == 0 {
			return nil, fmt.Errorf("incoming file %s has no current location", file.VariantID)
		}
		fields := incomingFields(file.Tags)
		mbids, invalid := incomingMBIDs(fields.ReleaseMBID)
		// MBID is a grouping selector, not an identity field to compare again.
		// In particular, malformed IDs must not accidentally partition fallback
		// tag groups after being ignored as diagnostics.
		fields.ReleaseMBID = nil
		if len(invalid) > 0 {
			diagnostics[file.VariantID] = append(diagnostics[file.VariantID], IncomingGroupingDiagnostic{VariantID: file.VariantID, Code: "invalid_release_mbid", Values: invalid})
		}
		if len(mbids) > 1 {
			diagnostics[file.VariantID] = append(diagnostics[file.VariantID], IncomingGroupingDiagnostic{VariantID: file.VariantID, Code: "multiple_release_mbids", Values: mbids})
		}
		keysForFile := make(map[string]incomingGroupKey)
		for _, location := range file.Locations {
			if err := validateIncomingLocation(location); err != nil {
				return nil, fmt.Errorf("incoming file %s location: %w", file.VariantID, err)
			}
			var key incomingGroupKey
			switch {
			case len(mbids) == 1:
				key = incomingGroupKey{Kind: "mbid", MBID: mbids[0], Fields: fields}
			case len(mbids) > 1:
				key = incomingGroupKey{Kind: "unresolved_mbid", Variant: file.VariantID.String()}
			case fields.sufficientForTagGroup():
				fields.Artist = incomingEffectiveArtist(file.Tags)
				key = incomingGroupKey{Kind: "tags", Fields: fields}
			default:
				key = incomingFolderKey(file.VariantID, location)
			}
			encoded, err := json.Marshal(key)
			if err != nil { // Structs above are JSON-safe; retain a defensive boundary.
				return nil, fmt.Errorf("encode incoming grouping key: %w", err)
			}
			keysForFile[string(encoded)] = key
		}
		if len(keysForFile) > 1 && len(mbids) != 1 && !fields.sufficientForTagGroup() {
			// Multiple fallback folders cannot establish which location owns the
			// variant. Keep it isolated rather than arbitrarily selecting a path.
			keysForFile = map[string]incomingGroupKey{}
			key := incomingGroupKey{Kind: "ambiguous_locations", Variant: file.VariantID.String()}
			encoded, _ := json.Marshal(key)
			keysForFile[string(encoded)] = key
			diagnostics[file.VariantID] = append(diagnostics[file.VariantID], IncomingGroupingDiagnostic{VariantID: file.VariantID, Code: "multiple_fallback_folders"})
		}
		for encoded := range keysForFile {
			keys[encoded] = append(keys[encoded], file.VariantID)
		}
	}

	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	filesByVariant := incomingFilesByVariant(files)
	groups := make([]IncomingGroup, 0, len(orderedKeys))
	for _, encoded := range orderedKeys {
		members := uniqueUUIDs(keys[encoded])
		sort.Slice(members, func(i, j int) bool { return members[i].String() < members[j].String() })
		var key incomingGroupKey
		if err := json.Unmarshal([]byte(encoded), &key); err != nil {
			return nil, fmt.Errorf("decode incoming grouping key: %w", err)
		}
		id := deterministicIncomingGroupID(key)
		revision, err := incomingGroupRevision(members, filesByVariant)
		if err != nil {
			return nil, err
		}
		group := IncomingGroup{ID: id, Revision: revision, Members: members}
		for _, member := range members {
			group.Diagnostics = append(group.Diagnostics, diagnostics[member]...)
		}
		sortIncomingDiagnostics(group.Diagnostics)
		groups = append(groups, group)
	}
	return groups, nil
}

func incomingFields(tags map[string][]string) incomingTagFields {
	values := make(map[string][]string)
	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := tags[name]
		canonical, ok := incomingTagAliases[strings.ToUpper(strings.TrimSpace(name))]
		if !ok {
			continue
		}
		for _, value := range raw {
			normalized := normalizeIncomingTag(value)
			if normalized != "" {
				values[canonical] = append(values[canonical], normalized)
			}
		}
		values[canonical] = uniqueStrings(values[canonical])
	}
	albumArtist := values["album_artist"]
	if albumArtist == nil {
		albumArtist = values["artist"]
	}
	return incomingTagFields{Album: values["album"], Artist: albumArtist, Date: values["date"], Catalog: values["catalog"], Country: values["country"], ReleaseMBID: values["release_mbid"]}
}

func incomingEffectiveArtist(tags map[string][]string) []string {
	fields := incomingFields(tags)
	return fields.Artist
}

func normalizeIncomingTag(value string) string {
	return cases.Fold().String(norm.NFC.String(strings.TrimSpace(value)))
}

func incomingMBIDs(values []string) (valid, invalid []string) {
	seenValid, seenInvalid := make(map[string]bool), make(map[string]bool)
	for _, value := range values {
		id, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil {
			if !seenInvalid[value] {
				invalid = append(invalid, value)
				seenInvalid[value] = true
			}
			continue
		}
		canonical := id.String()
		if !seenValid[canonical] {
			valid = append(valid, canonical)
			seenValid[canonical] = true
		}
	}
	sort.Strings(valid)
	return valid, invalid
}

func incomingFolderKey(variant uuid.UUID, location IncomingLocation) incomingGroupKey {
	folder := path.Dir(strings.TrimPrefix(path.Clean("/"+location.RelativePath), "/"))
	if folder == "." {
		folder = ""
	}
	if location.RootID == uuid.Nil {
		return incomingGroupKey{Kind: "folder", Variant: variant.String()}
	}
	return incomingGroupKey{Kind: "folder", Root: location.RootID.String(), Folder: folder}
}

// validateIncomingLocation rejects locations whose relative path cannot be
// resolved inside its root. An empty or absolute path and any path that escapes
// the root via ".." are invalid rather than silently normalized by path.Clean.
func validateIncomingLocation(location IncomingLocation) error {
	if location.RelativePath == "" {
		return fmt.Errorf("relative path is required")
	}
	if path.IsAbs(location.RelativePath) {
		return fmt.Errorf("relative path must not be absolute")
	}
	cleaned := path.Clean(location.RelativePath)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("relative path escapes its root")
	}
	return nil
}

func deterministicIncomingGroupID(key incomingGroupKey) uuid.UUID {
	encoded, _ := json.Marshal(key)
	hash := sha256.Sum256(append([]byte("melotrove:incoming-group:v1:"), encoded...))
	var id uuid.UUID
	copy(id[:], hash[:16])
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

type incomingMemberIdentity struct {
	Variant   string             `json:"variant"`
	Analysis  string             `json:"analysis"`
	Locations []IncomingLocation `json:"locations"`
}

func incomingFilesByVariant(files []IncomingFile) map[uuid.UUID]IncomingFile {
	byID := make(map[uuid.UUID]IncomingFile, len(files))
	for _, file := range files {
		byID[file.VariantID] = file
	}
	return byID
}

func incomingGroupRevision(members []uuid.UUID, filesByVariant map[uuid.UUID]IncomingFile) (string, error) {
	identities := make([]incomingMemberIdentity, 0, len(members))
	for _, member := range members {
		file, ok := filesByVariant[member]
		if !ok {
			continue
		}
		locations := uniqueIncomingLocations(file.Locations)
		sort.Slice(locations, func(i, j int) bool {
			a, b := locations[i], locations[j]
			if a.RootID != b.RootID {
				return a.RootID.String() < b.RootID.String()
			}
			if a.RelativePath != b.RelativePath {
				return a.RelativePath < b.RelativePath
			}
			if a.SizeBytes != b.SizeBytes {
				return a.SizeBytes < b.SizeBytes
			}
			return a.Mtime < b.Mtime
		})
		identities = append(identities, incomingMemberIdentity{Variant: member.String(), Analysis: file.CaptureAnalysisID.String(), Locations: locations})
	}
	encoded, err := json.Marshal(identities)
	if err != nil {
		return "", fmt.Errorf("encode incoming group revision: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func uniqueIncomingLocations(locations []IncomingLocation) []IncomingLocation {
	seen := make(map[IncomingLocation]bool, len(locations))
	result := make([]IncomingLocation, 0, len(locations))
	for _, location := range locations {
		if !seen[location] {
			seen[location] = true
			result = append(result, location)
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// sortIncomingDiagnostics orders diagnostics deterministically by variant then
// code, independent of the order groups or members were visited.
func sortIncomingDiagnostics(diagnostics []IncomingGroupingDiagnostic) {
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].VariantID != diagnostics[j].VariantID {
			return diagnostics[i].VariantID.String() < diagnostics[j].VariantID.String()
		}
		return diagnostics[i].Code < diagnostics[j].Code
	})
}

// incomingDiagnosticsPool indexes every diagnostic in a grouping snapshot by
// the variant it belongs to.
func incomingDiagnosticsPool(groups []IncomingGroup) map[uuid.UUID][]IncomingGroupingDiagnostic {
	pool := make(map[uuid.UUID][]IncomingGroupingDiagnostic)
	for _, group := range groups {
		for _, diagnostic := range group.Diagnostics {
			pool[diagnostic.VariantID] = append(pool[diagnostic.VariantID], diagnostic)
		}
	}
	return pool
}

// applyIncomingDiagnostics rewrites each group's diagnostics from its current
// membership so merge/split/move never retain diagnostics of variants the group
// no longer contains and never drop diagnostics of variants it gained.
func applyIncomingDiagnostics(groups []IncomingGroup, pool map[uuid.UUID][]IncomingGroupingDiagnostic) {
	for i := range groups {
		var diagnostics []IncomingGroupingDiagnostic
		for _, member := range groups[i].Members {
			diagnostics = append(diagnostics, pool[member]...)
		}
		sortIncomingDiagnostics(diagnostics)
		groups[i].Diagnostics = diagnostics
	}
}

func uniqueUUIDs(values []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(values))
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

// IncomingGroupDraft is an in-memory correction proposal. These helpers never
// persist or mutate their input groups.
type IncomingGroupDraft struct {
	BaseRevision string
	Groups       []IncomingGroup
}

// IncomingGroupsRevision returns a stable aggregate revision for a grouping
// snapshot, independent of group order.
func IncomingGroupsRevision(groups []IncomingGroup) (string, error) {
	ordered := cloneIncomingGroups(groups)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID.String() < ordered[j].ID.String() })
	identities := make([]struct {
		ID       string   `json:"id"`
		Revision string   `json:"revision"`
		Members  []string `json:"members"`
	}, len(ordered))
	for i, group := range ordered {
		identities[i].ID = group.ID.String()
		identities[i].Revision = group.Revision
		identities[i].Members = uuidStrings(group.Members)
	}
	encoded, err := json.Marshal(identities)
	if err != nil {
		return "", fmt.Errorf("encode incoming groups revision: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func validateIncomingGroupsRevision(groups []IncomingGroup, baseRevision string) error {
	if baseRevision == "" {
		return fmt.Errorf("base group revision is required")
	}
	actual, err := IncomingGroupsRevision(groups)
	if err != nil {
		return err
	}
	if actual != baseRevision {
		return fmt.Errorf("incoming groups revision is stale")
	}
	return nil
}

func DraftIncomingMerge(groups []IncomingGroup, baseRevision string, groupIDs ...uuid.UUID) (IncomingGroupDraft, error) {
	if err := validateIncomingGroupsRevision(groups, baseRevision); err != nil {
		return IncomingGroupDraft{}, err
	}
	diagnostics := incomingDiagnosticsPool(groups)
	selected, rest, err := selectIncomingGroups(groups, groupIDs)
	if err != nil {
		return IncomingGroupDraft{}, err
	}
	if len(selected) < 2 {
		return IncomingGroupDraft{}, fmt.Errorf("merge requires at least two groups")
	}
	merged := selected[0]
	merged.Members = nil
	for _, group := range selected {
		merged.Members = append(merged.Members, group.Members...)
	}
	merged.Members = uniqueUUIDs(merged.Members)
	sort.Slice(merged.Members, func(i, j int) bool { return merged.Members[i].String() < merged.Members[j].String() })
	merged.ID = deterministicIncomingGroupID(incomingGroupKey{Kind: "manual_merge", Variant: strings.Join(uuidStrings(merged.Members), ",")})
	rest = append(rest, merged)
	applyIncomingDiagnostics(rest, diagnostics)
	return IncomingGroupDraft{BaseRevision: baseRevision, Groups: rest}, nil
}

func DraftIncomingSplit(groups []IncomingGroup, baseRevision string, groupID uuid.UUID, memberIDs []uuid.UUID) (IncomingGroupDraft, error) {
	if err := validateIncomingGroupsRevision(groups, baseRevision); err != nil {
		return IncomingGroupDraft{}, err
	}
	diagnostics := incomingDiagnosticsPool(groups)
	selected, rest, err := selectIncomingGroups(groups, []uuid.UUID{groupID})
	if err != nil {
		return IncomingGroupDraft{}, err
	}
	selectedMembers := make(map[uuid.UUID]bool, len(memberIDs))
	for _, id := range memberIDs {
		selectedMembers[id] = true
	}
	left, right := make([]uuid.UUID, 0), make([]uuid.UUID, 0)
	for _, id := range selected[0].Members {
		if selectedMembers[id] {
			right = append(right, id)
		} else {
			left = append(left, id)
		}
	}
	if len(left) == 0 || len(right) == 0 || len(right) != len(selectedMembers) {
		return IncomingGroupDraft{}, fmt.Errorf("split members must be a non-empty proper subset of the group")
	}
	for _, part := range [][]uuid.UUID{left, right} {
		group := selected[0]
		group.Members = append([]uuid.UUID(nil), part...)
		group.ID = deterministicIncomingGroupID(incomingGroupKey{Kind: "manual_split", Variant: strings.Join(uuidStrings(part), ",")})
		rest = append(rest, group)
	}
	applyIncomingDiagnostics(rest, diagnostics)
	return IncomingGroupDraft{BaseRevision: baseRevision, Groups: rest}, nil
}

func DraftIncomingMove(groups []IncomingGroup, baseRevision string, memberIDs []uuid.UUID, targetGroupID uuid.UUID) (IncomingGroupDraft, error) {
	if err := validateIncomingGroupsRevision(groups, baseRevision); err != nil {
		return IncomingGroupDraft{}, err
	}
	diagnostics := incomingDiagnosticsPool(groups)
	if len(memberIDs) == 0 {
		return IncomingGroupDraft{}, fmt.Errorf("move requires members")
	}
	result := cloneIncomingGroups(groups)
	target := -1
	for i := range result {
		if result[i].ID == targetGroupID {
			target = i
			break
		}
	}
	if target < 0 {
		return IncomingGroupDraft{}, fmt.Errorf("target group not found")
	}
	selected := make(map[uuid.UUID]bool, len(memberIDs))
	for _, id := range memberIDs {
		selected[id] = true
	}
	found := make(map[uuid.UUID]bool)
	for i := range result {
		kept := result[i].Members[:0]
		for _, id := range result[i].Members {
			if selected[id] {
				found[id] = true
			} else {
				kept = append(kept, id)
			}
		}
		result[i].Members = kept
	}
	if len(found) != len(selected) {
		return IncomingGroupDraft{}, fmt.Errorf("move member not found")
	}
	result[target].Members = append(result[target].Members, memberIDs...)
	result[target].Members = uniqueUUIDs(result[target].Members)
	sort.Slice(result[target].Members, func(i, j int) bool { return result[target].Members[i].String() < result[target].Members[j].String() })
	filtered := result[:0]
	for _, group := range result {
		if len(group.Members) > 0 {
			filtered = append(filtered, group)
		}
	}
	applyIncomingDiagnostics(filtered, diagnostics)
	return IncomingGroupDraft{BaseRevision: baseRevision, Groups: filtered}, nil
}

func selectIncomingGroups(groups []IncomingGroup, ids []uuid.UUID) ([]IncomingGroup, []IncomingGroup, error) {
	wanted := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	selected, rest := make([]IncomingGroup, 0, len(wanted)), make([]IncomingGroup, 0, len(groups))
	for _, group := range groups {
		if wanted[group.ID] {
			selected = append(selected, cloneIncomingGroups([]IncomingGroup{group})[0])
			delete(wanted, group.ID)
		} else {
			rest = append(rest, cloneIncomingGroups([]IncomingGroup{group})[0])
		}
	}
	if len(wanted) > 0 {
		return nil, nil, fmt.Errorf("group reference not found")
	}
	return selected, rest, nil
}

func cloneIncomingGroups(groups []IncomingGroup) []IncomingGroup {
	result := make([]IncomingGroup, len(groups))
	for i, group := range groups {
		result[i] = group
		result[i].Members = append([]uuid.UUID(nil), group.Members...)
		result[i].Diagnostics = append([]IncomingGroupingDiagnostic(nil), group.Diagnostics...)
	}
	return result
}

func uuidStrings(ids []uuid.UUID) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = id.String()
	}
	sort.Strings(result)
	return result
}
