import {
  QueryClient,
  QueryClientProvider,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  confirmIncomingGroupManualMatching,
  getGetIncomingGroupMatchingQueryKey,
  getGetIncomingGroupsQueryKey,
  getIncomingGroupMatching,
  getIncomingGroups,
  previewIncomingGroupManualMatching,
} from "../../api/generated/client";
import type {
  MatchingManualAssignmentBody,
  MatchingManualConfirmBody,
  MatchingManualPreview,
  MatchingReview,
  MatchingReviewRelease,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { ManualReleasePicker } from "./ManualReleasePicker";

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
type PreviewState = { intent: string; response: MatchingManualPreview };
type ConfirmIntent = {
  groupId: string;
  generation: number;
  intent: string;
  body: MatchingManualConfirmBody;
};

function errorText(reason: unknown, fallback: string) {
  return reason instanceof Error && reason.message ? reason.message : fallback;
}

function responseError(
  response: { data: { detail?: string }; status: number },
  fallback: string,
) {
  return new Error(
    response.data.detail || `${fallback} (HTTP ${response.status}).`,
  );
}

function parsedProposal(review: MatchingReview | undefined): ReviewProposal {
  if (!review?.proposal || typeof review.proposal !== "object") return {};
  return review.proposal as ReviewProposal;
}

function layoutLabel(release: MatchingReviewRelease) {
  return `${release.title} · ${release.release_id}${release.tracklist_complete ? "" : " · неполный список"}`;
}

function positionLabel(
  position: NonNullable<MatchingReviewRelease["positions"]>[number],
) {
  const number = position.displayed_number || `${position.position + 1}`;
  return `Носитель ${position.medium + 1} · ${number} · ${position.title} (${position.recording_id})`;
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
  const [preparedReleaseMBID, setPreparedReleaseMBID] = useState("");
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [preview, setPreview] = useState<PreviewState>();
  const [confirmIntent, setConfirmIntent] = useState<ConfirmIntent>();
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [receipt, setReceipt] = useState<{
    groupId: string;
    response: MatchingManualPreview;
  }>();
  const [pendingRequest, setPendingRequest] = useState<{
    id: number;
    scope: string;
  }>();
  const generation = useRef(0);
  const scopeRef = useRef({ groupId: "", releaseId: "", generation: 0 });
  const evaluationRevisionRef = useRef("");
  const requestId = useRef(0);
  const seededGroup = useRef("");

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
  const files = groupsQuery.data?.files ?? [];
  const groupFiles = useMemo(() => {
    const ids = new Set(group?.members ?? []);
    return files.filter((file) => ids.has(file.variant_id));
  }, [files, group]);
  const memberIds = review?.members ?? group?.members ?? [];
  const proposalFiles = new Map(
    (proposal.files ?? []).map((file) => [file.file_member_id, file]),
  );
  const intentPayload = useMemo(() => {
    if (!review?.evaluation_revision || !release) return "";
    const assignments: MatchingManualAssignmentBody[] = memberIds.flatMap(
      (memberId) => {
        const trackId = assignmentsByMember[memberId];
        const position = release.positions?.find(
          (item) => item.track_id === trackId,
        );
        return trackId && position
          ? [
              {
                file_member_id: memberId,
                provider_track_id: position.track_id,
                medium: position.medium + 1,
                position: position.position + 1,
              },
            ]
          : [];
      },
    );
    return JSON.stringify({
      expected_evaluation_revision: review.evaluation_revision,
      provider_release_id: release.provider_release_id,
      assignments,
    });
  }, [assignmentsByMember, memberIds, release, review?.evaluation_revision]);
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
  }

  function changeRelease(nextReleaseId: string) {
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

  async function createPreview() {
    if (!intent || !groupId) return;
    const requestScope = scopeIdentity();
    const activeRequest = ++requestId.current;
    setPendingRequest({ id: activeRequest, scope: requestScope });
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
      if (requestScope === scopeIdentity())
        setPendingRequest((current) =>
          current?.id === activeRequest ? undefined : current,
        );
    }
  }

  async function confirm() {
    if (!intent || !groupId || !currentPreview) return;
    const preserved = currentRetryIntent ?? {
      groupId,
      generation: scopeRef.current.generation,
      intent: intentPayload,
      body: {
        ...intent,
        preview_digest: currentPreview.preview_digest,
        request_id: crypto.randomUUID(),
      },
    };
    const requestScope = scopeIdentity();
    const activeRequest = ++requestId.current;
    setConfirmIntent(preserved);
    setPendingRequest({ id: activeRequest, scope: requestScope });
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
      setReceipt({ groupId: preserved.groupId, response: response.data });
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
      if (requestScope === scopeIdentity())
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

  return (
    <section
      className="mx-auto max-w-7xl space-y-5"
      aria-labelledby="incoming-title"
    >
      <div className="flex flex-wrap items-end justify-between gap-3">
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
          <label className="block max-w-2xl text-sm font-medium">
            Входящая группа
            <select
              className="mt-1 block w-full rounded border border-stone-400 bg-white p-2"
              value={groupId}
              onChange={(event) => changeGroup(event.target.value)}
            >
              {groups.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.id} · {item.members?.length ?? 0} файлов
                  {item.manual ? " · вручную сгруппирована" : ""}
                </option>
              ))}
            </select>
          </label>

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
          {review?.evaluated && !review.proposal_available ? (
            <p className="rounded border border-amber-500 bg-amber-50 p-3">
              Сохраненные данные предложения недоступны или неполны. Ручной
              просмотр для этой оценки невозможен.
            </p>
          ) : null}

          {group ? (
            <ManualReleasePicker
              groupId={groupId}
              groupRevision={group.revision}
              evaluationRevision={review?.evaluation_revision || undefined}
              disabled={!review}
              onPrepared={setPreparedReleaseMBID}
            />
          ) : null}
          {review?.evaluated &&
          review.proposal_available &&
          review.releases?.length === 0 ? (
            <p className="rounded border border-stone-300 p-3">
              Для этой оценки нет доступных макетов изданий.
            </p>
          ) : null}

          {review?.evaluated && review.proposal_available ? (
            <>
              <section
                className="rounded border border-stone-300 bg-white p-4"
                aria-labelledby="evaluation-title"
              >
                <h2 id="evaluation-title" className="font-semibold">
                  Сохраненная оценка
                </h2>
                <p className="mt-1 text-sm">
                  Ревизия:{" "}
                  <code>{review.evaluation_revision || "не указана"}</code>
                </p>
                <p className="text-sm">
                  Участников группы: {memberIds.length}. Ребра-кандидаты не
                  считаются назначениями и не заполнены автоматически.
                </p>
                <label className="mt-3 block max-w-2xl text-sm font-medium">
                  Издание (выбор вручную)
                  <select
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
                        {layoutLabel(item)}
                      </option>
                    ))}
                  </select>
                </label>
                {savedSelectedId && selectedReleaseId === savedSelectedId ? (
                  <p className="mt-2 text-sm">
                    Это издание сохранено в оценке сервера.
                  </p>
                ) : null}
                {review.candidates?.length ? (
                  <div className="mt-3 overflow-x-auto">
                    <table className="w-full border-collapse text-left text-sm">
                      <caption className="mb-2 text-left font-medium">
                        Уверенность издания отдельно от уверенности назначений
                      </caption>
                      <thead>
                        <tr>
                          <th className="border-b p-2">Файл</th>
                          <th className="border-b p-2">Издание-кандидат</th>
                          <th className="border-b p-2">Уверенность издания</th>
                          <th className="border-b p-2">Уверенность позиции</th>
                        </tr>
                      </thead>
                      <tbody>
                        {review.candidates.flatMap((candidate) =>
                          candidate.positions?.length
                            ? candidate.positions.map((position) => (
                                <tr
                                  key={`${candidate.file_member_id}-${position.track_id}`}
                                >
                                  <td className="border-b p-2 font-mono">
                                    {candidate.file_member_id}
                                  </td>
                                  <td className="border-b p-2">
                                    {candidate.release_id}
                                  </td>
                                  <td className="border-b p-2">
                                    {Number.isFinite(
                                      candidate.release_confidence,
                                    )
                                      ? candidate.release_confidence
                                      : "неизвестно"}
                                  </td>
                                  <td className="border-b p-2">
                                    {position.assignment_confidence == null
                                      ? "неизвестно"
                                      : position.assignment_confidence}
                                  </td>
                                </tr>
                              ))
                            : [
                                <tr
                                  key={`${candidate.file_member_id}-${candidate.release_id}`}
                                >
                                  <td className="border-b p-2 font-mono">
                                    {candidate.file_member_id}
                                  </td>
                                  <td className="border-b p-2">
                                    {candidate.release_id}
                                  </td>
                                  <td className="border-b p-2">
                                    {Number.isFinite(
                                      candidate.release_confidence,
                                    )
                                      ? candidate.release_confidence
                                      : "неизвестно"}
                                  </td>
                                  <td className="border-b p-2">неизвестно</td>
                                </tr>,
                              ],
                        )}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <p className="mt-3 text-sm">
                    Сохраненных ребер-кандидатов нет.
                  </p>
                )}
              </section>

              {release ? (
                <section
                  className="rounded border border-stone-300 bg-white p-4"
                  aria-labelledby="layout-title"
                >
                  <h2 id="layout-title" className="font-semibold">
                    Макет издания: {release.title}
                  </h2>
                  <p className="text-sm">
                    {release.tracklist_complete
                      ? "Список позиций полный"
                      : "Список позиций помечен как неполный"}{" "}
                    · {release.positions?.length ?? 0} позиций
                  </p>
                  <ol className="mt-3 grid gap-2 md:grid-cols-2">
                    {[...(release.positions ?? [])]
                      .sort(
                        (a, b) =>
                          a.medium - b.medium ||
                          a.position - b.position ||
                          a.track_id.localeCompare(b.track_id),
                      )
                      .map((position) => (
                        <li
                          key={position.track_id}
                          className="rounded border border-stone-200 p-2 text-sm"
                        >
                          {positionLabel(position)}
                        </li>
                      ))}
                  </ol>
                </section>
              ) : null}

              <section className="space-y-3" aria-labelledby="members-title">
                <h2 id="members-title" className="text-lg font-semibold">
                  Файлы и явные назначения
                </h2>
                {memberIds.map((memberId, index) => {
                  const file = groupFiles.find(
                    (item) => item.variant_id === memberId,
                  );
                  const savedFacts = proposalFiles.get(memberId);
                  return (
                    <article
                      key={memberId}
                      className="rounded border border-stone-300 bg-white p-3"
                    >
                      <div className="flex flex-wrap items-start justify-between gap-3">
                        <div>
                          <h3 className="font-medium">
                            Участник {index + 1} · <code>{memberId}</code>
                          </h3>
                          {file?.locations?.map((location) => (
                            <p
                              key={location.location_id}
                              className="text-sm text-stone-700"
                            >
                              {location.relative_path}
                            </p>
                          ))}
                          {!file?.locations?.length ? (
                            <p className="text-sm text-stone-600">
                              Путь файла не сохранён.
                            </p>
                          ) : null}
                        </div>
                        <label className="min-w-64 flex-1 text-sm font-medium">
                          Назначение
                          <select
                            disabled={!release}
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
                      </div>
                      {savedFacts?.raw_tags ? (
                        <Metadata
                          title="Сохраненные метаданные предложения"
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
              </section>

              {error ? (
                <p role="alert" className="rounded border border-red-600 p-3">
                  {error}
                </p>
              ) : null}
              {notice ? (
                <p
                  role="status"
                  className="rounded border border-green-700 p-3"
                >
                  {notice}
                </p>
              ) : null}
              <div className="flex flex-wrap gap-2">
                <AppButton
                  isDisabled={busy || !release || !review.evaluation_revision}
                  onPress={() => {
                    void createPreview();
                  }}
                >
                  {busy && !currentRetryIntent
                    ? "Обработка…"
                    : "Предпросмотр всего назначения"}
                </AppButton>
                {currentPreview ? (
                  <AppButton
                    isDisabled={busy}
                    onPress={() => {
                      void confirm();
                    }}
                  >
                    {currentRetryIntent
                      ? "Повторить подтверждение"
                      : "Явно подтвердить"}
                  </AppButton>
                ) : null}
                {error.includes("изменились") ? (
                  <AppButton
                    isDisabled={busy}
                    onPress={() => {
                      void reloadReview();
                    }}
                  >
                    Обновить сохранённую оценку
                  </AppButton>
                ) : null}
              </div>
              {currentPreview ? (
                <section
                  className="rounded border border-sky-700 bg-white p-4"
                  aria-labelledby="preview-title"
                >
                  <h2 id="preview-title" className="font-semibold">
                    Предпросмотр полного назначения
                  </h2>
                  <p>
                    Издание: {currentPreview.selected_release.title}. Назначено:{" "}
                    {currentPreview.members?.filter(
                      (member) => member.assignment,
                    ).length ?? 0}
                    ; пропущено:{" "}
                    {currentPreview.omitted_member_ids?.length ?? 0}.
                  </p>
                  <p className="text-sm">
                    Контрольный digest:{" "}
                    <code>{currentPreview.preview_digest}</code>
                  </p>
                </section>
              ) : null}
            </>
          ) : null}

          {receipt?.groupId === groupId ? (
            <section
              className="rounded border border-green-700 bg-white p-4"
              aria-labelledby="receipt-title"
            >
              <h2 id="receipt-title" className="font-semibold">
                Подтверждено · локальное издание{" "}
                {receipt.response.local_release_id || "не указано"}
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
                    <code>{memberId}</code> остаётся во входящих
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
