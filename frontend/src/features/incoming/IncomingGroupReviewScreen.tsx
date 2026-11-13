import {
  QueryClient,
  QueryClientProvider,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
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
import { IncomingGroupingEditor } from "./IncomingGroupingEditor";
import { IncomingGroupsOverview } from "./IncomingGroupsOverview";
import {
  type IncomingFeedback,
  type IncomingMessageKey,
  type IncomingMessages,
  incomingFeedbackText,
  incomingMessages,
} from "./incomingMessages";
import { LocalReleaseEditor } from "./LocalReleaseEditor";
import { localMatchingMessages } from "./localMatchingMessages";
import { ManualReleasePicker } from "./ManualReleasePicker";
import { ManualSearchPicker } from "./ManualSearchPicker";
import { MatchingEvidenceInspector } from "./MatchingEvidenceInspector";
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
  if (reason instanceof Error && (reason as IncomingError).incomingMessageKey) {
    const error = reason as IncomingError;
    return {
      key: error.incomingMessageKey!,
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

function Metadata({
  title,
  tags,
  copy,
}: {
  title: string;
  tags: Record<string, string[] | null>;
  copy: IncomingMessages;
}) {
  const entries = Object.entries(tags);
  return (
    <details className="mt-2 text-sm">
      <summary className="cursor-pointer">{title}</summary>
      {entries.length ? (
        <dl className="mt-2 grid gap-1 sm:grid-cols-2">
          {entries.map(([key, values]) => (
            <div key={key} className="rounded border border-stone-200 p-2">
              <dt className="font-semibold">{key}</dt>
              <dd>{values?.length ? values.join(" · ") : copy.noValue}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p>{copy.metadataNotSaved}</p>
      )}
    </details>
  );
}

export function IncomingGroupReviewScreen() {
  const [queryClient] = useState(() => new QueryClient());
  return (
    <QueryClientProvider client={queryClient}>
      <IncomingGroupReviewContent />
    </QueryClientProvider>
  );
}

function IncomingGroupReviewContent() {
  const queryClient = useQueryClient();
  const { locale } = useI18n();
  const copy = incomingMessages[locale];
  const localMatchingCopy = localMatchingMessages[locale];
  const groupsQuery = useQuery({
    queryKey: getGetIncomingGroupsQueryKey(),
    queryFn: async ({ signal }) => {
      const result = await getIncomingGroups({ signal, cache: "no-store" });
      if (result.status !== 200) throw responseError(result, "loadGroupsError");
      return result.data;
    },
  });
  const groups = groupsQuery.data?.groups ?? [];
  const [groupId, setGroupId] = useState("");
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
  const authorityRevisionByGroup = useRef(new Map<string, number>());

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

  useEffect(() => {
    if (!groupId && groups.length) setGroupId(groups[0].id);
    if (groupId && !groups.some((group) => group.id === groupId)) {
      generation.current += 1;
      scopeRef.current = {
        groupId: groups[0]?.id ?? "",
        releaseId: "",
        generation: generation.current,
      };
      setGroupId(groups[0]?.id ?? "");
    }
  }, [groupId, groups]);

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
    enabled: Boolean(groupId),
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

  const release = releases.find(
    (candidate) => candidate.provider_release_id === selectedReleaseId,
  );
  const draftKey = `${groupId}:${selectedReleaseId}`;
  const assignmentsByMember = drafts[draftKey] ?? {};
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
  const fileLabel = (memberId: string, index: number) => {
    const file = groupFiles.find((item) => item.variant_id === memberId);
    const path = file?.locations?.[0]?.relative_path;
    if (path) return path;
    const tags = proposalFiles.get(memberId)?.raw_tags ?? file?.tags;
    const title = tags?.TITLE?.find(Boolean);
    const artist = tags?.ARTIST?.find(Boolean);
    return (
      [title, artist].filter(Boolean).join(" · ") ||
      `${copy.file} ${index >= 0 ? index + 1 : 1}`
    );
  };
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
    generation.current += 1;
    scopeRef.current = {
      groupId: nextGroupId,
      releaseId: "",
      generation: generation.current,
    };
    setGroupId(nextGroupId);
    setSelectedReleaseId("");
    setPreparedReleaseMBID("");
    setPreview(undefined);
    setConfirmIntent(undefined);
    setReceipt(undefined);
    setAuthorityIntent(undefined);
    setAuthorityNotice(null);
  }

  function changeRelease(nextReleaseId: string) {
    if (sameGroupInFlight) return;
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
  }

  function assign(memberId: string, trackId: string) {
    if (sameGroupInFlight) return;
    invalidateIntent();
    setDrafts((current) => {
      const draft = { ...(current[draftKey] ?? {}) };
      if (trackId) draft[memberId] = trackId;
      else delete draft[memberId];
      return { ...current, [draftKey]: draft };
    });
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
    : (memberIds[0] ?? "");
  const inspectedPosition =
    release?.positions?.find(
      (position) => position.track_id === inspectedTrackId,
    ) ??
    [...(release?.positions ?? [])].sort(
      (a, b) => a.medium - b.medium || a.position - b.position,
    )[0];
  const inspectedFile = groupFiles.find(
    (file) => file.variant_id === inspectedFileMemberId,
  );
  const inspectedSavedFacts = proposalFiles.get(inspectedFileMemberId);
  const inspectedCandidate = review?.candidates?.find(
    (item) =>
      item.file_member_id === inspectedFileMemberId &&
      (item.release_id === release?.release_id ||
        item.release_id === release?.provider_release_id),
  );

  return (
    <section
      className="incoming-screen mx-auto max-w-[1800px] space-y-5 min-[1400px]:flex min-[1400px]:h-full min-[1400px]:min-h-0 min-[1400px]:flex-col min-[1400px]:overflow-y-auto"
      aria-labelledby="incoming-title"
    >
      <div className="flex shrink-0 flex-wrap items-end justify-between gap-3">
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

      {groupsQuery.isLoading ? <p role="status">{copy.loadingGroups}</p> : null}
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

      {groups.length > 0 ? (
        <>
          <div className="space-y-5 min-[1400px]:max-h-[30dvh] min-[1400px]:shrink-0 min-[1400px]:overflow-y-auto">
            {groupsQuery.data ? (
              <details className="rounded border border-stone-300 p-3">
                <summary className="cursor-pointer font-semibold">
                  {copy.groupEditingSummary}
                </summary>
                <IncomingGroupingEditor
                  snapshot={groupsQuery.data}
                  onConfirmed={async (snapshot) => {
                    invalidateIntent();
                    setPreview(undefined);
                    setConfirmIntent(undefined);
                    setAuthorityIntent(undefined);
                    queryClient.setQueryData(
                      getGetIncomingGroupsQueryKey(),
                      snapshot,
                    );
                    await queryClient.invalidateQueries({
                      queryKey: getGetIncomingGroupMatchingQueryKey(groupId),
                    });
                  }}
                />
              </details>
            ) : null}
            {groupsQuery.data ? (
              <IncomingGroupsOverview
                snapshot={groupsQuery.data}
                selectedGroupId={groupId}
                review={review}
                onSelect={changeGroup}
              />
            ) : null}
            <label className="block max-w-2xl text-sm font-medium">
              {copy.incomingGroupsLabel}
              <select
                className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
                value={groupId}
                onChange={(event) => changeGroup(event.target.value)}
              >
                {groups.map((item, index) => (
                  <option key={item.id} value={item.id}>
                    {copy.groupOption(
                      index + 1,
                      item.members?.length ?? 0,
                      item.manual,
                    )}
                  </option>
                ))}
              </select>
            </label>

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
                  {copy.modeLabel} <strong>{review.authority_mode}</strong> ·{" "}
                  {copy.revisionLabel} <code>{review.authority_revision}</code>
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
          </div>

          {review?.evaluated && review.proposal_available ? (
            <div
              data-matching-workspace
              className="grid min-h-0 gap-3 min-[1400px]:min-h-[360px] min-[1400px]:flex-1 min-[1400px]:grid-cols-[minmax(320px,.85fr)_minmax(480px,2.1fr)_minmax(250px,.8fr)] min-[1400px]:grid-rows-[minmax(0,1fr)_minmax(180px,min(32dvh,340px))]"
            >
              <section
                data-matching-candidates
                className="min-w-0 space-y-3 rounded border border-stone-300 bg-white p-3 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto"
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
                <p className="text-sm">
                  {copy.reviewRevisionParticipants(
                    review.evaluation_revision || copy.unknown,
                    memberIds.length,
                  )}
                </p>
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
                <h3 className="font-medium">{copy.savedCandidatesHeading}</h3>
                {review.candidates?.length ? (
                  <ul className="space-y-2 text-sm">
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
                                  memberIds.indexOf(candidate.file_member_id),
                                )}
                              </strong>
                              <p>
                                {releases.find(
                                  (item) =>
                                    item.release_id === candidate.release_id ||
                                    item.provider_release_id ===
                                      candidate.release_id,
                                )?.title ?? copy.releaseOutsideLayout}
                              </p>
                              <p>
                                {copy.releaseConfidence}{" "}
                                {Number.isFinite(candidate.release_confidence)
                                  ? candidate.release_confidence
                                  : copy.unknown}{" "}
                                · {copy.positions}{" "}
                                {position.displayed_number ||
                                  position.position + 1}{" "}
                                —{" "}
                                {position.assignment_confidence ?? copy.unknown}
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
                                  memberIds.indexOf(candidate.file_member_id),
                                )}
                              </strong>
                              <p>
                                {releases.find(
                                  (item) =>
                                    item.release_id === candidate.release_id ||
                                    item.provider_release_id ===
                                      candidate.release_id,
                                )?.title ?? copy.releaseOutsideLayout}
                              </p>
                              <p>
                                {copy.releaseConfidence}{" "}
                                {Number.isFinite(candidate.release_confidence)
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
              </section>

              {release ? (
                <section
                  data-matching-positions
                  className="min-w-0 rounded border border-stone-300 bg-white p-3 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto"
                  aria-labelledby="assignment-board-title"
                >
                  <h2 id="assignment-board-title" className="font-semibold">
                    {copy.positionHeading} · {release.title}
                  </h2>
                  <p className="mt-1 text-sm text-stone-700">
                    {copy.positionInstructions}
                  </p>
                  <p className="mt-1 text-sm text-stone-700">
                    {release.tracklist_complete
                      ? copy.completeTrackList
                      : copy.incompleteTrackList}{" "}
                    · {copy.positionCount(release.positions?.length ?? 0)}
                  </p>
                  <ol className="mt-3 space-y-2">
                    {[...(release.positions ?? [])]
                      .sort(
                        (a, b) =>
                          a.medium - b.medium || a.position - b.position,
                      )
                      .map((position) => {
                        const occupants = memberIds.filter(
                          (memberId) =>
                            assignmentsByMember[memberId] === position.track_id,
                        );
                        return (
                          <li
                            key={position.track_id}
                            onDragOver={(event) => event.preventDefault()}
                            onDrop={(event) => {
                              event.preventDefault();
                              const memberId =
                                event.dataTransfer.getData("text/plain");
                              if (memberIds.includes(memberId))
                                assign(memberId, position.track_id);
                            }}
                            className={`min-h-24 rounded border border-dashed p-3 text-sm ${inspectedPosition?.track_id === position.track_id ? "border-stone-600 bg-stone-50" : "border-stone-400"}`}
                          >
                            <button
                              type="button"
                              className="text-left font-medium underline decoration-stone-400 underline-offset-2"
                              aria-pressed={
                                inspectedPosition?.track_id ===
                                position.track_id
                              }
                              onClick={() =>
                                setInspectedTrackId(position.track_id)
                              }
                            >
                              {positionLabel(position, copy)}
                            </button>
                            <p className="mt-1 text-stone-700">
                              {occupants.length
                                ? `${copy.assignedFiles(occupants.length)} · ${occupants.map((memberId) => fileLabel(memberId, memberIds.indexOf(memberId))).join(", ")}`
                                : copy.noAssignedFiles}
                            </p>
                            <TrackTagOverrides
                              title={positionLabel(position, copy)}
                              trackId={position.track_id}
                              override={
                                tagOverridesByTrack[position.track_id] ??
                                emptyTrackTagOverride()
                              }
                              disabled={
                                busy || authorityBusy || sameGroupInFlight
                              }
                              onChange={(override) =>
                                updateTrackTagOverride(
                                  position.track_id,
                                  override,
                                )
                              }
                            />
                          </li>
                        );
                      })}
                  </ol>
                </section>
              ) : (
                <section className="rounded border border-stone-300 bg-white p-3">
                  <h2 className="font-semibold">{copy.positionHeading}</h2>
                  <p className="mt-2 text-sm">{copy.choosePrepareRelease}</p>
                </section>
              )}

              <section
                data-matching-sources
                className="min-w-0 space-y-3 rounded border border-stone-300 bg-white p-3 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto"
                aria-labelledby="matching-sources-title"
              >
                <h2 id="matching-sources-title" className="font-semibold">
                  {copy.sourceFilesHeading}
                </h2>
                <p className="text-sm text-stone-700">
                  {copy.sourceInstructions}
                </p>
                {memberIds.map((memberId, index) => {
                  const file = groupFiles.find(
                    (item) => item.variant_id === memberId,
                  );
                  const savedFacts = proposalFiles.get(memberId);
                  const isFree = !assignmentsByMember[memberId];
                  const assignedPosition = release?.positions?.find(
                    (position) =>
                      position.track_id === assignmentsByMember[memberId],
                  );
                  return (
                    <article
                      key={memberId}
                      className="rounded border border-stone-200 p-3"
                    >
                      <button
                        type="button"
                        draggable={
                          isFree &&
                          !busy &&
                          !authorityBusy &&
                          !sameGroupInFlight
                        }
                        disabled={busy || authorityBusy || sameGroupInFlight}
                        aria-pressed={inspectedFileMemberId === memberId}
                        onClick={() => setInspectedMemberId(memberId)}
                        onDragStart={(event) => {
                          event.dataTransfer.setData("text/plain", memberId);
                          event.dataTransfer.effectAllowed = "move";
                        }}
                        className={`w-full rounded border p-2 text-left text-sm ${inspectedFileMemberId === memberId ? "border-stone-600 bg-stone-50" : "border-stone-400 bg-stone-50"}`}
                      >
                        <strong className="block">
                          {fileLabel(memberId, index)}
                        </strong>
                        {file?.locations?.map((location) => (
                          <span
                            key={location.location_id}
                            className="block text-stone-700"
                          >
                            {location.relative_path}
                          </span>
                        ))}
                        <span className="mt-1 block text-xs text-stone-700">
                          {isFree
                            ? copy.free
                            : assignedPosition
                              ? copy.assigned(
                                  positionLabel(assignedPosition, copy),
                                )
                              : copy.assignmentOutsideRelease}
                        </span>
                      </button>
                      <label className="mt-2 block text-sm font-medium">
                        {copy.assignment}
                        <select
                          disabled={
                            !release ||
                            busy ||
                            authorityBusy ||
                            sameGroupInFlight
                          }
                          className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
                          value={assignmentsByMember[memberId] ?? ""}
                          onChange={(event) =>
                            assign(memberId, event.target.value)
                          }
                        >
                          <option value="">{copy.leaveIncoming}</option>
                          {(release?.positions ?? []).map((position) => (
                            <option
                              key={position.track_id}
                              value={position.track_id}
                            >
                              {positionLabel(position, copy)}
                            </option>
                          ))}
                        </select>
                      </label>
                      {savedFacts?.raw_tags ? (
                        <Metadata
                          title={copy.savedProposalMetadata}
                          tags={savedFacts.raw_tags}
                          copy={copy}
                        />
                      ) : null}
                      {file ? (
                        <Metadata
                          title={copy.incomingFileMetadata}
                          tags={file.tags}
                          copy={copy}
                        />
                      ) : null}
                      {savedFacts?.diagnostics?.length ? (
                        <div className="mt-2 text-sm">
                          <p className="font-medium">{copy.groupDiagnostics}</p>
                          <ul className="list-inside list-disc">
                            {savedFacts.diagnostics.map((diagnostic) => (
                              <li
                                key={`${diagnostic.code}-${diagnostic.values?.join("-") ?? ""}`}
                              >
                                {diagnostic.code}
                                {diagnostic.values?.length
                                  ? `: ${diagnostic.values.join(" · ")}`
                                  : ""}
                              </li>
                            ))}
                          </ul>
                        </div>
                      ) : null}
                    </article>
                  );
                })}
                {memberIds.length === 0 ? (
                  <p className="text-sm text-stone-600">
                    {copy.noFilesInGroup}
                  </p>
                ) : null}
              </section>

              <section
                data-matching-inspector
                className="min-w-0 rounded border border-stone-300 bg-white p-3 min-[1400px]:col-span-3 min-[1400px]:grid min-[1400px]:min-h-0 min-[1400px]:grid-cols-[minmax(0,1fr)_minmax(220px,auto)] min-[1400px]:gap-3"
                aria-labelledby="matching-inspector-title"
              >
                <div className="min-w-0 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto">
                  <h2 id="matching-inspector-title" className="font-semibold">
                    {copy.inspectorLabel}
                    {": "}
                    {inspectedFileMemberId
                      ? fileLabel(
                          inspectedFileMemberId,
                          memberIds.indexOf(inspectedFileMemberId),
                        )
                      : copy.inspectorFileMissing}
                    {inspectedPosition
                      ? ` · ${positionLabel(inspectedPosition, copy)}`
                      : ""}
                  </h2>
                  <MatchingEvidenceInspector
                    candidate={inspectedCandidate}
                    rawTags={
                      inspectedSavedFacts?.raw_tags ?? inspectedFile?.tags
                    }
                  />
                </div>
                <aside
                  data-matching-actions
                  className="flex flex-wrap content-start items-start gap-2 min-[1400px]:min-h-0 min-[1400px]:min-w-[220px] min-[1400px]:flex-col min-[1400px]:overflow-y-auto"
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
                  {currentPreview ? (
                    <AppButton
                      isDisabled={sameGroupInFlight}
                      onPress={() => void confirm()}
                    >
                      {currentRetryIntent
                        ? copy.retryConfirmation
                        : copy.confirmExplicitly}
                    </AppButton>
                  ) : null}
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
            </div>
          ) : null}

          {currentPreview ? (
            <section
              className="rounded border border-sky-700 bg-white p-4 min-[1400px]:max-h-[20dvh] min-[1400px]:min-h-0 min-[1400px]:shrink-0 min-[1400px]:overflow-y-auto"
              aria-labelledby="preview-title"
            >
              <h2 id="preview-title" className="font-semibold">
                {copy.previewHeading}
              </h2>
              <p>
                {copy.previewSummary(
                  currentPreview.selected_release.title,
                  currentPreview.members?.filter((member) => member.assignment)
                    .length ?? 0,
                  currentPreview.omitted_member_ids?.length ?? 0,
                )}
              </p>
              <p className="text-sm">
                {copy.previewDigest}{" "}
                <code>{currentPreview.preview_digest}</code>
              </p>
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
                  <h3 className="font-medium">{copy.manualMetadataApplied}</h3>
                  <ul className="list-inside list-disc">
                    {release.positions?.map((position) => {
                      const override = tagOverridesByTrack[position.track_id];
                      if (
                        !Object.values(assignmentsByMember).includes(
                          position.track_id,
                        ) ||
                        (!override?.title.enabled && !override?.artists.enabled)
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
            </section>
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
                {(receipt.response.omitted_member_ids ?? []).map((memberId) => (
                  <li key={memberId}>
                    {receipt.omittedFileLabels[memberId] ?? copy.file}{" "}
                    {copy.remainsIncoming}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
