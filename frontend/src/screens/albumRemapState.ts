import type { AlbumRemapSuggestion } from "../api/client";

export type AlbumRemapMappingState = {
  readonly assignments: Readonly<Record<string, string>>;
  readonly selectedSourceId: string | null;
};

export type AlbumRemapMappingAction =
  | { readonly type: "initialize"; readonly assignments: Readonly<Record<string, string>> }
  | { readonly type: "select"; readonly sourceId: string }
  | { readonly type: "assign"; readonly sourceId: string; readonly trackMbid: string }
  | { readonly type: "unassign"; readonly sourceId: string };

function withoutSource(
  assignments: Readonly<Record<string, string>>,
  sourceId: string,
): Readonly<Record<string, string>> {
  const next: Record<string, string> = {};
  for (const [assignedSourceId, trackMbid] of Object.entries(assignments)) {
    if (assignedSourceId !== sourceId) next[assignedSourceId] = trackMbid;
  }
  return next;
}

function assignSource(
  assignments: Readonly<Record<string, string>>,
  sourceId: string,
  trackMbid: string,
): Readonly<Record<string, string>> {
  const next: Record<string, string> = {};
  for (const [assignedSourceId, assignedTrackMbid] of Object.entries(assignments)) {
    if (assignedSourceId !== sourceId && assignedTrackMbid !== trackMbid)
      next[assignedSourceId] = assignedTrackMbid;
  }
  next[sourceId] = trackMbid;
  return next;
}

export function suggestedAssignments(
  suggestions: readonly AlbumRemapSuggestion[],
  sourceIds: readonly string[],
  assignableTrackMbids: readonly string[],
): Readonly<Record<string, string>> {
  const sources = new Set(sourceIds);
  const tracks = new Set(assignableTrackMbids);
  const assignments: Record<string, string> = {};
  const assignedTrackMbids = new Set<string>();
  for (const suggestion of suggestions) {
    if (
      sources.has(suggestion.source_id) &&
      tracks.has(suggestion.track_mbid) &&
      assignments[suggestion.source_id] === undefined &&
      !assignedTrackMbids.has(suggestion.track_mbid)
    ) {
      assignments[suggestion.source_id] = suggestion.track_mbid;
      assignedTrackMbids.add(suggestion.track_mbid);
    }
  }
  return assignments;
}

export function albumRemapMappingReducer(
  state: AlbumRemapMappingState,
  action: AlbumRemapMappingAction,
): AlbumRemapMappingState {
  switch (action.type) {
    case "initialize":
      return { assignments: action.assignments, selectedSourceId: null };
    case "select":
      return {
        ...state,
        selectedSourceId: state.selectedSourceId === action.sourceId ? null : action.sourceId,
      };
    case "assign":
      return {
        assignments: assignSource(state.assignments, action.sourceId, action.trackMbid),
        selectedSourceId: null,
      };
    case "unassign":
      return { ...state, assignments: withoutSource(state.assignments, action.sourceId) };
  }
}
