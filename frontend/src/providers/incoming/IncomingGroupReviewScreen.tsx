import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  type ComponentProps,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
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
import { useI18n } from "../../app/i18n/host";
import type { ApplicationIncomingListModel } from "../../ui/app/ApplicationRouteOutlet";
import { focusView } from "../../ui/components/useViewFocus";
import type { IncomingCreationSource } from "../../ui/features/incoming/IncomingCreationOutlet";
import { IncomingWorkspaceView } from "../../ui/features/incoming/IncomingWorkspaceView";
import {
  type IncomingFeedback,
  type IncomingMessageKey,
  type IncomingMessages,
  incomingFeedbackText,
  incomingMessages,
} from "../../ui/features/incoming/incomingReviewMessages";
import { localMatchingMessages } from "../../ui/features/incoming/localMatchingMessages";
import type {
  MatchingScreenViewAction,
  MatchingScreenViewBoard,
  MatchingScreenViewCandidateRow,
  MatchingScreenViewModel,
  MatchingScreenViewPreview,
} from "../../ui/features/matching/MatchingScreenView";
import { matchingLinksCopy } from "../../ui/features/matching/matchingLinksCopy";
import {
  emptyTrackTagOverride,
  type TrackTagOverrideDraft,
  TrackTagOverrides,
} from "../../ui/features/matching/TrackTagOverrides";
import type {
  MatchingWorkspacePosition,
  MatchingWorkspacePositionContext,
} from "../../ui/features/matching/types";
import { IncomingLocalReleaseCreation } from "../local-release/IncomingLocalReleaseCreation";
import { apiMatchingSources } from "./apiMatchingSources";
import {
  findIncomingConfirmReceipt,
  nextIncomingConfirmOrder,
  saveIncomingConfirmReceipt,
  useIncomingConfirmReceipts,
} from "./confirmReceipts";
import { IncomingSavedMatchingResult } from "./IncomingSavedMatchingResult";
import { IncomingTagEncodingReview } from "./IncomingTagEncodingReview";
import {
  incomingGroupNames,
  incomingGroupNameText,
} from "./incomingGroupNames";
import { incomingGroupCountQueryKey } from "./incomingGroupsQueryKeys";
import {
  type IncomingGroupsRoute,
  incomingGroupMatchingHref,
  incomingGroupsListHref,
} from "./incomingGroupsRoute";
import { useLocalMatching } from "./LocalReleaseEditor";
import { ManualReleasePicker } from "./ManualReleasePicker";
import { ManualSearchPicker } from "./ManualSearchPicker";
import { matchingBoardScoresFromCandidates } from "./MatchingBoard";
import { savedAutomaticAssignments } from "./savedAutomaticAssignments";
import { useIncomingGroupsPanel } from "./useIncomingGroupsPanel";

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
  order: number;
  body: MatchingManualConfirmBody;
};
type AuthorityIntent = {
  groupId: string;
  generation: number;
  intent: string;
  action: "automatic";
  body: MatchingReturnAutomaticBody;
};

type IncomingCreationEditorProps = Omit<
  ComponentProps<typeof IncomingLocalReleaseCreation>,
  "children"
>;

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
  position: MatchingWorkspacePosition,
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

function candidateRows(
  candidates: NonNullable<MatchingReview["candidates"]>,
  releases: readonly MatchingReviewRelease[],
  memberIds: readonly string[],
  copy: IncomingMessages,
  fileLabel: (memberId: string, index: number) => string,
): MatchingScreenViewCandidateRow[] {
  const rows: MatchingScreenViewCandidateRow[] = [];
  for (const candidate of candidates) {
    const fileName = fileLabel(
      candidate.file_member_id,
      memberIds.indexOf(candidate.file_member_id),
    );
    const releaseTitle =
      releases.find(
        (item) =>
          item.release_id === candidate.release_id ||
          item.provider_release_id === candidate.release_id,
      )?.title ?? copy.releaseOutsideLayout;
    const releaseConfidence = Number.isFinite(candidate.release_confidence)
      ? candidate.release_confidence
      : copy.unknown;
    if (candidate.positions?.length) {
      for (const position of candidate.positions) {
        rows.push({
          key: `${candidate.file_member_id}:${candidate.release_id}:${position.track_id}`,
          fileName,
          releaseTitle,
          evidence: `${copy.releaseConfidence} ${releaseConfidence} · ${copy.positions} ${position.displayed_number || position.position + 1} — ${position.assignment_confidence ?? copy.unknown}`,
        });
      }
    } else {
      rows.push({
        key: `${candidate.file_member_id}-${candidate.release_id}`,
        fileName,
        releaseTitle,
        evidence: `${copy.releaseConfidence} ${releaseConfidence}`,
      });
    }
  }
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
  const receipts = useIncomingConfirmReceipts();
  const { locale } = useI18n();
  const copy = incomingMessages[locale];
  const linkCopy = matchingLinksCopy[locale];
  const localMatchingCopy = localMatchingMessages[locale];
  const routeGroupId = route.view === "matching" ? route.groupId : "";
  const routeReleaseId =
    route.view === "matching" ? (route.releaseId ?? "") : "";
  const creatingLocalRelease =
    route.view === "matching" && route.creatingLocalRelease === true;
  const matchingActive = route.view === "matching" && !creatingLocalRelease;
  const preferredLocalReleaseId =
    route.view === "matching" ? route.localReleaseId : undefined;
  const localMode = Boolean(preferredLocalReleaseId);
  const activeLocalReleaseRef = useRef(preferredLocalReleaseId);
  activeLocalReleaseRef.current = preferredLocalReleaseId;
  const prepareReleaseId =
    route.view === "matching" ? route.prepareReleaseId : undefined;
  const [retainedGroupId, setRetainedGroupId] = useState("");
  const [dismissedReceiptId, setDismissedReceiptId] = useState("");
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
  const group = groups.find((candidate) => candidate.id === groupId);
  const groupNames = useMemo(
    () =>
      incomingGroupNames(
        groupsQuery.data?.groups ?? [],
        groupsQuery.data?.files ?? null,
      ),
    [groupsQuery.data],
  );
  const groupsPanel = useIncomingGroupsPanel({
    snapshot: groupsQuery.data,
    groupNames,
    expandedGroupIds:
      route.view === "list" ? route.expandedGroupIds : undefined,
  });
  const [selectedReleaseId, setSelectedReleaseId] = useState("");
  const [inspectedMemberId, setInspectedMemberId] = useState("");
  const [inspectedTrackId, setInspectedTrackId] = useState("");
  const [preparedReleaseMBID, setPreparedReleaseMBID] = useState("");
  const [requestedRelease, setRequestedRelease] = useState<{
    groupId: string;
    id: string;
    sequence: number;
  }>();
  const [preparingRelease, setPreparingRelease] = useState(false);
  const preparationRoute = useRef("");
  const preparationSequence = useRef(0);
  const onPreparationPendingChange = useCallback((pending: boolean) => {
    if (pending) preparationRoute.current = window.location.hash;
    setPreparingRelease(pending);
  }, []);
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
    setRequestedRelease(undefined);
    setPreparingRelease(false);
    setPreview(undefined);
    setConfirmIntent(undefined);
    setAuthorityIntent(undefined);
    setAuthorityNotice(null);
  }, [routeGroupId]);

  useEffect(() => {
    if (!matchingActive || !routeGroupId) return;
    focusView(matchingHeadingRef.current, {
      scroll: { block: "start", inline: "nearest" },
      scrollFirst: true,
      preventScroll: true,
    });
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
  const review = group ? reviewQuery.data : undefined;
  evaluationRevisionRef.current = review?.evaluation_revision ?? "";
  const proposal = parsedProposal(review);
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
    if (
      !matchingActive ||
      !preparedReleaseMBID ||
      !groupId ||
      preparationRoute.current !== window.location.hash
    )
      return;
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
    setPreparedReleaseMBID("");
    window.location.hash = incomingGroupMatchingHref(
      groupId,
      chosen.provider_release_id,
    );
  }, [groupId, matchingActive, preparedReleaseMBID, releases]);

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
  const receiptReleaseId = routeReleaseId || release?.provider_release_id;
  const receipt =
    !localMode && !prepareReleaseId
      ? findIncomingConfirmReceipt(
          receipts,
          "manual",
          groupId,
          receiptReleaseId,
        )
      : undefined;
  const localReceipt = preferredLocalReleaseId
    ? findIncomingConfirmReceipt(
        receipts,
        "local",
        groupId,
        preferredLocalReleaseId,
      )
    : undefined;
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
  function invalidateMatching(originGroupId: string) {
    return Promise.all([
      queryClient.invalidateQueries({
        queryKey: getGetIncomingGroupsQueryKey(),
        exact: true,
      }),
      queryClient.invalidateQueries({ queryKey: incomingGroupCountQueryKey() }),
      queryClient.invalidateQueries({
        queryKey: getGetIncomingGroupMatchingQueryKey(originGroupId),
      }),
    ]);
  }
  const local = useLocalMatching({
    group,
    files,
    authorityRevision: review?.authority_revision,
    preferredReleaseId: preferredLocalReleaseId,
    onConfirmed: async (result, order, requestId, assignments) => {
      saveIncomingConfirmReceipt(queryClient, {
        kind: "local",
        order,
        groupId: result.group_id,
        releaseId: result.provider_release_id,
        requestId,
        response: result,
        assignments,
      });
      if (
        scopeRef.current.groupId === result.group_id &&
        activeLocalReleaseRef.current === result.provider_release_id
      ) {
        invalidateIntent();
        setPreview(undefined);
        setConfirmIntent(undefined);
        setAuthorityIntent(undefined);
      }
      await invalidateMatching(result.group_id);
    },
  });
  const groupFiles = useMemo(() => {
    const ids = new Set(group?.members ?? []);
    return files.filter((file) => ids.has(file.variant_id));
  }, [files, group]);
  const groupMembers = group?.members ?? [];
  const memberIds =
    review?.evaluated && review.members?.length
      ? review.members.filter((id) => groupMembers.includes(id))
      : groupMembers;
  const proposalFiles = new Map(
    (proposal.files ?? []).map((file) => [file.file_member_id, file]),
  );
  const { label: fileLabel, sources: boardSources } = apiMatchingSources(
    memberIds,
    groupFiles,
    proposalFiles,
    copy.file,
  );
  const workspaceMembers = localMode ? (group?.members ?? []) : memberIds;
  const workspaceSources = localMode
    ? apiMatchingSources(workspaceMembers, groupFiles, new Map(), copy.file)
        .sources
    : boardSources;
  const workspaceRelease = localMode ? local.workspaceRelease : release;
  const workspaceAssignments = localMode
    ? local.assignments
    : assignmentsByMember;
  function assignWorkspaceFile(memberId: string, trackId: string) {
    if (!localMode) return assign(memberId, trackId);
    if (
      workspaceBusy ||
      !workspaceMembers.includes(memberId) ||
      (trackId &&
        !workspaceRelease?.positions?.some(
          (track) => track.track_id === trackId,
        ))
    )
      return false;
    if (trackId && workspaceAssignments[memberId] === trackId) return false;
    const next = { ...workspaceAssignments };
    if (trackId) next[memberId] = trackId;
    else delete next[memberId];
    local.setAssignments(next);
    setInspectedMemberId(memberId);
    setInspectedTrackId(trackId);
    return true;
  }
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
  const savedReceipt = localMode
    ? local.previewResult
      ? undefined
      : localReceipt
    : currentPreview
      ? undefined
      : receipt;
  const confirmedMemberIds = new Set(
    savedReceipt?.response.confirmed_sources?.map((row) => row.file_member_id),
  );
  const canContinueMatching =
    !groupsQuery.isFetching &&
    !reviewQuery.isFetching &&
    groupMembers.length > 0 &&
    groupMembers.every((id) => !confirmedMemberIds.has(id));

  useEffect(() => {
    if (!currentPreview || focusedPreview.current === currentPreview) return;
    focusedPreview.current = currentPreview;
    if (!matchingActive) return;
    focusView(previewHeadingRef.current, {
      scroll: { block: "start", inline: "nearest" },
      scrollFirst: true,
      preventScroll: true,
    });
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
    (pendingRequest !== undefined && pendingRequest.groupId === groupId) ||
    Boolean(local.busy);
  const workspaceBusy = sameGroupInFlight || preparingRelease;
  const releaseSelectionLocked =
    workspaceBusy || local.pending || Boolean(currentRetryIntent);

  function authorityScope() {
    const scope = scopeRef.current;
    return `authority:${scope.groupId}:${scope.generation}`;
  }

  async function performAuthorityAction() {
    if (!group || !review || releaseSelectionLocked) return;
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
    if (releaseSelectionLocked) return;
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
      order: nextIncomingConfirmOrder(),
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
      if (response.status !== 200) {
        if (requestScope !== scopeIdentity()) return;
        if (response.status === 409) {
          setError({ key: "confirmConflict", refresh: "evaluation" });
          return;
        }
        throw responseError(response, "confirmError");
      }
      if (
        response.data.group_id !== preserved.groupId ||
        response.data.provider_release_id !== preserved.body.provider_release_id
      ) {
        if (requestScope === scopeIdentity()) setError({ key: "confirmError" });
        return;
      }
      saveIncomingConfirmReceipt(queryClient, {
        kind: "manual",
        order: preserved.order,
        groupId: preserved.groupId,
        releaseId: preserved.body.provider_release_id,
        requestId: preserved.body.request_id,
        response: response.data,
        omittedFileLabels: Object.fromEntries(
          (response.data.omitted_member_ids ?? []).map((memberId) => [
            memberId,
            fileLabel(memberId, memberIds.indexOf(memberId)),
          ]),
        ),
      });
      if (requestScope === scopeIdentity()) {
        setPreview(undefined);
        setConfirmIntent(undefined);
        setNotice({ key: "confirmNotice" });
      }
      await invalidateMatching(preserved.groupId);
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

  const inspectedFileMemberId = workspaceMembers.includes(inspectedMemberId)
    ? inspectedMemberId
    : "";
  const inspectedPosition: MatchingWorkspacePosition | undefined =
    workspaceRelease?.positions?.find(
      (position) => position.track_id === inspectedTrackId,
    );
  const inspectedFile = groupFiles.find(
    (file) => file.variant_id === inspectedFileMemberId,
  );
  const inspectedSavedFacts = localMode
    ? undefined
    : proposalFiles.get(inspectedFileMemberId);
  const inspectedFileTitle =
    inspectedFile?.display_tags?.TITLE?.[0] ||
    inspectedSavedFacts?.raw_tags?.TITLE?.[0] ||
    inspectedFile?.tags?.TITLE?.[0] ||
    inspectedFile?.locations?.[0]?.relative_path.split(/[\\/]/).at(-1) ||
    copy.file;
  const inspectedCandidate = !localMode
    ? review?.candidates?.find(
        (item) =>
          item.file_member_id === inspectedFileMemberId &&
          (item.release_id === release?.release_id ||
            item.release_id === release?.provider_release_id),
      )
    : undefined;

  const matchingHref = incomingGroupMatchingHref(groupId, routeReleaseId, {
    localReleaseId: preferredLocalReleaseId,
    prepareReleaseId,
  });
  const creationHash = creatingLocalRelease ? window.location.hash : "";

  const matchingTitle = `${copy.tabMatching}${
    group ? ` · ${incomingGroupNameText(groupNames.get(group.id), locale)}` : ""
  }`;
  const matchingHeading = {
    backHref: group
      ? incomingGroupsListHref([groupId])
      : incomingGroupsListHref(),
    backLabel: copy.backToGroups,
    title: matchingTitle,
    id: "incoming-matching-title",
    headingRef: matchingHeadingRef,
  };

  const matchingBoardModel: MatchingScreenViewBoard | null = workspaceRelease
    ? {
        groupId,
        release: workspaceRelease,
        members: workspaceMembers,
        sources: workspaceSources,
        assignments: workspaceAssignments,
        automaticAssignments: localMode ? undefined : savedAssignments,
        savedScores: localMode ? undefined : savedScores,
        disabled: workspaceBusy,
        inspectedTrackId: inspectedPosition?.track_id,
        locale,
        onAssign: assignWorkspaceFile,
        onUnassign: (memberId: string) => assignWorkspaceFile(memberId, ""),
        onInspectPosition: (trackId: string) => {
          setInspectedTrackId(trackId);
          const assigned = workspaceMembers.filter(
            (id) => workspaceAssignments[id] === trackId,
          );
          setInspectedMemberId((current) =>
            assigned.includes(current)
              ? current
              : assigned.length === 1
                ? assigned[0]
                : "",
          );
        },
        onInspectSource: (memberId: string) => {
          setInspectedMemberId(memberId);
          setInspectedTrackId(workspaceAssignments[memberId] ?? "");
        },
        onInspectPair: (memberId: string, trackId: string) => {
          setInspectedMemberId(memberId);
          setInspectedTrackId(trackId);
        },
        renderPositionMeta: localMode
          ? undefined
          : ({ position }: MatchingWorkspacePositionContext) => (
              <TrackTagOverrides
                title={positionLabel(position, copy)}
                locale={locale}
                override={
                  tagOverridesByTrack[position.track_id] ??
                  emptyTrackTagOverride()
                }
                disabled={busy || authorityBusy || sameGroupInFlight}
                onChange={(override) =>
                  updateTrackTagOverride(position.track_id, override)
                }
              />
            ),
      }
    : null;

  const emptyBoardModel = {
    positions: {
      heading: copy.positionHeading,
      loading:
        local.status === "loading"
          ? localMatchingCopy.loadingTracks
          : undefined,
      error:
        local.status === "error"
          ? { label: copy.retryReading, onPress: () => void local.read() }
          : undefined,
      instruction: copy.choosePrepareRelease,
    },
    sources: {
      heading: copy.sourceFilesHeading,
      instruction: copy.sourceInstructions,
      members: memberIds.map((memberId, index) => ({
        key: memberId,
        label: fileLabel(memberId, index),
      })),
      emptyText: copy.noFilesInGroup,
    },
  };

  const reviewSelectionModel =
    review?.evaluated && review.proposal_available
      ? {
          releaseSelection: {
            heading: copy.savedEvaluation,
            label: copy.releaseSelectionLabel,
            disabled: releaseSelectionLocked,
            value: localMode ? "" : selectedReleaseId,
            placeholder: copy.chooseRelease,
            options: releases.map((item) => ({
              value: item.provider_release_id,
              label: layoutLabel(item, releases, copy),
            })),
            onChange: (value: string) => changeRelease(value),
            savedText:
              !localMode &&
              savedSelectedId &&
              selectedReleaseId === savedSelectedId
                ? copy.savedRelease
                : undefined,
          },
          candidates: {
            heading: copy.savedCandidatesHeading,
            rows: candidateRows(
              review.candidates ?? [],
              releases,
              memberIds,
              copy,
              fileLabel,
            ),
            emptyText: copy.noCandidateEdges,
          },
        }
      : undefined;

  const evaluationModel = {
    loading: reviewQuery.isLoading ? copy.loadingEvaluation : undefined,
    error: reviewQuery.isError
      ? {
          text: incomingFeedbackText(
            locale,
            errorText(reviewQuery.error, "loadReviewError"),
          ),
          retry: {
            label: copy.retryReading,
            disabled: sameGroupInFlight,
            onPress: () => {
              void reviewQuery.refetch();
            },
          },
        }
      : undefined,
    notRun: review && !review.evaluated ? copy.evaluationNotRun : undefined,
    unavailable:
      review?.evaluated && !review.proposal_available
        ? copy.proposalUnavailable
        : undefined,
    noReleases:
      review?.evaluated &&
      review.proposal_available &&
      review.releases?.length === 0
        ? copy.noReleaseLayouts
        : undefined,
  };

  const authorityModel = review
    ? {
        heading: copy.authorityHeading,
        modeLabel: copy.modeLabel,
        mode:
          review.authority_mode === "manual"
            ? copy.manualMatching
            : copy.automaticMatching,
        explanation: copy.savedEvaluationExplanation,
        notice: authorityNotice
          ? incomingFeedbackText(locale, authorityNotice)
          : undefined,
        returnToAutomatic:
          review.authority_mode === "manual"
            ? {
                label:
                  currentAuthorityIntent?.action === "automatic"
                    ? copy.retryReturnAutomatic
                    : copy.returnAutomatic,
                disabled: workspaceBusy,
                onPress: () => void performAuthorityAction(),
              }
            : undefined,
        refresh:
          error?.refresh === "authority"
            ? {
                label: copy.refreshAuthority,
                disabled: sameGroupInFlight,
                onPress: () => {
                  setAuthorityIntent(undefined);
                  void Promise.all([
                    groupsQuery.refetch(),
                    reviewQuery.refetch(),
                  ]);
                },
              }
            : undefined,
      }
    : undefined;

  const inspectorContext = [
    inspectedFileMemberId ? inspectedFileTitle : "",
    inspectedPosition ? positionLabel(inspectedPosition, copy) : "",
  ]
    .filter(Boolean)
    .join(" → ");

  const inspectorButtons: MatchingScreenViewAction[] = [];
  if (localMode) {
    inspectorButtons.push({
      label: local.busy === "preview" ? copy.processing : linkCopy.preview,
      disabled: workspaceBusy || !local.canPreview,
      onPress: () => void local.preview(),
    });
    if (local.pending && local.release && !local.previewResult) {
      inspectorButtons.push({
        label: copy.retryConfirmation,
        disabled: workspaceBusy || !local.canConfirm,
        onPress: () => void local.confirm(),
      });
    }
  } else {
    inspectorButtons.push({
      label: busy && !currentRetryIntent ? copy.processing : linkCopy.preview,
      disabled: workspaceBusy || !release || !review?.evaluation_revision,
      onPress: () => void createPreview(),
    });
    if (error?.refresh === "evaluation") {
      inspectorButtons.push({
        label: copy.refreshEvaluation,
        disabled: sameGroupInFlight,
        onPress: () => void reloadReview(),
      });
    }
  }

  const inspectorModel = {
    ariaLabel: copy.inspectorLabel,
    title: copy.inspectorLabel,
    context: inspectorContext || undefined,
    evidence:
      inspectedFileMemberId && inspectedPosition
        ? {
            position: inspectedPosition,
            rawTags:
              inspectedSavedFacts?.raw_tags ?? inspectedFile?.tags ?? undefined,
            displayTags: inspectedFile?.display_tags ?? undefined,
            candidate: inspectedCandidate,
          }
        : null,
    emptyText: copy.inspectAssignment,
    actions: {
      ariaLabel: copy.assignmentActions,
      error: localMode
        ? local.error
        : error
          ? incomingFeedbackText(locale, error)
          : undefined,
      notice: localMode
        ? local.notice
          ? localMatchingCopy.notice(local.notice)
          : undefined
        : notice
          ? incomingFeedbackText(locale, notice)
          : undefined,
      stale:
        localMode && local.stale ? localMatchingCopy.staleNotice : undefined,
      buttons: inspectorButtons,
    },
  };

  const summaryTableModel =
    review?.evaluated && review.proposal_available
      ? {
          label: copy.groupSummaryLabel,
          heading: copy.groupDraftHeading,
          columns: [
            copy.uniqueFilesPaths,
            copy.draftAssignedTotal,
            copy.sourceSnapshot,
            copy.providerSnapshot,
          ] as const,
          values: [
            `${new Set(memberIds).size} / ${
              new Set(
                groupFiles.flatMap(
                  (file) =>
                    file.locations?.map(
                      (location) =>
                        `${location.root_id}:${location.relative_path}`,
                    ) ?? [],
                ),
              ).size
            }`,
            `${
              Object.keys(assignmentsByMember).filter((memberId) =>
                memberIds.includes(memberId),
              ).length
            } / ${memberIds.length}`,
            copy.savedFacts(
              groupFiles.filter(
                (file) => file.capture_identity || file.locations?.length,
              ).length,
              groupFiles.length,
            ),
            review.candidates?.length
              ? copy.savedCandidates(review.candidates.length)
              : copy.noCandidates,
          ] as const,
          note: `${copy.linkedManualFiles} ${
            review.current_linked_manual_media_source_ids?.length ?? 0
          }. ${copy.draftDoesNotChangeLinks}`,
        }
      : undefined;

  const localPreviewModel: MatchingScreenViewPreview | null =
    group && localMode && local.previewResult
      ? {
          heading: linkCopy.preview,
          releaseTitle: local.previewResult.release_title,
          summary: [
            linkCopy.assignedFiles(
              local.previewResult.sources?.filter(
                ({ file_member_id }) => workspaceAssignments[file_member_id],
              ).length ?? 0,
            ),
            linkCopy.remainingFiles(
              local.previewResult.omitted_file_member_ids?.length ?? 0,
            ),
          ].join(" · "),
          rows: [
            ...(local.previewResult.sources ?? []).map((source, index) => {
              const position = workspaceRelease?.positions?.find(
                (track) =>
                  track.track_id ===
                  workspaceAssignments[source.file_member_id],
              );
              return {
                key: source.file_member_id,
                source:
                  source.locations?.find(Boolean) ??
                  fileLabel(source.file_member_id, index),
                position: position ? positionLabel(position, copy) : undefined,
              };
            }),
            ...(local.previewResult.omitted_file_member_ids ?? []).map(
              (id) => ({
                key: id,
                source: fileLabel(id, workspaceMembers.indexOf(id)),
              }),
            ),
          ],
          primary: {
            label: local.pending ? copy.retryConfirmation : linkCopy.save,
            onPress: () => void local.confirm(),
            disabled: workspaceBusy || !local.canConfirm,
          },
        }
      : null;

  const appliedOverrides = (release?.positions ?? []).flatMap((position) => {
    const override = tagOverridesByTrack[position.track_id];
    if (!override) return [];
    if (!Object.values(assignmentsByMember).includes(position.track_id))
      return [];
    if (!override.title.enabled && !override.artists.enabled) return [];
    return [
      {
        key: position.track_id,
        title: position.title,
        text: [
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
          .join("; "),
      },
    ];
  });

  const manualPreviewModel: MatchingScreenViewPreview | null =
    !localMode && currentPreview
      ? {
          heading: linkCopy.preview,
          headingRef: previewHeadingRef,
          releaseTitle: currentPreview.selected_release.title,
          summary: [
            linkCopy.assignedFiles(
              currentPreview.members?.filter((member) => member.assignment)
                .length ?? 0,
            ),
            linkCopy.remainingFiles(
              currentPreview.omitted_member_ids?.length ?? 0,
            ),
          ].join(" · "),
          rows: previewRows(currentPreview, copy, fileLabel),
          primary: {
            label: currentRetryIntent ? copy.retryConfirmation : linkCopy.save,
            onPress: () => void confirm(),
            disabled: sameGroupInFlight,
          },
          details: appliedOverrides.length
            ? {
                heading: copy.manualMetadataApplied,
                items: appliedOverrides,
              }
            : undefined,
        }
      : null;

  let matchingModel: MatchingScreenViewModel;
  if (
    matchingActive &&
    savedReceipt &&
    dismissedReceiptId !== savedReceipt.requestId
  ) {
    matchingModel = {
      kind: "receipt",
      locale,
      hidden: !matchingActive,
      heading: matchingHeading,
      body: {
        kind: "savedReceipt",
        content: (
          <IncomingSavedMatchingResult
            key={savedReceipt.requestId}
            receipt={savedReceipt}
            locale={locale}
            onContinue={
              canContinueMatching
                ? () => setDismissedReceiptId(savedReceipt.requestId)
                : undefined
            }
          />
        ),
      },
    };
  } else if (!group) {
    matchingModel = groupsQuery.isLoading
      ? {
          kind: "loading",
          locale,
          hidden: !matchingActive,
          heading: matchingHeading,
          message: copy.loadingGroups,
        }
      : groupsQuery.isError
        ? {
            kind: "error",
            locale,
            hidden: !matchingActive,
            heading: matchingHeading,
            message: incomingFeedbackText(
              locale,
              errorText(groupsQuery.error, "groupsLoadError"),
            ),
          }
        : {
            kind: "missing",
            locale,
            hidden: !matchingActive,
            heading: matchingHeading,
            message: copy.groupNotFound,
          };
  } else {
    matchingModel = {
      kind: "workspace",
      locale,
      hidden: !matchingActive,
      heading: matchingHeading,
      status: {
        loading: groupsQuery.isLoading ? copy.loadingGroups : undefined,
        error: groupsQuery.isError
          ? incomingFeedbackText(
              locale,
              errorText(groupsQuery.error, "groupsLoadError"),
            )
          : undefined,
      },
      draftHint: copy.matchingDraftHint,
      groupSelect:
        groups.length > 0
          ? {
              label: copy.incomingGroupsLabel,
              value: groupId,
              options: groups.map((item) => ({
                value: item.id,
                label: copy.groupOption(
                  incomingGroupNameText(groupNames.get(item.id), locale),
                  item.members?.length ?? 0,
                  item.manual,
                ),
              })),
              onChange: (value: string) => changeGroup(value),
            }
          : undefined,
      encoding: (
        <IncomingTagEncodingReview
          key={group.id}
          groupId={group.id}
          disabled={sameGroupInFlight}
        />
      ),
      evaluation: evaluationModel,
      authority: authorityModel,
      search: {
        bind: (children: ReactNode) => (
          <ManualSearchPicker
            groupId={groupId}
            groupRevision={group.revision}
            disabled={!review || releaseSelectionLocked}
            selectedReleaseId={localMode ? undefined : release?.release_id}
            selectedLocalReleaseId={preferredLocalReleaseId}
            onSelectLocalRelease={(id) => {
              if (releaseSelectionLocked || id === preferredLocalReleaseId)
                return;
              applyReleaseSelection("");
              window.location.hash = incomingGroupMatchingHref(groupId, "", {
                localReleaseId: id,
              });
            }}
            onSelectRelease={(id) => {
              if (releaseSelectionLocked) return;
              const existing = releases.find((item) => item.release_id === id);
              if (existing) {
                changeRelease(existing.provider_release_id);
                return;
              }
              preparationRoute.current = window.location.hash;
              setRequestedRelease({
                groupId,
                id,
                sequence: ++preparationSequence.current,
              });
            }}
          >
            {children}
          </ManualSearchPicker>
        ),
      },
      preparation: (
        <ManualReleasePicker
          groupId={groupId}
          groupRevision={group.revision}
          evaluationRevision={review?.evaluation_revision || undefined}
          disabled={!review || releaseSelectionLocked}
          searchRelease={prepareReleaseId}
          requestedRelease={
            requestedRelease?.groupId === groupId ? requestedRelease : undefined
          }
          onPendingChange={onPreparationPendingChange}
          onPrepared={(id) => {
            if (preparationRoute.current === window.location.hash) {
              setPreparedReleaseMBID(id);
            }
          }}
        />
      ),
      createLocalReleaseHref: releaseSelectionLocked
        ? undefined
        : incomingGroupMatchingHref(group.id, routeReleaseId, {
            localReleaseId: preferredLocalReleaseId,
            prepareReleaseId,
            creatingLocalRelease: true,
          }),
      review: reviewSelectionModel,
      board: matchingBoardModel,
      emptyBoard: emptyBoardModel,
      summaryTable: summaryTableModel,
      inspector: inspectorModel,
      preview: manualPreviewModel ?? localPreviewModel,
    };
  }

  const listModel: ApplicationIncomingListModel = {
    locale,
    hidden: matchingActive || creatingLocalRelease,
    headingAction: {
      label: copy.refreshGroups,
      onPress: () => void groupsQuery.refetch(),
    },
    loading: groupsQuery.isLoading ? copy.loadingGroups : undefined,
    error: groupsQuery.isError
      ? incomingFeedbackText(
          locale,
          errorText(groupsQuery.error, "groupsLoadError"),
        )
      : undefined,
    panel: groupsPanel,
  };

  const creation: IncomingCreationSource<IncomingCreationEditorProps> | null =
    creatingLocalRelease
      ? group && groupsQuery.data
        ? {
            kind: "editor",
            key: group.id,
            Component: IncomingLocalReleaseCreation,
            props: {
              group,
              files: files.filter((file) =>
                [
                  ...(group.members ?? []),
                  ...(group.unready_members ?? []),
                ].includes(file.variant_id),
              ),
              label: incomingGroupNameText(groupNames.get(group.id), locale),
              onCancel: () => window.location.replace(matchingHref),
              onSelect: (id, kind) => {
                if (window.location.hash !== creationHash) return;
                const next = incomingGroupMatchingHref(
                  group.id,
                  kind === "local" ? "" : routeReleaseId,
                  {
                    localReleaseId: kind === "local" ? id : undefined,
                    prepareReleaseId: kind === "musicbrainz" ? id : undefined,
                  },
                );
                window.location.replace(next);
              },
            },
          }
        : {
            kind: "fallback",
            fallback: {
              backHref: matchingHref,
              backLabel: copy.tabMatching,
              feedback: groupsQuery.isLoading
                ? { kind: "loading", message: copy.loadingGroups }
                : {
                    kind: "error",
                    message: groupsQuery.isError
                      ? incomingFeedbackText(
                          locale,
                          errorText(groupsQuery.error, "groupsLoadError"),
                        )
                      : copy.groupNotFound,
                  },
            },
          }
      : null;

  return (
    <IncomingWorkspaceView<IncomingCreationEditorProps>
      creation={creation}
      list={listModel}
      matching={groupId ? matchingModel : null}
    />
  );
}
