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
import { IncomingGroupingEditor } from "./IncomingGroupingEditor";
import { IncomingGroupsOverview } from "./IncomingGroupsOverview";
import { LocalReleaseEditor } from "./LocalReleaseEditor";
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

function errorText(reason: unknown, fallback: string) {
  return sharedErrorMessage(reason, fallback) || fallback;
}

function responseError(
  response: { data: { detail?: string }; status: number },
  fallback: string,
) {
  return apiResponseError(response, `${fallback} (HTTP ${response.status}).`);
}

function parsedProposal(review: MatchingReview | undefined): ReviewProposal {
  if (!review?.proposal || typeof review.proposal !== "object") return {};
  return review.proposal as ReviewProposal;
}

function layoutLabel(
  release: MatchingReviewRelease,
  releases: MatchingReviewRelease[],
) {
  const sameTitle = releases.filter((item) => item.title === release.title);
  const duplicateTitle = sameTitle.length > 1;
  const ordinal = sameTitle.indexOf(release);
  return `${release.title}${release.tracklist_complete ? "" : " · неполный список"}${duplicateTitle ? ` · издание ${ordinal + 1}` : ""}`;
}

function positionLabel(
  position: NonNullable<MatchingReviewRelease["positions"]>[number],
) {
  const number = position.displayed_number || `${position.position + 1}`;
  return `Диск ${position.medium + 1} · трек ${number} · ${position.title}`;
}

function Metadata({
  title,
  tags,
}: {
  title: string;
  tags: Record<string, string[] | null>;
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
              <dd>{values?.length ? values.join(" · ") : "Нет значения"}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p>Метаданные не сохранены.</p>
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
  const groupsQuery = useQuery({
    queryKey: getGetIncomingGroupsQueryKey(),
    queryFn: async ({ signal }) => {
      const result = await getIncomingGroups({ signal, cache: "no-store" });
      if (result.status !== 200)
        throw responseError(result, "Не удалось загрузить входящие группы");
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
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [authorityNotice, setAuthorityNotice] = useState("");
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
      if (result.status !== 200)
        throw responseError(
          result,
          "Не удалось загрузить оценку сопоставления",
        );
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
      `Файл ${index >= 0 ? index + 1 : 1}`
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
    setError("");
    setNotice("");
    setAuthorityNotice("");
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
          setError(
            "Данные группы или режима изменились. Обновите их вручную перед новой попыткой.",
          );
          void Promise.all([
            groupsQuery.refetch(),
            reviewQuery.refetch(),
          ]).catch(() => undefined);
          return;
        }
        throw responseError(response, "Режим сопоставления не изменён");
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
      setAuthorityNotice(
        "Возврат к автоматическому сопоставлению сохранён. Ожидается новая оценка.",
      );
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
        setError(
          errorText(
            reason,
            "Не удалось изменить режим сопоставления. Повтор использует тот же запрос.",
          ),
        );
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
    setAuthorityNotice("");
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
    setNotice("");
    setError("");
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
    setNotice("");
    setError("");
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
    setNotice("");
    setError("");
  }

  async function createPreview() {
    if (sameGroupInFlight || !intent || !groupId) return;
    const requestScope = scopeIdentity();
    const activeRequest = ++requestId.current;
    setPendingRequest({ id: activeRequest, groupId, scope: requestScope });
    setError("");
    setNotice("");
    try {
      const response = await previewIncomingGroupManualMatching(
        groupId,
        intent,
      );
      if (requestScope !== scopeIdentity()) return;
      if (response.status !== 200)
        throw responseError(response, "Предпросмотр не создан");
      setPreview({ intent: intentPayload, response: response.data });
      setConfirmIntent(undefined);
    } catch (reason) {
      if (requestScope === scopeIdentity())
        setError(errorText(reason, "Не удалось создать предпросмотр."));
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
    setError("");
    setNotice("");
    try {
      const response = await confirmIncomingGroupManualMatching(
        preserved.groupId,
        preserved.body,
      );
      if (requestScope !== scopeIdentity()) return;
      if (response.status !== 200) {
        if (response.status === 409) {
          setError(
            "Данные оценки или группы изменились. Черновик сохранён; обновите оценку вручную перед новым предпросмотром.",
          );
          return;
        }
        throw responseError(response, "Подтверждение не выполнено");
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
      setNotice(
        "Подтверждение сохранено. Не включённые в назначение файлы остаются во входящей группе.",
      );
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
        setError(
          errorText(
            reason,
            "Не удалось подтвердить назначение. Повтор использует тот же запрос.",
          ),
        );
    } finally {
      setPendingRequest((current) =>
        current?.id === activeRequest ? undefined : current,
      );
    }
  }

  async function reloadReview() {
    const requestScope = scopeIdentity();
    setError("");
    await reviewQuery.refetch();
    if (requestScope !== scopeIdentity()) return;
    setPreview(undefined);
    setConfirmIntent(undefined);
    setNotice(
      "Оценка обновлена. Черновик назначений сохранён для выбранного издания.",
    );
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
            Разбор входящих групп
          </h1>
          <p className="mt-1 text-sm text-stone-700">
            Выберите издание и явно назначьте файлы позициям. Черновик хранится
            только в этой странице.
          </p>
        </div>
        <AppButton
          onPress={() => {
            void groupsQuery.refetch();
          }}
        >
          Обновить список
        </AppButton>
      </div>

      {groupsQuery.isLoading ? (
        <p role="status">Загрузка входящих групп…</p>
      ) : null}
      {groupsQuery.isError ? (
        <p role="alert">
          {errorText(groupsQuery.error, "Не удалось загрузить группы.")}
        </p>
      ) : null}
      {groupsQuery.data && groups.length === 0 ? (
        <p className="rounded border border-stone-300 p-4">
          Входящих групп нет.
        </p>
      ) : null}

      {groups.length > 0 ? (
        <>
          <div className="space-y-5 min-[1400px]:max-h-[30dvh] min-[1400px]:shrink-0 min-[1400px]:overflow-y-auto">
            {groupsQuery.data ? (
              <details className="rounded border border-stone-300 p-3">
                <summary className="cursor-pointer font-semibold">
                  Объединить, разделить или переместить файлы групп
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
              Входящая группа
              <select
                className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
                value={groupId}
                onChange={(event) => changeGroup(event.target.value)}
              >
                {groups.map((item, index) => (
                  <option key={item.id} value={item.id}>
                    Группа {index + 1} · {item.members?.length ?? 0} файлов
                    {item.manual ? " · вручную сгруппирована" : ""}
                  </option>
                ))}
              </select>
            </label>

            {group && groupsQuery.data ? (
              <details className="rounded border border-stone-300 p-3">
                <summary className="cursor-pointer font-semibold">
                  Локальный релиз без связей с провайдерами
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
              <p role="status">Чтение сохранённой оценки…</p>
            ) : null}
            {reviewQuery.isError ? (
              <div role="alert" className="space-y-2">
                <p>
                  {errorText(
                    reviewQuery.error,
                    "Оценка сопоставления недоступна.",
                  )}
                </p>
                <AppButton
                  isDisabled={sameGroupInFlight}
                  onPress={() => {
                    void reviewQuery.refetch();
                  }}
                >
                  Повторить чтение
                </AppButton>
              </div>
            ) : null}
            {review && !review.evaluated ? (
              <p className="rounded border border-amber-500 bg-amber-50 p-3">
                Оценка ещё не выполнялась. Автоматическое сопоставление здесь не
                запускается.
              </p>
            ) : null}
            {review ? (
              <section
                className="rounded border border-stone-300 bg-white p-4"
                aria-labelledby="authority-title"
              >
                <h2 id="authority-title" className="font-semibold">
                  Текущий режим управления сопоставлением
                </h2>
                <p className="mt-1 text-sm">
                  Режим: <strong>{review.authority_mode}</strong> · ревизия:{" "}
                  <code>{review.authority_revision}</code>
                </p>
                <p className="mt-1 text-sm">
                  Сохранённая текущая оценка и её доказательства показаны
                  отдельно ниже. Удаление источников выполняется вне этого
                  экрана.
                </p>
                {authorityNotice ? (
                  <p
                    role="status"
                    className="mt-2 rounded border border-green-700 p-3"
                  >
                    {authorityNotice}
                  </p>
                ) : null}
                {review.authority_mode === "manual" ? (
                  <div className="mt-3">
                    <AppButton
                      isDisabled={sameGroupInFlight}
                      onPress={() => void performAuthorityAction()}
                    >
                      {currentAuthorityIntent?.action === "automatic"
                        ? "Повторить возврат к автоматическому сопоставлению"
                        : "Вернуть к автоматическому сопоставлению"}
                    </AppButton>
                  </div>
                ) : null}
                {error.includes("изменились") ? (
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
                    Обновить данные группы и режима
                  </AppButton>
                ) : null}
              </section>
            ) : null}
            {review?.evaluated && !review.proposal_available ? (
              <p className="rounded border border-amber-500 bg-amber-50 p-3">
                Сохраненные данные предложения недоступны или неполны. Ручной
                просмотр для этой оценки невозможен.
              </p>
            ) : null}

            {group && !(review?.evaluated && review.proposal_available) ? (
              <section
                className="grid gap-3 rounded border border-stone-300 bg-white p-3 lg:grid-cols-2"
                aria-label="Поиск и подготовка издания"
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
                Для этой оценки нет доступных макетов изданий.
              </p>
            ) : null}

            {review?.evaluated && review.proposal_available ? (
              <section
                className="rounded border border-stone-300 bg-white p-4"
                aria-label="Сводка входящей группы"
              >
                <h2 className="font-semibold">
                  Файлы группы и текущий черновик
                </h2>
                <div className="mt-2 overflow-x-auto">
                  <table className="w-full border-collapse text-left text-sm">
                    <thead>
                      <tr>
                        <th className="border-b p-2">
                          Уникальные файлы / пути
                        </th>
                        <th className="border-b p-2">
                          Черновик: назначено / всего
                        </th>
                        <th className="border-b p-2">Снимок источника</th>
                        <th className="border-b p-2">Снимок провайдера</th>
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
                          {
                            groupFiles.filter(
                              (file) =>
                                file.capture_identity || file.locations?.length,
                            ).length
                          }{" "}
                          из {groupFiles.length} файлов с сохранёнными данными
                        </td>
                        <td className="border-b p-2">
                          {review.candidates?.length
                            ? `${review.candidates.length} сохранённых кандидатов`
                            : "кандидаты отсутствуют"}
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <p className="mt-2 text-sm">
                  Уникальных файлов с сохранёнными ручными связями:{" "}
                  {review.current_linked_manual_media_source_ids?.length ?? 0}.
                  Черновик назначений не изменяет эти связи до подтверждения.
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
                  Поиск и файлы
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
                <h3 className="font-medium">Сохраненная оценка</h3>
                <p className="text-sm">
                  Ревизия: {review.evaluation_revision || "не указана"} ·
                  участников: {memberIds.length}
                </p>
                <label className="block text-sm font-medium">
                  Издание (выбор вручную)
                  <select
                    disabled={busy || authorityBusy || sameGroupInFlight}
                    className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
                    value={selectedReleaseId}
                    onChange={(event) => changeRelease(event.target.value)}
                  >
                    <option value="">Выберите издание…</option>
                    {releases.map((item) => (
                      <option
                        key={item.provider_release_id}
                        value={item.provider_release_id}
                      >
                        {layoutLabel(item, releases)}
                      </option>
                    ))}
                  </select>
                </label>
                {savedSelectedId && selectedReleaseId === savedSelectedId ? (
                  <p className="text-sm">
                    Это издание сохранено в оценке сервера.
                  </p>
                ) : null}
                <h3 className="font-medium">Сохранённые кандидаты</h3>
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
                                )?.title ?? "Издание вне сохранённого макета"}
                              </p>
                              <p>
                                Уверенность издания:{" "}
                                {Number.isFinite(candidate.release_confidence)
                                  ? candidate.release_confidence
                                  : "неизвестно"}{" "}
                                · позиции:{" "}
                                {position.displayed_number ||
                                  position.position + 1}{" "}
                                —{" "}
                                {position.assignment_confidence ?? "неизвестно"}
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
                                )?.title ?? "Издание вне сохранённого макета"}
                              </p>
                              <p>
                                Уверенность издания:{" "}
                                {Number.isFinite(candidate.release_confidence)
                                  ? candidate.release_confidence
                                  : "неизвестно"}
                              </p>
                            </li>,
                          ],
                    )}
                  </ul>
                ) : (
                  <p className="text-sm">Сохранённых рёбер-кандидатов нет.</p>
                )}
              </section>

              {release ? (
                <section
                  data-matching-positions
                  className="min-w-0 rounded border border-stone-300 bg-white p-3 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto"
                  aria-labelledby="assignment-board-title"
                >
                  <h2 id="assignment-board-title" className="font-semibold">
                    Позиции издания · {release.title}
                  </h2>
                  <p className="mt-1 text-sm text-stone-700">
                    Перетащите свободный файл на позицию или выберите назначение
                    в списке файла. Несколько файлов могут быть назначены одной
                    позиции.
                  </p>
                  <p className="mt-1 text-sm text-stone-700">
                    {release.tracklist_complete
                      ? "Список позиций полный"
                      : "Список позиций помечен как неполный"}{" "}
                    · {release.positions?.length ?? 0} позиций
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
                              {positionLabel(position)}
                            </button>
                            <p className="mt-1 text-stone-700">
                              {occupants.length
                                ? `Назначено файлов: ${occupants.length} · ${occupants.map((memberId) => fileLabel(memberId, memberIds.indexOf(memberId))).join(", ")}`
                                : "Нет назначенных файлов"}
                            </p>
                            <TrackTagOverrides
                              title={positionLabel(position)}
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
                  <h2 className="font-semibold">Позиции издания</h2>
                  <p className="mt-2 text-sm">
                    Выберите или подготовьте издание, чтобы показать его позиции
                    и начать ручное назначение.
                  </p>
                </section>
              )}

              <section
                data-matching-sources
                className="min-w-0 space-y-3 rounded border border-stone-300 bg-white p-3 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto"
                aria-labelledby="matching-sources-title"
              >
                <h2 id="matching-sources-title" className="font-semibold">
                  Файлы источников
                </h2>
                <p className="text-sm text-stone-700">
                  Перетащите свободный файл на позицию или назначьте его из
                  этого списка с клавиатуры.
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
                            ? "Свободен"
                            : assignedPosition
                              ? `Назначен: ${positionLabel(assignedPosition)}`
                              : "Назначение вне выбранного издания"}
                        </span>
                      </button>
                      <label className="mt-2 block text-sm font-medium">
                        Назначение
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
                          <option value="">
                            Не назначать — оставить во входящих
                          </option>
                          {(release?.positions ?? []).map((position) => (
                            <option
                              key={position.track_id}
                              value={position.track_id}
                            >
                              {positionLabel(position)}
                            </option>
                          ))}
                        </select>
                      </label>
                      {savedFacts?.raw_tags ? (
                        <Metadata
                          title="Сохранённые метаданные предложения"
                          tags={savedFacts.raw_tags}
                        />
                      ) : null}
                      {file ? (
                        <Metadata
                          title="Метаданные файла из входящей группы"
                          tags={file.tags}
                        />
                      ) : null}
                      {savedFacts?.diagnostics?.length ? (
                        <div className="mt-2 text-sm">
                          <p className="font-medium">Диагностика группы</p>
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
                  <p className="text-sm text-stone-600">В группе нет файлов.</p>
                ) : null}
              </section>

              <section
                data-matching-inspector
                className="min-w-0 rounded border border-stone-300 bg-white p-3 min-[1400px]:col-span-3 min-[1400px]:grid min-[1400px]:min-h-0 min-[1400px]:grid-cols-[minmax(0,1fr)_minmax(220px,auto)] min-[1400px]:gap-3"
                aria-labelledby="matching-inspector-title"
              >
                <div className="min-w-0 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto">
                  <h2 id="matching-inspector-title" className="font-semibold">
                    Осмотр:{" "}
                    {inspectedFileMemberId
                      ? fileLabel(
                          inspectedFileMemberId,
                          memberIds.indexOf(inspectedFileMemberId),
                        )
                      : "файл не выбран"}
                    {inspectedPosition
                      ? ` · ${positionLabel(inspectedPosition)}`
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
                  aria-label="Действия с назначением"
                >
                  {error ? (
                    <p
                      role="alert"
                      className="w-full rounded border border-red-600 p-2 text-sm"
                    >
                      {error}
                    </p>
                  ) : null}
                  {notice ? (
                    <p
                      role="status"
                      className="w-full rounded border border-green-700 p-2 text-sm"
                    >
                      {notice}
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
                      ? "Обработка…"
                      : "Предпросмотр всего назначения"}
                  </AppButton>
                  {currentPreview ? (
                    <AppButton
                      isDisabled={sameGroupInFlight}
                      onPress={() => void confirm()}
                    >
                      {currentRetryIntent
                        ? "Повторить подтверждение"
                        : "Явно подтвердить"}
                    </AppButton>
                  ) : null}
                  {error.includes("изменились") ? (
                    <AppButton
                      isDisabled={sameGroupInFlight}
                      onPress={() => void reloadReview()}
                    >
                      Обновить сохранённую оценку
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
                Предпросмотр полного назначения
              </h2>
              <p>
                Издание: {currentPreview.selected_release.title}. Назначено:{" "}
                {currentPreview.members?.filter((member) => member.assignment)
                  .length ?? 0}
                ; пропущено: {currentPreview.omitted_member_ids?.length ?? 0}.
              </p>
              <p className="text-sm">
                Контрольный digest: <code>{currentPreview.preview_digest}</code>
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
                  <h3 className="font-medium">
                    Ручные метаданные, которые будут применены
                  </h3>
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
                              ? `название «${override.title.value}»`
                              : undefined,
                            override.artists.enabled
                              ? `исполнители ${override.artists.values.length ? override.artists.values.map((artist) => `«${artist.name}»`).join(", ") : "(пустой список)"}`
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
                Подтверждено · локальное издание
              </h2>
              <p>
                Назначено файлов:{" "}
                {receipt.response.members?.filter((member) => member.assignment)
                  .length ?? 0}{" "}
                из {receipt.response.members?.length ?? 0}. Пропущено:{" "}
                {receipt.response.omitted_member_ids?.length ?? 0}.
              </p>
              <ul className="mt-2 list-inside list-disc text-sm">
                {(receipt.response.omitted_member_ids ?? []).map((memberId) => (
                  <li key={memberId}>
                    {receipt.omittedFileLabels[memberId] ?? "Файл"} остаётся во
                    входящих
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
