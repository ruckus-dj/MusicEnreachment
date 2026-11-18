import { type ReactNode, useId, useState } from "react";
import type {
  MatchingReviewCandidate,
  MatchingReviewLayoutPosition,
  MatchingReviewRelease,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { MatchingDropZone, MatchingFileDragHandle } from "./MatchingDnD";
import { matchingBoardMessages } from "./matchingBoardMessages";
import "./matching-board.css";

/**
 * Saved automatic assignments. The preferred shape is the direct output of
 * `savedAutomaticAssignments()` (release-scoped member id to track id). A map
 * nested by provider release id is also accepted.
 */
export type MatchingBoardAutomaticAssignments =
  | Readonly<Record<string, string>>
  | Readonly<Record<string, Readonly<Record<string, string>>>>;

/**
 * Saved per-track confidence. The preferred shape is release-scoped
 * member id to track id to a [0, 1] score. A map nested by provider release id
 * is also accepted. Only finite scores are rendered.
 */
export type MatchingBoardSavedScores =
  | Readonly<Record<string, Readonly<Record<string, number>>>>
  | Readonly<
      Record<string, Readonly<Record<string, Readonly<Record<string, number>>>>>
    >;

export type MatchingBoardSource = {
  id: string;
  label: string;
  detail?: string;
  path?: string;
  available?: boolean;
};

export type MatchingBoardSavedReference = {
  memberId: string;
  trackId: string;
  confidence?: number;
};

export type MatchingBoardPositionContext = {
  position: MatchingReviewLayoutPosition;
  assigned: readonly MatchingBoardSource[];
  saved: readonly MatchingBoardSavedReference[];
};

export type MatchingBoardProps = {
  groupId: string;
  release: MatchingReviewRelease;
  members: readonly string[];
  /** Every group member, including already assigned files. */
  sources: readonly MatchingBoardSource[];
  /** Manual draft assignments, member id to selected release track id. */
  assignments: Readonly<Record<string, string>>;
  automaticAssignments?: MatchingBoardAutomaticAssignments | null;
  savedScores?: MatchingBoardSavedScores | null;
  disabled?: boolean;
  inspectedTrackId?: string;
  selectedSourceId?: string | null;
  defaultSelectedSourceId?: string | null;
  onSelectedSourceChange?: (memberId: string | null) => void;
  onAssign: (memberId: string, trackId: string) => void;
  onUnassign?: (memberId: string) => void;
  onInspectPosition?: (trackId: string) => void;
  onInspectSource?: (memberId: string) => void;
  /**
   * Paired inspection of one file and one position. When provided it replaces
   * the separate source/position callbacks so a single pair can be inspected
   * without the two handlers racing each other.
   */
  onInspectPair?: (memberId: string, trackId: string) => void;
  /** Per-position contextual content, for example a collapsed tag-override editor. */
  renderPositionMeta?: (context: MatchingBoardPositionContext) => ReactNode;
  renderSourceDetails?: (source: MatchingBoardSource) => ReactNode;
  className?: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/**
 * Resolve the release-scoped portion of an automatic assignment map. When the
 * map is nested, the provider release id is the primary key and the readable
 * release id is the fallback.
 */
export function matchingBoardAutomaticForRelease(
  automaticAssignments: MatchingBoardAutomaticAssignments | null | undefined,
  release: MatchingReviewRelease,
): Readonly<Record<string, string>> {
  if (!automaticAssignments) return {};
  const nested =
    automaticAssignments[release.provider_release_id] ??
    automaticAssignments[release.release_id];
  return isRecord(nested)
    ? (nested as Readonly<Record<string, string>>)
    : (automaticAssignments as Readonly<Record<string, string>>);
}

/**
 * Resolve the release-scoped portion of a score map. A nested map is
 * recognized only when its release entry contains member records; a scoped map
 * keeps track-score records at the first level.
 */
export function matchingBoardScoresForRelease(
  savedScores: MatchingBoardSavedScores | null | undefined,
  release: MatchingReviewRelease,
): Readonly<Record<string, Readonly<Record<string, number>>>> {
  if (!savedScores) return {};
  const nested =
    savedScores[release.provider_release_id] ?? savedScores[release.release_id];
  if (isRecord(nested)) {
    const values = Object.values(nested);
    if (values.length === 0 || values.every(isRecord)) {
      return nested as Readonly<
        Record<string, Readonly<Record<string, number>>>
      >;
    }
  }
  return savedScores as Readonly<
    Record<string, Readonly<Record<string, number>>>
  >;
}

export function matchingBoardSavedTrack(
  automaticAssignments: MatchingBoardAutomaticAssignments | null | undefined,
  release: MatchingReviewRelease,
  memberId: string,
): string | undefined {
  return matchingBoardAutomaticForRelease(automaticAssignments, release)[
    memberId
  ];
}

export function matchingBoardSavedScore(
  savedScores: MatchingBoardSavedScores | null | undefined,
  release: MatchingReviewRelease,
  memberId: string,
  trackId: string,
): number | undefined {
  const value = matchingBoardScoresForRelease(savedScores, release)[memberId]?.[
    trackId
  ];
  return typeof value === "number" &&
    Number.isFinite(value) &&
    value >= 0 &&
    value <= 1
    ? value
    : undefined;
}

export function matchingBoardPercent(value: number) {
  return `${Math.round(value * 100)}%`;
}

/**
 * Project saved candidate edges for exactly one release into the board's
 * member-to-track score map. Scores are real saved [0, 1] values; missing or
 * out-of-range values are omitted rather than inferred.
 */
export function matchingBoardScoresFromCandidates(
  candidates: readonly MatchingReviewCandidate[] | null | undefined,
  release: MatchingReviewRelease,
): Readonly<Record<string, Readonly<Record<string, number>>>> {
  const tracks = new Set(
    (release.positions ?? []).map((position) => position.track_id),
  );
  const releaseIds = new Set([release.provider_release_id, release.release_id]);
  const scores: Record<string, Record<string, number>> = {};
  for (const candidate of candidates ?? []) {
    if (!releaseIds.has(candidate.release_id)) continue;
    for (const position of candidate.positions ?? []) {
      if (!tracks.has(position.track_id)) continue;
      const confidence = position.assignment_confidence;
      if (
        typeof confidence !== "number" ||
        !Number.isFinite(confidence) ||
        confidence < 0 ||
        confidence > 1
      ) {
        continue;
      }
      let memberScores = scores[candidate.file_member_id];
      if (!memberScores) {
        memberScores = {};
        scores[candidate.file_member_id] = memberScores;
      }
      const existing = memberScores[position.track_id];
      memberScores[position.track_id] =
        existing === undefined ? confidence : Math.max(existing, confidence);
    }
  }
  return scores;
}

function sortedPositions(release: MatchingReviewRelease) {
  return [...(release.positions ?? [])].sort(
    (left, right) =>
      left.medium - right.medium || left.position - right.position,
  );
}

export function MatchingBoard({
  groupId,
  release,
  members,
  sources,
  assignments,
  automaticAssignments,
  savedScores,
  disabled = false,
  inspectedTrackId,
  selectedSourceId,
  defaultSelectedSourceId,
  onSelectedSourceChange,
  onAssign,
  onUnassign,
  onInspectPosition,
  onInspectSource,
  onInspectPair,
  renderPositionMeta,
  renderSourceDetails,
  className,
}: MatchingBoardProps) {
  const { locale } = useI18n();
  const copy = matchingBoardMessages[locale];
  const baseId = useId();
  const positionsTitleId = `${baseId}-positions`;
  const sourcesTitleId = `${baseId}-sources`;
  const [uncontrolledSelection, setUncontrolledSelection] = useState<
    string | null
  >(defaultSelectedSourceId ?? null);
  const selectionControlled = selectedSourceId !== undefined;
  const selection = selectionControlled
    ? (selectedSourceId ?? null)
    : uncontrolledSelection;

  function setSelection(next: string | null) {
    if (!selectionControlled) setUncontrolledSelection(next);
    onSelectedSourceChange?.(next);
  }

  const sourceById = new Map(sources.map((source) => [source.id, source]));
  const sourceFor = (memberId: string): MatchingBoardSource =>
    sourceById.get(memberId) ?? { id: memberId, label: memberId };
  const positions = sortedPositions(release);
  const trackIds = new Set(positions.map((position) => position.track_id));
  const mediaCount = new Set(positions.map((position) => position.medium)).size;
  const automaticByMember = matchingBoardAutomaticForRelease(
    automaticAssignments,
    release,
  );
  const automaticByTrack = new Map<string, string[]>();
  for (const memberId of members) {
    const trackId = automaticByMember[memberId];
    if (!trackId || !trackIds.has(trackId)) continue;
    const current = automaticByTrack.get(trackId);
    if (current) current.push(memberId);
    else automaticByTrack.set(trackId, [memberId]);
  }
  const freeMembers = members.filter((memberId) => !assignments[memberId]);
  const selectedSource = selection ? sourceById.get(selection) : undefined;
  const panelClass = `matching-board-panel${className ? ` ${className}` : ""}`;

  function unassign(memberId: string) {
    if (onUnassign) onUnassign(memberId);
    else onAssign(memberId, "");
  }

  function inspectAt(memberId: string, trackId: string) {
    if (onInspectPair) {
      onInspectPair(memberId, trackId);
      return;
    }
    onInspectSource?.(memberId);
    onInspectPosition?.(trackId);
  }

  function scoreFor(memberId: string, trackId: string) {
    return matchingBoardSavedScore(savedScores, release, memberId, trackId);
  }

  return (
    <>
      <section
        data-matching-positions
        aria-labelledby={positionsTitleId}
        className={`${panelClass} matching-board-positions`}
      >
        <h2 id={positionsTitleId} className="matching-board-heading">
          {copy.positionsHeading(release.title)}
        </h2>
        <p className="matching-board-summary">
          {release.tracklist_complete
            ? copy.completeTrackList
            : copy.incompleteTrackList}{" "}
          · {copy.positionCount(positions.length)}
        </p>
        <p className="matching-board-instructions">
          {copy.positionInstructions}
        </p>
        <ol className="matching-board-position-list">
          {positions.map((position) => {
            const number =
              position.displayed_number || `${position.position + 1}`;
            const label = copy.positionLabel(
              position.medium + 1,
              number,
              position.title,
            );
            const occupants = members.filter(
              (memberId) => assignments[memberId] === position.track_id,
            );
            const automaticMembers =
              automaticByTrack.get(position.track_id) ?? [];
            const savedOnly = automaticMembers.filter(
              (memberId) => assignments[memberId] !== position.track_id,
            );
            const inspected = inspectedTrackId === position.track_id;
            const selectedAssignedHere =
              selection !== null && occupants.includes(selection);
            const selectedAvailable =
              selection !== null && selectedSource?.available !== false;
            const canAddSelected =
              Boolean(selection) &&
              selectedAvailable &&
              !selectedAssignedHere &&
              !disabled;
            const addLabel = !selection
              ? copy.chooseSourceFirst
              : selectedAssignedHere
                ? copy.alreadyAssignedHere(selectedSource?.label ?? "", label)
                : selectedSource
                  ? copy.assignSelectedTo(selectedSource.label, label)
                  : copy.chooseSourceFirst;

            return (
              <li
                key={position.track_id}
                className="matching-board-position"
                aria-label={label}
                data-inspected={inspected || undefined}
              >
                <MatchingDropZone
                  groupId={groupId}
                  trackId={position.track_id}
                  trackName={label}
                  dropHint={copy.dropHint}
                  dropTargetLabel={copy.dropTarget(label)}
                  disabled={disabled}
                  onAssign={(fileId) => {
                    onAssign(fileId, position.track_id);
                    if (fileId === selection) setSelection(null);
                    return true;
                  }}
                >
                  <div className="matching-board-position-head">
                    {onInspectPosition ? (
                      <button
                        type="button"
                        className="matching-board-position-title"
                        aria-pressed={inspected}
                        aria-label={copy.inspectPosition(label)}
                        title={position.title}
                        onClick={() => onInspectPosition(position.track_id)}
                      >
                        <span className="matching-board-position-number">
                          {number}
                        </span>
                        <span className="matching-board-position-name">
                          {position.title}
                        </span>
                      </button>
                    ) : (
                      <span className="matching-board-position-title">
                        <span className="matching-board-position-number">
                          {number}
                        </span>
                        <span className="matching-board-position-name">
                          {position.title}
                        </span>
                      </span>
                    )}
                    {mediaCount > 1 ? (
                      <span className="matching-board-position-medium">
                        {copy.disc(position.medium + 1)}
                      </span>
                    ) : null}
                    <button
                      type="button"
                      className="matching-board-add"
                      disabled={!canAddSelected}
                      aria-label={addLabel}
                      title={addLabel}
                      onClick={() => {
                        if (!selection || !canAddSelected) return;
                        onAssign(selection, position.track_id);
                        setSelection(null);
                      }}
                    >
                      {copy.addSelected}
                    </button>
                  </div>
                  <div className="matching-board-assigned">
                    {occupants.map((memberId) => {
                      const source = sourceFor(memberId);
                      const confidence = scoreFor(memberId, position.track_id);
                      const savedHere =
                        automaticByMember[memberId] === position.track_id;
                      return (
                        <span
                          key={memberId}
                          className="matching-board-assigned-file"
                        >
                          <button
                            type="button"
                            className="matching-board-file-label"
                            title={source.path ?? source.label}
                            aria-label={copy.inspectSource(source.label)}
                            onClick={() =>
                              inspectAt(memberId, position.track_id)
                            }
                          >
                            {source.label}
                          </button>
                          {savedHere ? (
                            <span className="matching-board-auto-marker">
                              {copy.savedAutomaticShort}
                            </span>
                          ) : null}
                          {confidence !== undefined ? (
                            <span
                              className="matching-board-score"
                              title={copy.savedScoreLong(
                                matchingBoardPercent(confidence),
                              )}
                            >
                              {copy.savedScore(
                                matchingBoardPercent(confidence),
                              )}
                            </span>
                          ) : null}
                          <MatchingFileDragHandle
                            groupId={groupId}
                            fileId={memberId}
                            name={source.label}
                            available={!disabled && source.available !== false}
                            label={copy.dragFile(source.label)}
                          />
                          <button
                            type="button"
                            className="matching-board-remove"
                            disabled={disabled}
                            aria-label={copy.removeFile(source.label, label)}
                            onClick={() => unassign(memberId)}
                          >
                            ×
                          </button>
                        </span>
                      );
                    })}
                    {savedOnly.map((memberId) => {
                      const source = sourceFor(memberId);
                      const confidence = scoreFor(memberId, position.track_id);
                      const content = (
                        <>
                          <span className="matching-board-auto-marker">
                            {copy.savedAutomatic}
                          </span>
                          <span className="matching-board-file-label">
                            {source.label}
                          </span>
                          {confidence !== undefined ? (
                            <span
                              className="matching-board-score"
                              title={copy.savedScoreLong(
                                matchingBoardPercent(confidence),
                              )}
                            >
                              {copy.savedScore(
                                matchingBoardPercent(confidence),
                              )}
                            </span>
                          ) : null}
                        </>
                      );
                      return onInspectSource ||
                        onInspectPosition ||
                        onInspectPair ? (
                        <button
                          key={memberId}
                          type="button"
                          className="matching-board-saved-file"
                          title={source.path ?? source.label}
                          onClick={() => inspectAt(memberId, position.track_id)}
                        >
                          {content}
                        </button>
                      ) : (
                        <span
                          key={memberId}
                          className="matching-board-saved-file"
                          title={source.path ?? source.label}
                        >
                          {content}
                        </span>
                      );
                    })}
                    {!occupants.length && !savedOnly.length ? (
                      <span className="matching-board-empty">
                        {copy.noFilesAssigned}
                      </span>
                    ) : null}
                  </div>
                  {renderPositionMeta?.({
                    position,
                    assigned: occupants.map(sourceFor),
                    saved: automaticMembers.map((memberId) => ({
                      memberId,
                      trackId: position.track_id,
                      confidence: scoreFor(memberId, position.track_id),
                    })),
                  })}
                </MatchingDropZone>
              </li>
            );
          })}
        </ol>
      </section>

      <section
        data-matching-sources
        aria-labelledby={sourcesTitleId}
        className={`${panelClass} matching-board-sources`}
      >
        <h2 id={sourcesTitleId} className="matching-board-heading">
          {copy.sourcesHeading(freeMembers.length)}
        </h2>
        <p className="matching-board-instructions">
          {copy.sourcesInstructions}
        </p>
        <ul className="matching-board-source-list">
          {freeMembers.map((memberId) => {
            const source = sourceFor(memberId);
            const selected = selection === memberId;
            const available = !disabled && source.available !== false;
            return (
              <li key={memberId}>
                <div
                  className="matching-board-source"
                  data-selected={selected || undefined}
                >
                  <button
                    type="button"
                    className="matching-board-source-select"
                    aria-pressed={selected}
                    aria-label={copy.selectSource(source.label)}
                    disabled={!available}
                    title={source.path ?? source.label}
                    onClick={() => {
                      if (selected) {
                        setSelection(null);
                        return;
                      }
                      setSelection(memberId);
                      onInspectSource?.(memberId);
                    }}
                  >
                    <strong>{source.label}</strong>
                    {source.detail ? <span>{source.detail}</span> : null}
                    {source.path ? <span>{source.path}</span> : null}
                    {source.available === false ? (
                      <span>{copy.unavailable}</span>
                    ) : null}
                  </button>
                  <MatchingFileDragHandle
                    groupId={groupId}
                    fileId={memberId}
                    name={source.label}
                    available={available}
                    label={copy.dragFile(source.label)}
                  />
                </div>
                {renderSourceDetails?.(source)}
              </li>
            );
          })}
        </ul>
        {freeMembers.length === 0 ? (
          <p className="matching-board-empty">{copy.allAssigned}</p>
        ) : null}
        <p role="status" className="matching-board-source-status">
          {selectedSource
            ? copy.selectedSource(selectedSource.label)
            : copy.selectSourceHint}
        </p>
      </section>
    </>
  );
}
