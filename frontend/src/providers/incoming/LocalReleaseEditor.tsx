import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";
import { createUUID } from "../../api/client/uuid";
import {
  confirmIncomingGroupLocalMatching,
  previewIncomingGroupLocalMatching,
  readLocalProviderRelease,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  LocalProviderItem,
  LocalProviderTrack,
  MatchingLocalPreview,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import {
  type LocalMatchingMessages,
  type LocalMatchingNotice,
  localMatchingMessages,
} from "../../ui/features/incoming/localMatchingMessages";
import { compareSourceFiles } from "../../ui/features/matching/sourceFileOrder";
import type { MatchingWorkspaceRelease } from "../../ui/features/matching/types";
import {
  incomingSourceFileName,
  readIncomingSourceTrackOrder,
} from "./apiMatchingSources";
import { nextIncomingConfirmOrder } from "./confirmReceipts";

type Assignment = { file_member_id: string; provider_track_id: string };
type PreviewBody = {
  provider_release_id: string;
  expected_group_revision: string;
  expected_authority_revision: number;
  assignments: Assignment[];
};
type ConfirmBody = PreviewBody & {
  preview_digest: string;
  request_id: string;
};
type PreviewState = {
  scope: string;
  body: PreviewBody;
  response: MatchingLocalPreview;
};
type PendingConfirmation = { scope: string; body: ConfirmBody; order: number };
type EditorState = {
  selectedReleaseID: string;
  release?: LocalProviderItem;
  assignments: Record<string, string>;
  assignmentDrafts: Record<string, Record<string, string>>;
  preview?: PreviewState;
  pending?: PendingConfirmation;
  receipt?: MatchingLocalPreview;
  busy?: "preview" | "confirm";
  busyRequest?: number;
  error?: string;
  readError?: string;
  notice?: LocalMatchingNotice;
  stale?: boolean;
};

const emptyEditorState = (): EditorState => ({
  selectedReleaseID: "",
  assignments: {},
  assignmentDrafts: {},
});

const noMembers: readonly string[] = [];

export type LocalMatchingStatus = "idle" | "loading" | "ready" | "error";

export interface LocalMatchingProps {
  group: IncomingGroupResponse | undefined;
  files: IncomingFileResponse[];
  authorityRevision: number | undefined;
  preferredReleaseId?: string;
  onConfirmed?: (
    receipt: MatchingLocalPreview,
    order: number,
    requestId: string,
    assignments: readonly Assignment[],
  ) => void | Promise<void>;
}

export interface LocalMatchingController {
  /** The exact Local catalog release loaded for `preferredReleaseId`. */
  release: LocalProviderItem | undefined;
  /** Pure neutral adapter for `MatchingBoard` and other workspace UI. */
  workspaceRelease: MatchingWorkspaceRelease | undefined;
  /** Group members in assignment source order (disc, track, then name). */
  files: readonly IncomingFileResponse[];
  /** Tracks of the loaded release, in catalog order. */
  tracks: readonly LocalProviderTrack[];
  /** Read-only draft, keyed by incoming file member/variant id. */
  assignments: Readonly<Record<string, string>>;
  /** Replaces the draft and invalidates any preview for the old draft. */
  setAssignments: (next: Readonly<Record<string, string>>) => void;
  /** Re-reads the intended release. Keeps drafts and a pending retry. */
  read: () => Promise<void>;
  preview: () => Promise<void>;
  confirm: () => Promise<void>;
  /** Frozen preview response, only while it belongs to the current scope. */
  previewResult: MatchingLocalPreview | undefined;
  receipt: MatchingLocalPreview | undefined;
  error: string | undefined;
  notice: LocalMatchingNotice | undefined;
  stale: boolean;
  busy: "preview" | "confirm" | undefined;
  /** An uncertain confirmation body/id is retained for an exact replay. */
  pending: boolean;
  /** The confirmation request is in flight right now. */
  confirmationPending: boolean;
  /** Release-read state; action errors are reported separately via `error`. */
  status: LocalMatchingStatus;
  canPreview: boolean;
  canConfirm: boolean;
}

/**
 * Pure projection from the Local catalog shape to the neutral matching
 * workspace release shape. Local releases have a single flat medium.
 */
function localMatchingWorkspaceRelease(
  release: LocalProviderItem | undefined,
): MatchingWorkspaceRelease | undefined {
  if (!release) return undefined;
  return {
    release_id: release.id,
    provider_release_id: release.id,
    title: release.title || "",
    tracklist_complete: true,
    positions: (release.tracks ?? []).map((track) => ({
      track_id: track.id,
      medium: 0,
      position: track.position,
      displayed_number: track.displayed_number,
      recording_id: track.recording_id,
      title: track.title_override || track.recording_title,
    })),
  };
}

function message(reason: unknown, fallback: string) {
  return sharedErrorMessage(reason, fallback) || fallback;
}

function responseMessage(
  response: { data: { detail?: string }; status: number },
  fallback: string,
  copy: LocalMatchingMessages,
) {
  return apiResponseError(response, copy.httpError(fallback, response.status))
    .message;
}

/**
 * Headless controller for exact Local release matching. The intended release is
 * `preferredReleaseId`; there is no catalog list or release selector here.
 *
 * Every preview/confirm body keeps the captured group revision, authority
 * revision, assignment draft and frozen preview digest. An uncertain confirm
 * keeps its original body and `request_id`, so `confirm()` can replay it even
 * after the preview has been hidden by a scope change.
 */
export function useLocalMatching({
  group,
  files,
  authorityRevision,
  preferredReleaseId,
  onConfirmed,
}: LocalMatchingProps): LocalMatchingController {
  const { locale } = useI18n();
  const copy = localMatchingMessages[locale];
  const [states, setStates] = useState<Record<string, EditorState>>({});
  const statesRef = useRef<Record<string, EditorState>>({});
  const sequence = useRef(0);
  const detailRequests = useRef<Record<string, number>>({});
  const targets = useRef<Record<string, string>>({});
  const previousScopes = useRef<Record<string, string>>({});
  const appliedPreferredReleases = useRef<Record<string, string>>({});
  const groupID = group?.id ?? "";
  const targetReleaseID = preferredReleaseId ?? "";
  const currentScope = group
    ? `${group.id}:${group.revision}:${authorityRevision === undefined ? "unknown" : authorityRevision}`
    : "";
  const currentScopeRef = useRef(currentScope);
  currentScopeRef.current = currentScope;
  const state = groupID
    ? (states[groupID] ?? emptyEditorState())
    : emptyEditorState();
  if (groupID) targets.current[groupID] = targetReleaseID;
  const memberIds = group?.members ?? noMembers;
  const scopedFiles = useMemo(() => {
    const members = new Set(memberIds);
    const collator = new Intl.Collator(locale, {
      numeric: true,
      sensitivity: "base",
    });
    return files
      .filter((file) => members.has(file.variant_id))
      .map((file) => ({
        file,
        name:
          incomingSourceFileName(file.locations?.[0]?.relative_path) ??
          file.variant_id,
        ...readIncomingSourceTrackOrder(file.tags),
      }))
      .sort((a, b) => compareSourceFiles(a, b, "track", collator))
      .map(({ file }) => file);
  }, [files, memberIds, locale]);
  const release =
    group && targetReleaseID && state.selectedReleaseID === targetReleaseID
      ? state.release
      : undefined;
  const tracks = release?.tracks ?? [];
  const previewCurrent =
    state.preview?.scope === currentScope &&
    state.selectedReleaseID === targetReleaseID &&
    state.preview.body.provider_release_id === targetReleaseID &&
    state.preview.response.provider_release_id === targetReleaseID
      ? state.preview
      : undefined;
  const confirmationPending = state.busy === "confirm";
  const status: LocalMatchingStatus =
    !group || !targetReleaseID
      ? "idle"
      : state.selectedReleaseID !== targetReleaseID
        ? "loading"
        : release
          ? "ready"
          : state.readError
            ? "error"
            : "loading";
  const pending = Boolean(
    targetReleaseID &&
      state.pending?.body.provider_release_id === targetReleaseID,
  );
  const canPreview =
    Boolean(group && targetReleaseID && release) &&
    authorityRevision !== undefined &&
    state.busy === undefined &&
    state.pending === undefined;
  const canConfirm = state.busy === undefined && (pending || !!previewCurrent);

  function editorState(targetGroupID: string) {
    return targetGroupID
      ? (statesRef.current[targetGroupID] ?? emptyEditorState())
      : emptyEditorState();
  }

  const updateState = useCallback(
    (targetGroupID: string, update: (current: EditorState) => EditorState) => {
      const current = statesRef.current[targetGroupID] ?? emptyEditorState();
      const next = {
        ...statesRef.current,
        [targetGroupID]: update(current),
      };
      statesRef.current = next;
      setStates(next);
    },
    [],
  );

  async function loadRelease(
    targetGroupID: string,
    releaseID: string,
    fresh: boolean,
  ) {
    const request = (detailRequests.current[targetGroupID] ?? 0) + 1;
    detailRequests.current[targetGroupID] = request;
    sequence.current += 1;
    updateState(targetGroupID, (current) => {
      const assignmentDrafts = { ...current.assignmentDrafts };
      if (current.selectedReleaseID) {
        assignmentDrafts[current.selectedReleaseID] = current.assignments;
      }
      return {
        ...current,
        selectedReleaseID: releaseID,
        release: undefined,
        assignmentDrafts,
        assignments: fresh
          ? { ...(assignmentDrafts[releaseID] ?? {}) }
          : current.assignments,
        preview: undefined,
        receipt: fresh ? undefined : current.receipt,
        error: undefined,
        readError: undefined,
        notice: fresh ? undefined : current.notice,
        stale: false,
      };
    });
    if (!releaseID) return;
    try {
      const response = await readLocalProviderRelease(releaseID, {
        cache: "no-store",
      });
      if (
        request !== detailRequests.current[targetGroupID] ||
        statesRef.current[targetGroupID]?.selectedReleaseID !== releaseID
      )
        return;
      if (response.status !== 200) {
        const text = responseMessage(response, copy.releaseReadError, copy);
        updateState(targetGroupID, (current) => ({
          ...current,
          release: undefined,
          readError: text,
          error: text,
        }));
        return;
      }
      updateState(targetGroupID, (current) => ({
        ...current,
        release: response.data,
        readError: undefined,
      }));
    } catch (reason) {
      if (
        request === detailRequests.current[targetGroupID] &&
        statesRef.current[targetGroupID]?.selectedReleaseID === releaseID
      ) {
        const text = message(reason, copy.releaseReadError);
        updateState(targetGroupID, (current) => ({
          ...current,
          release: undefined,
          readError: text,
          error: text,
        }));
      }
    }
  }

  async function selectRelease(
    releaseID: string,
    { restoreDraft = true }: { restoreDraft?: boolean } = {},
  ) {
    if (!group) return;
    const current = editorState(group.id);
    if (current.pending) return;
    await loadRelease(group.id, releaseID, !restoreDraft);
  }

  const selectReleaseRef = useRef(selectRelease);
  selectReleaseRef.current = selectRelease;

  useEffect(() => {
    if (!groupID) return;
    const previous = previousScopes.current[groupID];
    previousScopes.current[groupID] = currentScope;
    if (previous === undefined || previous === currentScope) return;
    sequence.current += 1;
    updateState(groupID, (existing) => ({
      ...existing,
      preview: undefined,
      stale: true,
      notice: {
        key: existing.receipt
          ? "scopeChangedAfterConfirm"
          : "scopeChangedBeforeConfirm",
      },
    }));
  }, [currentScope, groupID, updateState]);

  useEffect(() => {
    if (!groupID) return;
    if (!preferredReleaseId) {
      delete appliedPreferredReleases.current[groupID];
      return;
    }
    if (appliedPreferredReleases.current[groupID] === preferredReleaseId) {
      return;
    }
    if (state.pending || state.busy === "confirm") return;
    appliedPreferredReleases.current[groupID] = preferredReleaseId;
    if (state.selectedReleaseID !== preferredReleaseId)
      void selectReleaseRef.current(preferredReleaseId);
  }, [
    preferredReleaseId,
    groupID,
    state.pending,
    state.busy,
    state.selectedReleaseID,
  ]);

  function makePreviewBody(): PreviewBody | undefined {
    if (!group) return undefined;
    const current = editorState(group.id);
    if (authorityRevision === undefined) {
      updateState(group.id, (latest) => ({
        ...latest,
        error: copy.authorityNotReady,
      }));
      return undefined;
    }
    if (
      !targetReleaseID ||
      current.selectedReleaseID !== targetReleaseID ||
      !current.release
    ) {
      updateState(group.id, (latest) => ({
        ...latest,
        error: copy.selectReleaseFirst,
      }));
      return undefined;
    }
    const allowedMembers = new Set(scopedFiles.map((file) => file.variant_id));
    const allowedTracks = new Set(
      (current.release.tracks ?? []).map((track) => track.id),
    );
    const assignments = Object.entries(current.assignments)
      .filter(
        ([fileID, trackID]) =>
          allowedMembers.has(fileID) && allowedTracks.has(trackID),
      )
      .map(([file_member_id, provider_track_id]) => ({
        file_member_id,
        provider_track_id,
      }));
    if (assignments.length === 0) {
      updateState(group.id, (latest) => ({
        ...latest,
        error: copy.assignAtLeastOne,
      }));
      return undefined;
    }
    return {
      provider_release_id: current.selectedReleaseID,
      expected_group_revision: group.revision,
      expected_authority_revision: authorityRevision,
      assignments,
    };
  }

  function setAssignments(next: Readonly<Record<string, string>>) {
    if (!group) return;
    const current = editorState(group.id);
    if (current.pending || current.busy === "confirm") return;
    sequence.current += 1;
    updateState(group.id, (latest) => {
      if (latest.pending || latest.busy === "confirm") return latest;
      return {
        ...latest,
        assignments: { ...next },
        preview: undefined,
        receipt: undefined,
        error: undefined,
        notice: undefined,
        stale: false,
      };
    });
  }

  async function read() {
    if (!group) return;
    const releaseID = targetReleaseID;
    if (!releaseID) return;
    const current = editorState(group.id);
    await loadRelease(
      group.id,
      releaseID,
      current.selectedReleaseID !== releaseID,
    );
  }

  async function preview() {
    if (!group) return;
    const current = editorState(group.id);
    if (current.pending || current.busy) return;
    const body = makePreviewBody();
    if (!body) return;
    const targetGroupID = group.id;
    const requestedReleaseID = body.provider_release_id;
    const request = ++sequence.current;
    const requestedScope = currentScope;
    updateState(targetGroupID, (latest) => ({
      ...latest,
      busy: "preview",
      busyRequest: request,
      error: undefined,
      notice: undefined,
      stale: false,
    }));
    try {
      const response = await previewIncomingGroupLocalMatching(
        targetGroupID,
        body,
      );
      if (
        request !== sequence.current ||
        currentScopeRef.current !== requestedScope ||
        targets.current[targetGroupID] !== requestedReleaseID
      )
        return;
      if (response.status !== 200) {
        updateState(targetGroupID, (latest) => ({
          ...latest,
          busy: undefined,
          busyRequest: undefined,
          preview: undefined,
          pending: response.status === 409 ? undefined : latest.pending,
          stale: response.status === 409,
          error:
            response.status === 409
              ? copy.previewConflict
              : responseMessage(response, copy.previewError, copy),
        }));
        return;
      }
      updateState(targetGroupID, (latest) => ({
        ...latest,
        busy: undefined,
        busyRequest: undefined,
        preview: { scope: requestedScope, body, response: response.data },
        pending: undefined,
        receipt: undefined,
      }));
    } catch (reason) {
      if (
        request === sequence.current &&
        currentScopeRef.current === requestedScope &&
        targets.current[targetGroupID] === requestedReleaseID
      ) {
        updateState(targetGroupID, (latest) => ({
          ...latest,
          busy: undefined,
          busyRequest: undefined,
          error: message(reason, copy.previewError),
        }));
      }
    } finally {
      updateState(targetGroupID, (latest) =>
        latest.busyRequest === request
          ? { ...latest, busy: undefined, busyRequest: undefined }
          : latest,
      );
    }
  }

  async function confirm() {
    if (!group || !targetReleaseID) return;
    const targetGroupID = group.id;
    const current = editorState(targetGroupID);
    const retainedPending =
      current.pending &&
      current.pending.body.provider_release_id === targetReleaseID
        ? current.pending
        : undefined;
    if (current.pending && !retainedPending) return;
    const pendingConfirmation =
      retainedPending ??
      (current.preview?.scope === currentScope &&
      current.selectedReleaseID === targetReleaseID &&
      current.preview.body.provider_release_id === targetReleaseID &&
      current.preview.response.provider_release_id === targetReleaseID
        ? {
            scope: currentScope,
            order: nextIncomingConfirmOrder(),
            body: {
              ...current.preview.body,
              preview_digest: current.preview.response.preview_digest,
              request_id: createUUID(),
            },
          }
        : undefined);
    if (!pendingConfirmation || current.busy) return;
    if (!retainedPending) {
      updateState(targetGroupID, (latest) => ({
        ...latest,
        pending: pendingConfirmation,
      }));
    }
    updateState(targetGroupID, (latest) => ({
      ...latest,
      busy: "confirm",
      error: undefined,
    }));
    try {
      const response = await confirmIncomingGroupLocalMatching(
        targetGroupID,
        pendingConfirmation.body,
      );
      if (response.status !== 200) {
        updateState(targetGroupID, (latest) => ({
          ...latest,
          busy: undefined,
          stale: response.status === 409,
          preview: response.status === 409 ? undefined : latest.preview,
          pending: response.status === 409 ? undefined : latest.pending,
          error:
            response.status === 409
              ? copy.confirmConflict
              : responseMessage(response, copy.confirmError, copy),
        }));
        return;
      }
      if (
        response.data.group_id !== targetGroupID ||
        response.data.provider_release_id !==
          pendingConfirmation.body.provider_release_id
      ) {
        throw new Error(copy.confirmError);
      }
      updateState(targetGroupID, (latest) => ({
        ...latest,
        busy: undefined,
        receipt: response.data,
        pending: undefined,
        preview: undefined,
        notice: { key: "confirmed" },
      }));
      try {
        await onConfirmed?.(
          response.data,
          pendingConfirmation.order,
          pendingConfirmation.body.request_id,
          pendingConfirmation.body.assignments ?? [],
        );
      } catch (reason) {
        updateState(targetGroupID, (latest) => ({
          ...latest,
          notice: {
            key: "confirmedRefreshError",
            reason: sharedErrorMessage(reason, "") || undefined,
          },
        }));
      }
    } catch (reason) {
      updateState(targetGroupID, (latest) => ({
        ...latest,
        busy: undefined,
        error: `${message(reason, copy.confirmError)} ${copy.confirmRetryNotice}`,
      }));
    }
  }

  return {
    release,
    workspaceRelease: localMatchingWorkspaceRelease(release),
    files: scopedFiles,
    tracks,
    assignments: state.assignments,
    setAssignments,
    read,
    preview,
    confirm,
    previewResult: previewCurrent?.response,
    receipt: state.receipt,
    error: state.error,
    notice: state.notice,
    stale: state.stale ?? false,
    busy: state.busy,
    pending,
    confirmationPending,
    status,
    canPreview,
    canConfirm,
  };
}
