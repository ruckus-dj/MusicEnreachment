import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";
import { createUUID } from "../../api/client/uuid";
import {
  confirmIncomingGroupManualMatching,
  getGetIncomingGroupMatchingQueryKey,
  getGetIncomingGroupsQueryKey,
  getIncomingGroupMatching,
  getIncomingGroups,
  previewIncomingGroupManualMatching,
  returnIncomingGroupMatchingToAutomatic,
} from "../../api/generated/client";
import type {
  MatchingManualAssignmentBody,
  MatchingManualConfirmBody,
  MatchingManualPreview,
  MatchingReturnAutomaticBody,
  MatchingReview,
  MatchingReviewRelease,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { MatchingPreview } from "../matchingWorkspace/MatchingPreview";
import {
  MatchingWorkspaceLayout,
  matchingCandidatesPanel,
  matchingInspectorActions,
  matchingInspectorContent,
  matchingInspectorPanel,
} from "../matchingWorkspace/MatchingWorkspaceLayout";
import {
  matchingBoardClass,
  matchingBoardClasses,
} from "../matchingWorkspace/matchingBoardStyles";
import { apiMatchingSources } from "./apiMatchingSources";
import { evidenceOverrideMessages } from "./evidenceOverrideMessages";
import { IncomingGroupsOverview } from "./IncomingGroupsOverview";
import {
  incomingGroupNames,
  incomingGroupNameText,
} from "./incomingGroupNames";
import {
  type IncomingGroupsRoute,
  incomingGroupMatchingHref,
  incomingGroupsListHref,
} from "./incomingGroupsRoute";
import {
  type IncomingFeedback,
  type IncomingMessageKey,
  type IncomingMessages,
  incomingFeedbackText,
  incomingMessages,
} from "./incomingMessages";
import {
  incomingScreenClass,
  incomingViewHeadingClass,
} from "./incomingStyles";
import { LocalReleaseEditor } from "./LocalReleaseEditor";
import { localMatchingMessages } from "./localMatchingMessages";
import { ManualReleasePicker } from "./ManualReleasePicker";
import { ManualSearchPicker } from "./ManualSearchPicker";
import {
  MatchingBoard,
  matchingBoardScoresFromCandidates,
} from "./MatchingBoard";
import { MatchingEvidenceInspector } from "./MatchingEvidenceInspector";
import { savedAutomaticAssignments } from "./savedAutomaticAssignments";
import {
  emptyTrackTagOverride,
  type TrackTagOverrideDraft,
  TrackTagOverrides,
} from "./TrackTagOverrides";

type ProposalFile = {
  file_member_id: string;
  raw_tags?: Record<string, string[] | null>;
  diagnostics?: { code: string; values?: string[] | null }[];
};
type ReviewProposal = {
  selected_release?: MatchingReviewRelease | null;
  files?: ProposalFile[];
};
type Draft = Record<string, string>;
type ManualAssignmentWithOverrides = MatchingManualAssignmentBody & {
  title_override?: string;
  artist_overrides?: string[] | null;
};
type PreviewState = { intent: string; response: MatchingManualPreview };
type ConfirmIntent = {
  groupId: string;
  generation: number;
  intent: string;
  body: MatchingManualConfirmBody;
};
type AuthorityIntent = {
  groupId: string;
  generation: number;
  intent: string;
  action: "automatic";
  body: MatchingReturnAutomaticBody;
};

type IncomingError = Error & {
  incomingMessageKey?: IncomingMessageKey;
  incomingHttpStatus?: number;
};

function errorText(
  reason: unknown,
  fallback: IncomingMessageKey,
): IncomingFeedback {
  const error = reason instanceof Error ? (reason as IncomingError) : undefined;
  if (error?.incomingMessageKey) {
    return {
      key: error.incomingMessageKey,
      status: error.incomingHttpStatus,
    };
  }
  return {
    key: fallback,
    raw: sharedErrorMessage(reason, "") || undefined,
  };
}

function responseError(
  response: { data: { detail?: string }; status: number },
  fallback: IncomingMessageKey,
) {
  const detail = response.data.detail;
  if (detail) return apiResponseError(response);
  const error = new Error() as IncomingError;
  error.incomingMessageKey = fallback;
  error.incomingHttpStatus = response.status;
  return error;
}

function parsedProposal(review: MatchingReview | undefined): ReviewProposal {
  if (!review?.proposal || typeof review.proposal !== "object") return {};
  return review.proposal as ReviewProposal;
}

function layoutLabel(
  release: MatchingReviewRelease,
  releases: MatchingReviewRelease[],
  copy: IncomingMessages,
) {
  const sameTitle = releases.filter((item) => item.title === release.title);
  const duplicateTitle = sameTitle.length > 1;
  const ordinal = sameTitle.indexOf(release);
  return `${release.title}${release.tracklist_complete ? "" : ` · ${copy.incompleteRelease}`}${duplicateTitle ? ` · ${copy.releaseOrdinal(ordinal + 1)}` : ""}`;
}

function positionLabel(
  position: NonNullable<MatchingReviewRelease["positions"]>[number],
  copy: IncomingMessages,
) {
  const number = position.displayed_number || `${position.position + 1}`;
  return copy.discTrack(position.medium + 1, number, position.title);
}

function previewRows(
  preview: MatchingManualPreview,
  copy: IncomingMessages,
  fileLabel: (memberId: string, index: number) => string,
) {
  const members = preview.members ?? [];
  const positions = preview.selected_release.positions ?? [];
  const memberIds = new Set(members.map((member) => member.file_member_id));
  const rows: { key: string; source: string; position?: string }[] =
    members.map((member, index) => {
      const assignment = member.assignment;
      // Confirmable assignment coordinates are already one-based. Only use
      // the layout for display metadata, not for the assignment coordinates.
      const matched = assignment
        ? positions.find(
            (item) => item.track_id === assignment.provider_track_id,
          )
        : undefined;
      return {
        key: member.file_member_id,
        source:
          member.locations?.find(Boolean) ??
          fileLabel(member.file_member_id, index),
        ...(assignment
          ? {
              position: copy.discTrack(
                assignment.medium,
                matched?.displayed_number || String(assignment.position),
                matched?.title || copy.unknown,
              ),
            }
          : {}),
      };
    });
  const omitted = [...new Set(preview.omitted_member_ids ?? [])].filter(
    (memberId) => !memberIds.has(memberId),
  );
  omitted.forEach((memberId, index) => {
    rows.push({
      key: `omitted:${memberId}`,
      source: fileLabel(memberId, members.length + index),
    });
  });
  return rows;
}

export function IncomingGroupReviewScreen({
  route,
}: {
  route: IncomingGroupsRoute;
}) {
  return <IncomingGroupReviewContent route={route} />;
}

function IncomingGroupReviewContent({ route }: { route: IncomingGroupsRoute }) {
  const queryClient = useQueryClient();
  const { locale } = useI18n();
  const copy = incomingMessages[locale];
  const localMatchingCopy = localMatchingMessages[locale];
  const routeGroupId = route.view === "matching" ? route.groupId : "";
  const routeReleaseId =
    route.view === "matching" ? (route.releaseId ?? "") : "";
  const matchingActive = route.view === "matching";
  const [retainedGroupId, setRetainedGroupId] = useState("");
  const groupId = routeGroupId || retainedGroupId;
  const groupsQuery = useQuery({
    queryKey: getGetIncomingGroupsQueryKey(),
    queryFn: async ({ signal }) => {
      const result = await getIncomingGroups({ signal, cache: "no-store" });
      if (result.status !== 200) throw responseError(result, "loadGroupsError");
      return result.data;
    },
  });
  const groups = groupsQuery.data?.groups ?? [];
  const groupNames = useMemo(
    () =>
      incomingGroupNames(
        groupsQuery.data?.groups ?? [],
        groupsQuery.data?.files ?? null,
      ),
    [groupsQuery.data],
  );
  const [selectedReleaseId, setSelectedReleaseId] = useState("");
  const [inspectedMemberId, setInspectedMemberId] = useState("");
  const [inspectedTrackId, setInspectedTrackId] = useState("");
  const [preparedReleaseMBID, setPreparedReleaseMBID] = useState("");
  const [searchReleaseSelections, setSearchReleaseSelections] = useState<
    Record<string, string>
  >({});
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [tagOverrideDrafts, setTagOverrideDrafts] = useState<
    Record<string, Record<string, TrackTagOverrideDraft>>
  >({});
  const [preview, setPreview] = useState<PreviewState>();
  const [confirmIntent, setConfirmIntent] = useState<ConfirmIntent>();
  const [error, setError] = useState<IncomingFeedback | null>(null);
  const [notice, setNotice] = useState<IncomingFeedback | null>(null);
  const [authorityNotice, setAuthorityNotice] =
    useState<IncomingFeedback | null>(null);
  const [authorityIntent, setAuthorityIntent] = useState<AuthorityIntent>();
  const [receipt, setReceipt] = useState<{
    groupId: string;
    response: MatchingManualPreview;
    omittedFileLabels: Record<string, string>;
  }>();
  const [pendingRequest, setPendingRequest] = useState<{
    id: number;
    groupId: string;
    scope: string;
  }>();
  const generation = useRef(0);
  const scopeRef = useRef({ groupId: "", releaseId: "", generation: 0 });
  const evaluationRevisionRef = useRef("");
  const requestId = useRef(0);
  const seededGroup = useRef("");
  const appliedRouteGroup = useRef("");
  const observedEvaluation = useRef<MatchingReview | undefined>(undefined);
  const authorityRevisionByGroup = useRef(new Map<string, number>());
  const matchingHeadingRef = useRef<HTMLHeadingElement>(null);
  const previewHeadingRef = useRef<HTMLHeadingElement>(null);
  const focusedPreview = useRef<MatchingManualPreview | undefined>(undefined);

  function scopeIdentity() {
    const scope = scopeRef.current;
    return `${scope.groupId}:${scope.releaseId}:${scope.generation}:${evaluationRevisionRef.current}`;
  }

  function invalidateIntent() {
    generation.current += 1;
    scopeRef.current = {
      groupId,
      releaseId: selectedReleaseId,
      generation: generation.current,
    };
    setPendingRequest(undefined);
  }

  const applyReleaseSelection = useCallback(
    (nextReleaseId: string) => {
      generation.current += 1;
      scopeRef.current = {
        groupId,
        releaseId: nextReleaseId,
        generation: generation.current,
      };
      setSelectedReleaseId(nextReleaseId);
      setPreview(undefined);
      setConfirmIntent(undefined);
      setReceipt(undefined);
      setNotice(null);
      setError(null);
    },
    [groupId],
  );

  useEffect(() => {
    if (!routeGroupId) return;
    setRetainedGroupId(routeGroupId);
    if (appliedRouteGroup.current === routeGroupId) return;
    appliedRouteGroup.current = routeGroupId;
    generation.current += 1;
    scopeRef.current = {
      groupId: routeGroupId,
      releaseId: "",
      generation: generation.current,
    };
    setSelectedReleaseId("");
    setPreparedReleaseMBID("");
    setPreview(undefined);
    setConfirmIntent(undefined);
    setReceipt(undefined);
    setAuthorityIntent(undefined);
    setAuthorityNotice(null);
  }, [routeGroupId]);

  useEffect(() => {
    if (!matchingActive || !routeGroupId) return;
    const heading = matchingHeadingRef.current;
    if (!heading) return;
    if (typeof heading.scrollIntoView === "function") {
      heading.scrollIntoView({ block: "start", inline: "nearest" });
    }
    heading.focus({ preventScroll: true });
  }, [matchingActive, routeGroupId]);

  const reviewQuery = useQuery({
    queryKey: getGetIncomingGroupMatchingQueryKey(groupId),
    queryFn: async ({ signal }) => {
      const result = await getIncomingGroupMatching(groupId, {
        signal,
        cache: "no-store",
      });
      if (result.status !== 200) throw responseError(result, "loadReviewError");
      return result.data;
    },
    enabled:
      Boolean(groupId) && groups.some((candidate) => candidate.id === groupId),
    // The backend publishes a new evaluation revision asynchronously when
    // provider data changes. Re-read the saved evaluation only while the
    // matching view is visible; a hidden tab and every other screen stay idle.
    // Focus refetch keeps its default behavior.
    refetchInterval: matchingActive ? 10_000 : false,
    refetchIntervalInBackground: false,
  });
  const review = reviewQuery.data;
  evaluationRevisionRef.current = review?.evaluation_revision ?? "";
  const proposal = parsedProposal(review);
  const group = groups.find((candidate) => candidate.id === groupId);
  const releases = review?.releases ?? [];
  const savedSelectedId = proposal.selected_release?.provider_release_id ?? "";

  useEffect(() => {
    if (!groupId || review?.group_id !== groupId) return;
    const previousRevision = authorityRevisionByGroup.current.get(groupId);
    authorityRevisionByGroup.current.set(groupId, review.authority_revision);
    if (
      previousRevision === undefined ||
      previousRevision === review.authority_revision
    )
      return;

    generation.current += 1;
    scopeRef.current = {
      groupId,
      releaseId: selectedReleaseId,
      generation: generation.current,
    };
    setPendingRequest(undefined);
    setPreview(undefined);
    setConfirmIntent(undefined);
    setAuthorityIntent(undefined);
  }, [groupId, review, selectedReleaseId]);

  // A same-group evaluation revision replaced by the periodic read invalidates
  // a preview and its confirmation retry: both belong to the revision that
  // produced their digest. Drafts stay intact and pending responses are fenced
  // by the revision embedded in the request scope.
  useEffect(() => {
    if (!groupId || review?.group_id !== groupId) return;
    const previous = observedEvaluation.current;
    observedEvaluation.current = review;
    if (
      previous?.group_id !== groupId ||
      previous.evaluation_revision === review.evaluation_revision
    )
      return;
    setPreview(undefined);
    setConfirmIntent(undefined);
    const editedRelease = (item: MatchingReview) =>
      item.releases?.find(
        (candidate) => candidate.provider_release_id === selectedReleaseId,
      );
    const oldPositions = editedRelease(previous)?.positions ?? [];
    const newPositions = editedRelease(review)?.positions ?? [];
    const key = `${groupId}:${selectedReleaseId}`;
    const editedTracks = [
      ...Object.values(drafts[key] ?? {}).filter(Boolean),
      ...Object.keys(tagOverrideDrafts[key] ?? {}),
    ];
    if (
      editedTracks.some((id) => {
        const oldPosition = oldPositions.find((item) => item.track_id === id);
        const newPosition = newPositions.find((item) => item.track_id === id);
        return (
          oldPosition &&
          (!newPosition ||
            oldPosition.medium !== newPosition.medium ||
            oldPosition.position !== newPosition.position)
        );
      })
    )
      setNotice({ key: "draftTargetsChanged" });
  }, [drafts, groupId, review, selectedReleaseId, tagOverrideDrafts]);

  useEffect(() => {
    if (!preparedReleaseMBID || !groupId) return;
    const chosen = releases.find(
      (item) => item.release_id === preparedReleaseMBID,
    );
    if (!chosen) return;
    setSelectedReleaseId(chosen.provider_release_id);
    scopeRef.current = {
      groupId,
      releaseId: chosen.provider_release_id,
      generation: ++generation.current,
    };
    setPreview(undefined);
    setConfirmIntent(undefined);
    setReceipt(undefined);
    setPreparedReleaseMBID("");
    window.location.hash = incomingGroupMatchingHref(
      groupId,
      chosen.provider_release_id,
    );
  }, [groupId, preparedReleaseMBID, releases]);

  useEffect(() => {
    if (!groupId || !review || seededGroup.current === groupId) return;
    seededGroup.current = groupId;
    setSelectedReleaseId(savedSelectedId);
    scopeRef.current = {
      groupId,
      releaseId: savedSelectedId,
      generation: generation.current,
    };
  }, [groupId, review, savedSelectedId]);

  // The release is an explicit URL state. A valid persisted proposal seeds the
  // selection only when the URL carries no release of its own. Abandoning the
  // query (for example by returning to the list) keeps the in-memory draft.
  useEffect(() => {
    if (!matchingActive || !routeReleaseId) return;
    if (routeReleaseId === selectedReleaseId) return;
    if (!review || review.group_id !== groupId) return;
    if (
      !(review.releases ?? []).some(
        (item) => item.provider_release_id === routeReleaseId,
      )
    )
      return;
    applyReleaseSelection(routeReleaseId);
  }, [
    applyReleaseSelection,
    groupId,
    matchingActive,
    review,
    routeReleaseId,
    selectedReleaseId,
  ]);

  const release = releases.find(
    (candidate) => candidate.provider_release_id === selectedReleaseId,
  );
  const draftKey = `${groupId}:${selectedReleaseId}`;
  const savedAssignments = useMemo(
    () => savedAutomaticAssignments(review, selectedReleaseId),
    [review, selectedReleaseId],
  );
  const assignmentsByMember = useMemo(() => {
    const merged: Record<string, string> = { ...savedAssignments };
    for (const [memberId, trackId] of Object.entries(drafts[draftKey] ?? {})) {
      // Keep a missing target in the draft without replacing it with a new
      // suggestion. The file is unassigned in the current layout.
      if (release?.positions?.some((item) => item.track_id === trackId))
        merged[memberId] = trackId;
      else delete merged[memberId];
    }
    return merged;
  }, [drafts, draftKey, release, savedAssignments]);
  const tagOverridesByTrack = tagOverrideDrafts[draftKey] ?? {};
  const files = groupsQuery.data?.files ?? [];
  const groupFiles = useMemo(() => {
    const ids = new Set(group?.members ?? []);
    return files.filter((file) => ids.has(file.variant_id));
  }, [files, group]);
  const memberIds = review?.members ?? group?.members ?? [];
  const proposalFiles = new Map(
    (proposal.files ?? []).map((file) => [file.file_member_id, file]),
  );
  const { label: fileLabel, sources: boardSources } = apiMatchingSources(
    memberIds,
    groupFiles,
    proposalFiles,
    copy.file,
  );
  const savedScores = release
    ? matchingBoardScoresFromCandidates(review?.candidates, release)
    : {};
  const intentPayload = useMemo(() => {
    if (!review?.evaluation_revision || !release) return "";
    const assignments: ManualAssignmentWithOverrides[] = memberIds.flatMap(
      (memberId) => {
        const trackId = assignmentsByMember[memberId];
        const position = release.positions?.find(
          (item) => item.track_id === trackId,
        );
        if (!trackId || !position) return [];
        const override = tagOverridesByTrack[position.track_id];
        return [
          {
            file_member_id: memberId,
            provider_track_id: position.track_id,
            medium: position.medium + 1,
            position: position.position + 1,
            ...(override?.title.enabled
              ? { title_override: override.title.value }
              : {}),
            ...(override?.artists.enabled
              ? {
                  artist_overrides: override.artists.values.map(
                    (artist) => artist.name,
                  ),
                }
              : {}),
          },
        ];
      },
    );
    return JSON.stringify({
      expected_evaluation_revision: review.evaluation_revision,
      provider_release_id: release.provider_release_id,
      assignments,
    });
  }, [
    assignmentsByMember,
    memberIds,
    release,
    review?.evaluation_revision,
    tagOverridesByTrack,
  ]);
  const intent = useMemo(() => {
    if (!intentPayload) return undefined;
    return JSON.parse(intentPayload) as {
      expected_evaluation_revision: string;
      provider_release_id: string;
      assignments: MatchingManualAssignmentBody[];
    };
  }, [intentPayload]);
  const currentPreview =
    preview?.intent === intentPayload ? preview.response : undefined;

  useEffect(() => {
    if (!currentPreview || focusedPreview.current === currentPreview) return;
    focusedPreview.current = currentPreview;
    if (!matchingActive) return;
    const heading = previewHeadingRef.current;
    if (!heading) return;
    if (typeof heading.scrollIntoView === "function") {
      heading.scrollIntoView({ block: "start", inline: "nearest" });
    }
    heading.focus({ preventScroll: true });
  }, [currentPreview, matchingActive]);

  const currentRetryIntent =
    confirmIntent?.groupId === groupId &&
    confirmIntent.generation === scopeRef.current.generation &&
    confirmIntent.intent === intentPayload
      ? confirmIntent
      : undefined;
  const busy = pendingRequest?.scope === scopeIdentity();
  const currentAuthorityIntent =
    authorityIntent?.groupId === groupId &&
    authorityIntent.generation === scopeRef.current.generation &&
    authorityIntent.intent ===
      `${group?.revision ?? ""}:${review?.authority_revision ?? -1}:${authorityIntent.action}`
      ? authorityIntent
      : undefined;
  const authorityBusy =
    pendingRequest?.scope ===
    `authority:${groupId}:${scopeRef.current.generation}`;
  // Group-scoped in-flight identity: independent of intent generation, so a
  // same-group request keeps draft controls locked even if the scope drifts.
  const sameGroupInFlight =
    pendingRequest !== undefined && pendingRequest.groupId === groupId;

  function authorityScope() {
    const scope = scopeRef.current;
    return `authority:${scope.groupId}:${scope.generation}`;
  }

  async function performAuthorityAction() {
    if (!group || !review || sameGroupInFlight) return;
    const action = "automatic" as const;
    const actionIntent = `${group.revision}:${review.authority_revision}:${action}`;
    const preserved =
      currentAuthorityIntent?.action === action
        ? currentAuthorityIntent
        : {
            groupId,
            generation: scopeRef.current.generation,
            intent: actionIntent,
            action,
            body: {
              expected_group_revision: group.revision,
              expected_authority_revision: review.authority_revision,
              request_id: createUUID(),
            } satisfies MatchingReturnAutomaticBody,
          };
    const requestScope = authorityScope();
    const activeRequest = ++requestId.current;
    setAuthorityIntent(preserved);
    setPendingRequest({ id: activeRequest, groupId, scope: requestScope });
    setError(null);
    setNotice(null);
    setAuthorityNotice(null);
    try {
      const response = await returnIncomingGroupMatchingToAutomatic(
        groupId,
        preserved.body,
      );
      if (requestScope !== authorityScope()) return;
      if (response.status !== 200) {
        if (response.status === 409) {
          invalidateIntent();
          setAuthorityIntent(undefined);
          setPreview(undefined);
          setConfirmIntent(undefined);
          setError({ key: "authorityConflict", refresh: "authority" });
          void Promise.all([
            groupsQuery.refetch(),
            reviewQuery.refetch(),
          ]).catch(() => undefined);
          return;
        }
        throw responseError(response, "authorityError");
      }
      generation.current += 1;
      scopeRef.current = {
        groupId,
        releaseId: selectedReleaseId,
        generation: generation.current,
      };
      setAuthorityIntent(undefined);
      setPreview(undefined);
      setConfirmIntent(undefined);
      setPendingRequest(undefined);
      setAuthorityNotice({ key: "authoritySavedNotice" });
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: getGetIncomingGroupsQueryKey(),
        }),
        queryClient.invalidateQueries({
          queryKey: getGetIncomingGroupMatchingQueryKey(groupId),
        }),
      ]);
    } catch (reason) {
      if (requestScope === authorityScope())
        setError(errorText(reason, "authorityError"));
    } finally {
      setPendingRequest((current) =>
        current?.id === activeRequest ? undefined : current,
      );
    }
  }

  function changeGroup(nextGroupId: string) {
    window.location.hash = incomingGroupMatchingHref(nextGroupId);
  }

  function changeRelease(nextReleaseId: string) {
    if (sameGroupInFlight) return;
    applyReleaseSelection(nextReleaseId);
    window.location.hash = incomingGroupMatchingHref(groupId, nextReleaseId);
  }

  function assign(memberId: string, trackId: string) {
    if (sameGroupInFlight || !review?.evaluation_revision || !release) return;
    if (!memberIds.includes(memberId)) return;
    if (
      trackId &&
      !(release.positions ?? []).some(
        (position) => position.track_id === trackId,
      )
    )
      return;
    invalidateIntent();
    // Record every explicit decision as a patch over the saved solver work,
    // including a value that currently equals the baseline. A later solver
    // revision must not silently reinterpret an explicit edit as "automatic".
    setDrafts((current) => ({
      ...current,
      [draftKey]: { ...(current[draftKey] ?? {}), [memberId]: trackId },
    }));
    setPreview(undefined);
    setConfirmIntent(undefined);
    setReceipt(undefined);
    setNotice(null);
    setError(null);
  }

  function updateTrackTagOverride(
    trackId: string,
    override: TrackTagOverrideDraft,
  ) {
    if (sameGroupInFlight) return;
    invalidateIntent();
    setTagOverrideDrafts((current) => ({
      ...current,
      [draftKey]: { ...(current[draftKey] ?? {}), [trackId]: override },
    }));
    setPreview(undefined);
    setConfirmIntent(undefined);
    setReceipt(undefined);
    setNotice(null);
    setError(null);
  }

  async function createPreview() {
    if (sameGroupInFlight || !intent || !groupId) return;
    const requestScope = scopeIdentity();
    const activeRequest = ++requestId.current;
    setPendingRequest({ id: activeRequest, groupId, scope: requestScope });
    setError(null);
    setNotice(null);
    try {
      const response = await previewIncomingGroupManualMatching(
        groupId,
        intent,
      );
      if (requestScope !== scopeIdentity()) return;
      if (response.status !== 200)
        throw responseError(response, "previewError");
      setPreview({ intent: intentPayload, response: response.data });
      setConfirmIntent(undefined);
    } catch (reason) {
      if (requestScope === scopeIdentity())
        setError(errorText(reason, "previewError"));
    } finally {
      setPendingRequest((current) =>
        current?.id === activeRequest ? undefined : current,
      );
    }
  }

  async function confirm() {
    if (sameGroupInFlight || !intent || !groupId || !currentPreview) return;
    const preserved = currentRetryIntent ?? {
      groupId,
      generation: scopeRef.current.generation,
      intent: intentPayload,
      body: {
        ...intent,
        preview_digest: currentPreview.preview_digest,
        request_id: createUUID(),
      },
    };
    const requestScope = scopeIdentity();
    const activeRequest = ++requestId.current;
    setConfirmIntent(preserved);
    setPendingRequest({ id: activeRequest, groupId, scope: requestScope });
    setError(null);
    setNotice(null);
    try {
      const response = await confirmIncomingGroupManualMatching(
        preserved.groupId,
        preserved.body,
      );
      if (requestScope !== scopeIdentity()) return;
      if (response.status !== 200) {
        if (response.status === 409) {
          setError({ key: "confirmConflict", refresh: "evaluation" });
          return;
        }
        throw responseError(response, "confirmError");
      }
      setReceipt({
        groupId: preserved.groupId,
        response: response.data,
        omittedFileLabels: Object.fromEntries(
          (response.data.omitted_member_ids ?? []).map((memberId) => [
            memberId,
            fileLabel(memberId, memberIds.indexOf(memberId)),
          ]),
        ),
      });
      setPreview(undefined);
      setConfirmIntent(undefined);
      setNotice({ key: "confirmNotice" });
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: getGetIncomingGroupsQueryKey(),
        }),
        queryClient.invalidateQueries({
          queryKey: getGetIncomingGroupMatchingQueryKey(groupId),
        }),
      ]);
    } catch (reason) {
      if (requestScope === scopeIdentity())
        setError(errorText(reason, "confirmError"));
    } finally {
      setPendingRequest((current) =>
        current?.id === activeRequest ? undefined : current,
      );
    }
  }

  async function reloadReview() {
    const requestScope = scopeIdentity();
    setError(null);
    await reviewQuery.refetch();
    if (requestScope !== scopeIdentity()) return;
    setPreview(undefined);
    setConfirmIntent(undefined);
    setNotice({ key: "refreshNotice" });
  }

  const inspectedFileMemberId = memberIds.includes(inspectedMemberId)
    ? inspectedMemberId
    : "";
  const inspectedPosition = release?.positions?.find(
    (position) => position.track_id === inspectedTrackId,
  );
  const inspectedFile = groupFiles.find(
    (file) => file.variant_id === inspectedFileMemberId,
  );
  const inspectedSavedFacts = proposalFiles.get(inspectedFileMemberId);
  const inspectedFileTitle =
    inspectedSavedFacts?.raw_tags?.TITLE?.[0] ||
    inspectedFile?.tags?.TITLE?.[0] ||
    inspectedFile?.locations?.[0]?.relative_path.split(/[\\/]/).at(-1) ||
    copy.file;
  const inspectedCandidate = review?.candidates?.find(
    (item) =>
      item.file_member_id === inspectedFileMemberId &&
      (item.release_id === release?.release_id ||
        item.release_id === release?.provider_release_id),
  );

  return (
    <section className={incomingScreenClass}>
      <section hidden={matchingActive} aria-labelledby="incoming-title">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 id="incoming-title" className="text-2xl font-semibold">
              {copy.title}
            </h1>
            <p className="mt-1 text-sm text-stone-700">{copy.introduction}</p>
          </div>
          <AppButton
            onPress={() => {
              void groupsQuery.refetch();
            }}
          >
            {copy.refreshGroups}
          </AppButton>
        </div>

        {groupsQuery.isLoading ? (
          <p role="status">{copy.loadingGroups}</p>
        ) : null}
        {groupsQuery.isError ? (
          <p role="alert">
            {incomingFeedbackText(
              locale,
              errorText(groupsQuery.error, "groupsLoadError"),
            )}
          </p>
        ) : null}
        {groupsQuery.data && groups.length === 0 ? (
          <p className="rounded border border-stone-300 p-4">{copy.noGroups}</p>
        ) : null}

        {groupsQuery.data ? (
          <IncomingGroupsOverview
            snapshot={groupsQuery.data}
            groupNames={groupNames}
            expandedGroupIds={
              route.view === "list" ? route.expandedGroupIds : undefined
            }
          />
        ) : null}
      </section>

      {groupId ? (
        <section
          hidden={!matchingActive}
          className="space-y-4"
          aria-labelledby="incoming-matching-title"
        >
          <div>
            <a
              className="mb-1.5 inline-block text-[var(--accent)] underline"
              href={
                group
                  ? incomingGroupsListHref([groupId])
                  : incomingGroupsListHref()
              }
            >
              {copy.backToGroups}
            </a>
            <h1
              id="incoming-matching-title"
              ref={matchingHeadingRef}
              tabIndex={-1}
              className={`${incomingViewHeadingClass} text-xl font-semibold`}
            >
              {copy.tabMatching}
              {group
                ? ` · ${incomingGroupNameText(groupNames.get(group.id), locale)}`
                : ""}
            </h1>
          </div>

          <div className="space-y-4">
            {groupsQuery.isLoading ? (
              <p role="status">{copy.loadingGroups}</p>
            ) : null}
            {groupsQuery.isError ? (
              <p role="alert">
                {incomingFeedbackText(
                  locale,
                  errorText(groupsQuery.error, "groupsLoadError"),
                )}
              </p>
            ) : null}
            {groupsQuery.data && !group ? (
              <p role="alert" className="rounded border border-stone-300 p-4">
                {copy.groupNotFound}
              </p>
            ) : null}
            {groups.length > 0 && group ? (
              <label className="block max-w-2xl text-sm font-medium">
                {copy.incomingGroupsLabel}
                <select
                  className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
                  value={groupId}
                  onChange={(event) => changeGroup(event.target.value)}
                >
                  {groups.map((item) => (
                    <option key={item.id} value={item.id}>
                      {copy.groupOption(
                        incomingGroupNameText(groupNames.get(item.id), locale),
                        item.members?.length ?? 0,
                        item.manual,
                      )}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}

            {group && groupsQuery.data ? (
              <details className="rounded border border-stone-300 p-3">
                <summary className="cursor-pointer font-semibold">
                  {localMatchingCopy.sectionSummary}
                </summary>
                <LocalReleaseEditor
                  group={group}
                  files={groupsQuery.data.files ?? []}
                  authorityRevision={review?.authority_revision}
                  onConfirmed={async () => {
                    invalidateIntent();
                    setPreview(undefined);
                    setConfirmIntent(undefined);
                    setAuthorityIntent(undefined);
                    await Promise.all([
                      queryClient.invalidateQueries({
                        queryKey: getGetIncomingGroupsQueryKey(),
                      }),
                      queryClient.invalidateQueries({
                        queryKey: getGetIncomingGroupMatchingQueryKey(groupId),
                      }),
                    ]);
                  }}
                />
              </details>
            ) : null}

            {reviewQuery.isLoading ? (
              <p role="status">{copy.loadingEvaluation}</p>
            ) : null}
            {reviewQuery.isError ? (
              <div role="alert" className="space-y-2">
                <p>
                  {incomingFeedbackText(
                    locale,
                    errorText(reviewQuery.error, "loadReviewError"),
                  )}
                </p>
                <AppButton
                  isDisabled={sameGroupInFlight}
                  onPress={() => {
                    void reviewQuery.refetch();
                  }}
                >
                  {copy.retryReading}
                </AppButton>
              </div>
            ) : null}
            {review && !review.evaluated ? (
              <p className="rounded border border-amber-500 bg-amber-50 p-3">
                {copy.evaluationNotRun}
              </p>
            ) : null}
            {review ? (
              <section
                className="rounded border border-stone-300 bg-white p-4"
                aria-labelledby="authority-title"
              >
                <h2 id="authority-title" className="font-semibold">
                  {copy.authorityHeading}
                </h2>
                <p className="mt-1 text-sm">
                  {copy.modeLabel}{" "}
                  <strong>
                    {review.authority_mode === "manual"
                      ? copy.manualMatching
                      : copy.automaticMatching}
                  </strong>
                </p>
                <p className="mt-1 text-sm">
                  {copy.savedEvaluationExplanation}
                </p>
                {authorityNotice ? (
                  <p
                    role="status"
                    className="mt-2 rounded border border-green-700 p-3"
                  >
                    {incomingFeedbackText(locale, authorityNotice)}
                  </p>
                ) : null}
                {review.authority_mode === "manual" ? (
                  <div className="mt-3">
                    <AppButton
                      isDisabled={sameGroupInFlight}
                      onPress={() => void performAuthorityAction()}
                    >
                      {currentAuthorityIntent?.action === "automatic"
                        ? copy.retryReturnAutomatic
                        : copy.returnAutomatic}
                    </AppButton>
                  </div>
                ) : null}
                {error?.refresh === "authority" ? (
                  <AppButton
                    isDisabled={sameGroupInFlight}
                    onPress={() => {
                      setAuthorityIntent(undefined);
                      void Promise.all([
                        groupsQuery.refetch(),
                        reviewQuery.refetch(),
                      ]);
                    }}
                  >
                    {copy.refreshAuthority}
                  </AppButton>
                ) : null}
              </section>
            ) : null}
            {review?.evaluated && !review.proposal_available ? (
              <p className="rounded border border-amber-500 bg-amber-50 p-3">
                {copy.proposalUnavailable}
              </p>
            ) : null}

            {group && !(review?.evaluated && review.proposal_available) ? (
              <section
                className="grid gap-3 rounded border border-stone-300 bg-white p-3 lg:grid-cols-2"
                aria-label={copy.searchPreparationLabel}
              >
                <ManualSearchPicker
                  groupId={groupId}
                  groupRevision={group.revision}
                  disabled={
                    !review || busy || authorityBusy || sameGroupInFlight
                  }
                  onSelectRelease={(releaseId) =>
                    setSearchReleaseSelections((current) => ({
                      ...current,
                      [groupId]: releaseId,
                    }))
                  }
                />
                <ManualReleasePicker
                  groupId={groupId}
                  groupRevision={group.revision}
                  evaluationRevision={review?.evaluation_revision || undefined}
                  disabled={
                    !review || busy || authorityBusy || sameGroupInFlight
                  }
                  searchRelease={searchReleaseSelections[groupId]}
                  onPrepared={setPreparedReleaseMBID}
                />
              </section>
            ) : null}

            {review?.evaluated &&
            review.proposal_available &&
            review.releases?.length === 0 ? (
              <p className="rounded border border-stone-300 p-3">
                {copy.noReleaseLayouts}
              </p>
            ) : null}

            {review?.evaluated && review.proposal_available ? (
              <MatchingWorkspaceLayout>
                <section
                  data-matching-candidates
                  className={matchingCandidatesPanel}
                  aria-labelledby="matching-search-title"
                >
                  <h2 id="matching-search-title" className="font-semibold">
                    {copy.searchFilesHeading}
                  </h2>
                  {group ? (
                    <>
                      <ManualSearchPicker
                        groupId={groupId}
                        groupRevision={group.revision}
                        disabled={
                          !review || busy || authorityBusy || sameGroupInFlight
                        }
                        onSelectRelease={(releaseId) =>
                          setSearchReleaseSelections((current) => ({
                            ...current,
                            [groupId]: releaseId,
                          }))
                        }
                      />
                      <ManualReleasePicker
                        groupId={groupId}
                        groupRevision={group.revision}
                        evaluationRevision={
                          review?.evaluation_revision || undefined
                        }
                        disabled={
                          !review || busy || authorityBusy || sameGroupInFlight
                        }
                        searchRelease={searchReleaseSelections[groupId]}
                        onPrepared={setPreparedReleaseMBID}
                      />
                    </>
                  ) : null}
                  <h3 className="font-medium">{copy.savedEvaluation}</h3>
                  <label className="block text-sm font-medium">
                    {copy.releaseSelectionLabel}
                    <select
                      disabled={busy || authorityBusy || sameGroupInFlight}
                      className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
                      value={selectedReleaseId}
                      onChange={(event) => changeRelease(event.target.value)}
                    >
                      <option value="">{copy.chooseRelease}</option>
                      {releases.map((item) => (
                        <option
                          key={item.provider_release_id}
                          value={item.provider_release_id}
                        >
                          {layoutLabel(item, releases, copy)}
                        </option>
                      ))}
                    </select>
                  </label>
                  {savedSelectedId && selectedReleaseId === savedSelectedId ? (
                    <p className="text-sm">{copy.savedRelease}</p>
                  ) : null}
                  <details className="rounded border border-[var(--line)] p-2">
                    <summary className="cursor-pointer font-medium">
                      {copy.savedCandidatesHeading}
                    </summary>
                    {review.candidates?.length ? (
                      <ul className="mt-2 max-h-[22rem] space-y-2 overflow-y-auto text-sm">
                        {review.candidates.flatMap((candidate) =>
                          candidate.positions?.length
                            ? candidate.positions.map((position) => (
                                <li
                                  key={`${candidate.file_member_id}-${position.track_id}`}
                                  className="rounded border border-stone-200 p-2"
                                >
                                  <strong>
                                    {fileLabel(
                                      candidate.file_member_id,
                                      memberIds.indexOf(
                                        candidate.file_member_id,
                                      ),
                                    )}
                                  </strong>
                                  <p>
                                    {releases.find(
                                      (item) =>
                                        item.release_id ===
                                          candidate.release_id ||
                                        item.provider_release_id ===
                                          candidate.release_id,
                                    )?.title ?? copy.releaseOutsideLayout}
                                  </p>
                                  <p>
                                    {copy.releaseConfidence}{" "}
                                    {Number.isFinite(
                                      candidate.release_confidence,
                                    )
                                      ? candidate.release_confidence
                                      : copy.unknown}{" "}
                                    · {copy.positions}{" "}
                                    {position.displayed_number ||
                                      position.position + 1}{" "}
                                    —{" "}
                                    {position.assignment_confidence ??
                                      copy.unknown}
                                  </p>
                                </li>
                              ))
                            : [
                                <li
                                  key={`${candidate.file_member_id}-${candidate.release_id}`}
                                  className="rounded border border-stone-200 p-2"
                                >
                                  <strong>
                                    {fileLabel(
                                      candidate.file_member_id,
                                      memberIds.indexOf(
                                        candidate.file_member_id,
                                      ),
                                    )}
                                  </strong>
                                  <p>
                                    {releases.find(
                                      (item) =>
                                        item.release_id ===
                                          candidate.release_id ||
                                        item.provider_release_id ===
                                          candidate.release_id,
                                    )?.title ?? copy.releaseOutsideLayout}
                                  </p>
                                  <p>
                                    {copy.releaseConfidence}{" "}
                                    {Number.isFinite(
                                      candidate.release_confidence,
                                    )
                                      ? candidate.release_confidence
                                      : copy.unknown}
                                  </p>
                                </li>,
                              ],
                        )}
                      </ul>
                    ) : (
                      <p className="text-sm">{copy.noCandidateEdges}</p>
                    )}
                  </details>
                </section>

                {release ? (
                  <MatchingBoard
                    groupId={groupId}
                    release={release}
                    members={memberIds}
                    sources={boardSources}
                    assignments={assignmentsByMember}
                    automaticAssignments={savedAssignments}
                    savedScores={savedScores}
                    disabled={busy || authorityBusy || sameGroupInFlight}
                    inspectedTrackId={inspectedPosition?.track_id}
                    onAssign={assign}
                    onUnassign={(memberId) => assign(memberId, "")}
                    onInspectPosition={(trackId) => {
                      setInspectedTrackId(trackId);
                      const assigned = memberIds.filter(
                        (id) => assignmentsByMember[id] === trackId,
                      );
                      setInspectedMemberId((current) =>
                        assigned.includes(current)
                          ? current
                          : assigned.length === 1
                            ? assigned[0]
                            : "",
                      );
                    }}
                    onInspectSource={(memberId) => {
                      setInspectedMemberId(memberId);
                      setInspectedTrackId(assignmentsByMember[memberId] ?? "");
                    }}
                    onInspectPair={(memberId, trackId) => {
                      setInspectedMemberId(memberId);
                      setInspectedTrackId(trackId);
                    }}
                    renderPositionMeta={({ position }) => (
                      <details className="mt-1">
                        <summary className="cursor-pointer text-xs text-stone-700">
                          {evidenceOverrideMessages[locale].tagOverridesLegend(
                            positionLabel(position, copy),
                          )}
                        </summary>
                        <TrackTagOverrides
                          title={positionLabel(position, copy)}
                          trackId={position.track_id}
                          override={
                            tagOverridesByTrack[position.track_id] ??
                            emptyTrackTagOverride()
                          }
                          disabled={busy || authorityBusy || sameGroupInFlight}
                          onChange={(override) =>
                            updateTrackTagOverride(position.track_id, override)
                          }
                        />
                      </details>
                    )}
                  />
                ) : (
                  <>
                    <section
                      data-matching-positions
                      className={matchingBoardClass.panel}
                      aria-labelledby="assignment-board-title"
                    >
                      <h2
                        id="assignment-board-title"
                        className={matchingBoardClass.heading}
                      >
                        {copy.positionHeading}
                      </h2>
                      <p className={matchingBoardClass.instructions}>
                        {copy.choosePrepareRelease}
                      </p>
                    </section>
                    <section
                      data-matching-sources
                      className={matchingBoardClass.panel}
                      aria-labelledby="matching-sources-title"
                    >
                      <h2
                        id="matching-sources-title"
                        className={matchingBoardClass.heading}
                      >
                        {copy.sourceFilesHeading}
                      </h2>
                      <p className={matchingBoardClass.instructions}>
                        {copy.sourceInstructions}
                      </p>
                      {memberIds.length ? (
                        <ul className={matchingBoardClass.list}>
                          {memberIds.map((memberId, index) => (
                            <li
                              key={memberId}
                              className={matchingBoardClasses(
                                matchingBoardClass.source,
                                matchingBoardClass.listItem,
                              )}
                            >
                              <span className={matchingBoardClass.sourceSelect}>
                                <strong>{fileLabel(memberId, index)}</strong>
                              </span>
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <p className={matchingBoardClass.empty}>
                          {copy.noFilesInGroup}
                        </p>
                      )}
                    </section>
                  </>
                )}

                <section
                  data-matching-inspector
                  className={matchingInspectorPanel}
                  aria-labelledby="matching-inspector-title"
                >
                  <div className={matchingInspectorContent}>
                    <h2 id="matching-inspector-title" className="font-semibold">
                      {copy.inspectorLabel}
                    </h2>
                    {inspectedFileMemberId || inspectedPosition ? (
                      <p className="mt-1 text-sm wrap-anywhere">
                        {[
                          inspectedFileMemberId ? inspectedFileTitle : "",
                          inspectedPosition
                            ? positionLabel(inspectedPosition, copy)
                            : "",
                        ]
                          .filter(Boolean)
                          .join(" → ")}
                      </p>
                    ) : null}
                    {inspectedFileMemberId && inspectedPosition ? (
                      <MatchingEvidenceInspector
                        candidate={inspectedCandidate}
                        position={inspectedPosition}
                        rawTags={
                          inspectedSavedFacts?.raw_tags ?? inspectedFile?.tags
                        }
                      />
                    ) : (
                      <p className="mt-2 text-sm text-[var(--muted)]">
                        {copy.inspectAssignment}
                      </p>
                    )}
                  </div>
                  <aside
                    data-matching-actions
                    className={matchingInspectorActions}
                    aria-label={copy.assignmentActions}
                  >
                    {error ? (
                      <p
                        role="alert"
                        className="w-full rounded border border-red-600 p-2 text-sm"
                      >
                        {incomingFeedbackText(locale, error)}
                      </p>
                    ) : null}
                    {notice ? (
                      <p
                        role="status"
                        className="w-full rounded border border-green-700 p-2 text-sm"
                      >
                        {incomingFeedbackText(locale, notice)}
                      </p>
                    ) : null}
                    <AppButton
                      isDisabled={
                        sameGroupInFlight ||
                        !release ||
                        !review.evaluation_revision
                      }
                      onPress={() => void createPreview()}
                    >
                      {busy && !currentRetryIntent
                        ? copy.processing
                        : copy.previewAll}
                    </AppButton>
                    {error?.refresh === "evaluation" ? (
                      <AppButton
                        isDisabled={sameGroupInFlight}
                        onPress={() => void reloadReview()}
                      >
                        {copy.refreshEvaluation}
                      </AppButton>
                    ) : null}
                  </aside>
                </section>
              </MatchingWorkspaceLayout>
            ) : null}

            {review?.evaluated && review.proposal_available ? (
              <section
                className="rounded border border-stone-300 bg-white p-4"
                aria-label={copy.groupSummaryLabel}
              >
                <h2 className="font-semibold">{copy.groupDraftHeading}</h2>
                <div className="mt-2 overflow-x-auto">
                  <table className="w-full border-collapse text-left text-sm">
                    <thead>
                      <tr>
                        <th className="border-b p-2">
                          {copy.uniqueFilesPaths}
                        </th>
                        <th className="border-b p-2">
                          {copy.draftAssignedTotal}
                        </th>
                        <th className="border-b p-2">{copy.sourceSnapshot}</th>
                        <th className="border-b p-2">
                          {copy.providerSnapshot}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td className="border-b p-2">
                          {new Set(memberIds).size} /{" "}
                          {
                            new Set(
                              groupFiles.flatMap(
                                (file) =>
                                  file.locations?.map(
                                    (location) =>
                                      `${location.root_id}:${location.relative_path}`,
                                  ) ?? [],
                              ),
                            ).size
                          }
                        </td>
                        <td className="border-b p-2">
                          {
                            Object.keys(assignmentsByMember).filter(
                              (memberId) => memberIds.includes(memberId),
                            ).length
                          }{" "}
                          / {memberIds.length}
                        </td>
                        <td className="border-b p-2">
                          {copy.savedFacts(
                            groupFiles.filter(
                              (file) =>
                                file.capture_identity || file.locations?.length,
                            ).length,
                            groupFiles.length,
                          )}
                        </td>
                        <td className="border-b p-2">
                          {review.candidates?.length
                            ? copy.savedCandidates(review.candidates.length)
                            : copy.noCandidates}
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <p className="mt-2 text-sm">
                  {copy.linkedManualFiles}{" "}
                  {review.current_linked_manual_media_source_ids?.length ?? 0}.{" "}
                  {copy.draftDoesNotChangeLinks}
                </p>
              </section>
            ) : null}

            {currentPreview ? (
              <MatchingPreview
                heading={copy.previewHeading}
                headingRef={previewHeadingRef}
                releaseTitle={currentPreview.selected_release.title}
                summary={copy.previewSummary(
                  currentPreview.members?.filter((member) => member.assignment)
                    .length ?? 0,
                  currentPreview.omitted_member_ids?.length ?? 0,
                )}
                locale={locale}
                rows={previewRows(currentPreview, copy, fileLabel)}
                primary={{
                  label: currentRetryIntent
                    ? copy.retryConfirmation
                    : copy.confirmExplicitly,
                  onPress: () => void confirm(),
                  disabled: sameGroupInFlight,
                }}
              >
                {release?.positions?.some((position) => {
                  const override = tagOverridesByTrack[position.track_id];
                  return (
                    Object.values(assignmentsByMember).includes(
                      position.track_id,
                    ) &&
                    (override?.title.enabled || override?.artists.enabled)
                  );
                }) ? (
                  <div className="mt-2 text-sm">
                    <h3 className="font-medium">
                      {copy.manualMetadataApplied}
                    </h3>
                    <ul className="list-inside list-disc">
                      {release.positions?.map((position) => {
                        const override = tagOverridesByTrack[position.track_id];
                        if (
                          !Object.values(assignmentsByMember).includes(
                            position.track_id,
                          ) ||
                          (!override?.title.enabled &&
                            !override?.artists.enabled)
                        )
                          return null;
                        return (
                          <li key={position.track_id}>
                            {position.title}:{" "}
                            {[
                              override.title.enabled
                                ? copy.titleOverride(override.title.value)
                                : undefined,
                              override.artists.enabled
                                ? copy.artistOverrides(
                                    override.artists.values.length
                                      ? override.artists.values
                                          .map((artist) => `«${artist.name}»`)
                                          .join(", ")
                                      : copy.emptyList,
                                  )
                                : undefined,
                            ]
                              .filter(Boolean)
                              .join("; ")}
                          </li>
                        );
                      })}
                    </ul>
                  </div>
                ) : null}
              </MatchingPreview>
            ) : null}
            {receipt?.groupId === groupId ? (
              <section
                className="rounded border border-green-700 bg-white p-4"
                aria-labelledby="receipt-title"
              >
                <h2 id="receipt-title" className="font-semibold">
                  {copy.confirmedLocalRelease}
                </h2>
                <p>
                  {copy.receiptCounts(
                    receipt.response.members?.filter(
                      (member) => member.assignment,
                    ).length ?? 0,
                    receipt.response.members?.length ?? 0,
                    receipt.response.omitted_member_ids?.length ?? 0,
                  )}
                </p>
                <ul className="mt-2 list-inside list-disc text-sm">
                  {(receipt.response.omitted_member_ids ?? []).map(
                    (memberId) => (
                      <li key={memberId}>
                        {receipt.omittedFileLabels[memberId] ?? copy.file}{" "}
                        {copy.remainsIncoming}
                      </li>
                    ),
                  )}
                </ul>
              </section>
            ) : null}
          </div>
        </section>
      ) : null}
    </section>
  );
}
