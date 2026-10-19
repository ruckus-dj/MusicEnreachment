import { useCallback, useEffect, useRef, useState } from "react";
import { subscribeToOperation } from "../../api/client/operations";
import {
  applyLibraryManualLyrics,
  getLibraryPositionMetadata,
  getLibraryPublication,
  getLibraryRelease,
  patchLibraryPositionMetadata,
} from "../../api/generated/client";
import type {
  LibraryMetadataResponse,
  LibraryPublicationResponse,
  LibraryReleaseDetailResponse,
} from "../../api/generated/client.schemas";
import { FirstPublicationPreview } from "./FirstPublicationPreview";

type PositionData = {
  metadata?: LibraryMetadataResponse;
  publication?: LibraryPublicationResponse;
  lyrics: string;
  draftDirty?: boolean;
  error?: string;
  readError?: string;
  saving?: boolean;
  applying?: boolean;
  applyOperationID?: string;
  firstPublicationOperationID?: string;
  notice?: string;
};

function errorMessage(data: { detail?: string }, status: number) {
  return data.detail || `Запрос не выполнен (HTTP ${status}).`;
}

export function LibraryReleaseScreen({ releaseId }: { releaseId: string }) {
  const [release, setRelease] = useState<LibraryReleaseDetailResponse>();
  const [loadedReleaseID, setLoadedReleaseID] = useState("");
  const [positions, setPositions] = useState<Record<string, PositionData>>({});
  const [error, setError] = useState("");
  const mounted = useRef(false);
  const currentReleaseID = useRef(releaseId);
  currentReleaseID.current = releaseId;
  const scope = useRef({ releaseId, generation: 0 });
  if (scope.current.releaseId !== releaseId) {
    scope.current = { releaseId, generation: scope.current.generation + 1 };
  }
  const monitors = useRef(
    new Map<string, { stop: () => void; positions: Set<string> }>(),
  );
  const finishedOperations = useRef(new Set<string>());
  const positionReadGenerations = useRef(new Map<string, number>());
  const lyricsEditGenerations = useRef(new Map<string, number>());

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const refreshPosition = useCallback(
    async (positionId: string) => {
      const requestScope = scope.current;
      const readGeneration =
        (positionReadGenerations.current.get(positionId) ?? 0) + 1;
      positionReadGenerations.current.set(positionId, readGeneration);
      const [metadata, publication] = await Promise.all([
        getLibraryPositionMetadata(releaseId, positionId, {
          cache: "no-store",
        }),
        getLibraryPublication(releaseId, positionId, { cache: "no-store" }),
      ]);
      if (
        !mounted.current ||
        scope.current !== requestScope ||
        positionReadGenerations.current.get(positionId) !== readGeneration
      )
        return;
      if (metadata.status !== 200)
        throw new Error(errorMessage(metadata.data, metadata.status));
      if (publication.status !== 200)
        throw new Error(errorMessage(publication.data, publication.status));
      const field = metadata.data.envelope.desired.lyrics;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          metadata: metadata.data,
          publication: publication.data,
          lyrics: current[positionId]?.draftDirty
            ? (current[positionId]?.lyrics ?? "")
            : field?.state === "value" && typeof field.value === "string"
              ? field.value
              : "",
          error: undefined,
          readError: undefined,
        },
      }));
    },
    [releaseId],
  );

  useEffect(() => {
    let active = true;
    setRelease(undefined);
    setLoadedReleaseID("");
    setPositions({});
    setError("");
    void getLibraryRelease(releaseId, { cache: "no-store" })
      .then(async (response) => {
        if (response.status !== 200)
          throw new Error(errorMessage(response.data, response.status));
        if (!active) return;
        setRelease(response.data);
        setLoadedReleaseID(releaseId);
        await Promise.all(
          (response.data.tracks ?? []).map((track) =>
            refreshPosition(track.id).catch((reason: unknown) => {
              if (active && scope.current.releaseId === releaseId)
                setPositions((current) => ({
                  ...current,
                  [track.id]: {
                    ...current[track.id],
                    lyrics: current[track.id]?.lyrics ?? "",
                    readError:
                      reason instanceof Error
                        ? reason.message
                        : "Не удалось загрузить данные публикации.",
                  },
                }));
            }),
          ),
        );
      })
      .catch((reason: unknown) => {
        if (active && scope.current.releaseId === releaseId)
          setError(
            reason instanceof Error
              ? reason.message
              : "Не удалось загрузить релиз.",
          );
      });
    return () => {
      active = false;
      for (const monitor of monitors.current.values()) monitor.stop();
      monitors.current.clear();
      finishedOperations.current.clear();
      positionReadGenerations.current.clear();
    };
  }, [releaseId, refreshPosition]);

  // Keep one wake-only SSE subscription per operation, including attempts that
  // were already active when the release screen was opened.
  useEffect(() => {
    const requestScope = scope.current;
    const byOperation = new Map<string, Set<string>>();
    for (const [positionId, position] of Object.entries(
      loadedReleaseID === releaseId ? positions : {},
    )) {
      const operationID =
        position.applyOperationID ??
        position.firstPublicationOperationID ??
        position.publication?.active_attempt?.id;
      if (!operationID || finishedOperations.current.has(operationID)) continue;
      const associated = byOperation.get(operationID) ?? new Set<string>();
      associated.add(positionId);
      byOperation.set(operationID, associated);
    }
    for (const [operationID, monitor] of monitors.current) {
      const nextPositions = byOperation.get(operationID);
      if (!nextPositions) {
        monitor.stop();
        monitors.current.delete(operationID);
      } else {
        monitor.positions = nextPositions;
      }
    }
    for (const [operationID, positionIDs] of byOperation) {
      if (monitors.current.has(operationID)) continue;
      const entry = { stop: () => {}, positions: positionIDs };
      const stop = subscribeToOperation(
        operationID,
        (snapshot) => {
          if (
            !mounted.current ||
            currentReleaseID.current !== releaseId ||
            scope.current !== requestScope
          )
            return;
          for (const positionID of entry.positions) {
            void refreshPosition(positionID)
              .then(() => {
                if (
                  !mounted.current ||
                  currentReleaseID.current !== releaseId ||
                  scope.current !== requestScope
                )
                  return;
                if (
                  ["succeeded", "failed", "cancelled"].includes(snapshot.state)
                ) {
                  finishedOperations.current.add(operationID);
                  setPositions((current) => {
                    if (
                      !mounted.current ||
                      currentReleaseID.current !== releaseId ||
                      scope.current !== requestScope
                    )
                      return current;
                    return {
                      ...current,
                      [positionID]: {
                        ...current[positionID],
                        applying: false,
                        applyOperationID: undefined,
                        firstPublicationOperationID:
                          current[positionID]?.firstPublicationOperationID ===
                          operationID
                            ? undefined
                            : current[positionID]?.firstPublicationOperationID,
                        notice:
                          snapshot.state === "succeeded"
                            ? "Операция завершена. Сверьте опубликованные данные и квитанцию ниже."
                            : `Операция завершилась со статусом: ${snapshot.state}.`,
                      },
                    };
                  });
                }
              })
              .catch((reason: unknown) => {
                if (
                  !mounted.current ||
                  currentReleaseID.current !== releaseId ||
                  scope.current !== requestScope
                )
                  return;
                setPositions((current) => {
                  if (
                    !mounted.current ||
                    currentReleaseID.current !== releaseId ||
                    scope.current !== requestScope
                  )
                    return current;
                  return {
                    ...current,
                    [positionID]: {
                      ...current[positionID],
                      applying: false,
                      readError:
                        reason instanceof Error
                          ? reason.message
                          : "Не удалось обновить публикацию.",
                    },
                  };
                });
              });
          }
        },
        () => {
          // The event stream is only a wake-up/error channel. Re-read both views;
          // on reconnect, its subsequent snapshot callback does the same.
          for (const positionID of entry.positions) {
            void refreshPosition(positionID).catch((reason: unknown) => {
              if (
                mounted.current &&
                currentReleaseID.current === releaseId &&
                scope.current === requestScope
              ) {
                setPositions((current) => {
                  if (
                    !mounted.current ||
                    currentReleaseID.current !== releaseId ||
                    scope.current !== requestScope
                  )
                    return current;
                  return {
                    ...current,
                    [positionID]: {
                      ...current[positionID],
                      applying: false,
                      readError:
                        reason instanceof Error
                          ? reason.message
                          : "Не удалось обновить публикацию.",
                    },
                  };
                });
              }
            });
          }
        },
      );
      entry.stop = stop;
      monitors.current.set(operationID, entry);
    }
  }, [positions, refreshPosition, releaseId, loadedReleaseID]);

  async function saveLyrics(positionId: string, explicitEmpty = false) {
    const position = positions[positionId];
    if (!position?.metadata || position.saving) return;
    const requestScope = scope.current;
    const submittedEditGeneration =
      lyricsEditGenerations.current.get(positionId) ?? 0;
    setPositions((current) => ({
      ...current,
      [positionId]: {
        ...current[positionId],
        saving: true,
        error: undefined,
        notice: undefined,
      },
    }));
    try {
      const field = explicitEmpty
        ? {
            authority: "manual",
            provenance: { kind: "manual" },
            state: "explicit_empty",
          }
        : {
            authority: "manual",
            provenance: { kind: "manual" },
            state: "value",
            value: position.lyrics,
          };
      const response = await patchLibraryPositionMetadata(
        releaseId,
        positionId,
        {
          expected_revision: position.metadata.revision,
          changes: { lyrics: field },
        },
      );
      if (response.status !== 200)
        throw new Error(errorMessage(response.data, response.status));
      if (!mounted.current || scope.current !== requestScope) return;
      // Any GET begun before this successful write is now stale, even if it
      // finishes after the PATCH response and would otherwise roll back state.
      positionReadGenerations.current.set(
        positionId,
        (positionReadGenerations.current.get(positionId) ?? 0) + 1,
      );
      const savedField = response.data.envelope.desired.lyrics;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          metadata: response.data,
          ...(lyricsEditGenerations.current.get(positionId) ===
          submittedEditGeneration
            ? {
                lyrics:
                  savedField?.state === "value" &&
                  typeof savedField.value === "string"
                    ? savedField.value
                    : "",
                draftDirty: false,
              }
            : { draftDirty: true }),
        },
      }));
      await refreshPosition(positionId);
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          notice:
            "Текст сохранён. Примените его отдельно к текущей публикации.",
        },
      }));
    } catch (reason) {
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          error:
            reason instanceof Error
              ? reason.message
              : "Не удалось сохранить текст.",
        },
      }));
    } finally {
      if (mounted.current && scope.current === requestScope)
        setPositions((current) => ({
          ...current,
          [positionId]: { ...current[positionId], saving: false },
        }));
    }
  }

  async function applyLyrics(positionId: string) {
    const position = positions[positionId];
    let publication = position?.publication;
    if (
      !publication?.can_apply_manual_lyrics ||
      !publication.current_epoch ||
      !publication.current_generation ||
      position.applying ||
      position.applyOperationID
    )
      return;
    const requestScope = scope.current;
    setPositions((current) => ({
      ...current,
      [positionId]: {
        ...current[positionId],
        applying: true,
        error: undefined,
        notice: undefined,
      },
    }));
    try {
      // Read fresh revisions immediately before admission; a background update
      // may have advanced metadata since the screen was first loaded.
      await refreshPosition(positionId);
      if (!mounted.current || scope.current !== requestScope) return;
      const fresh = await Promise.all([
        getLibraryPositionMetadata(releaseId, positionId, {
          cache: "no-store",
        }),
        getLibraryPublication(releaseId, positionId, { cache: "no-store" }),
      ]);
      if (fresh[0].status !== 200)
        throw new Error(errorMessage(fresh[0].data, fresh[0].status));
      if (fresh[1].status !== 200)
        throw new Error(errorMessage(fresh[1].data, fresh[1].status));
      publication = fresh[1].data;
      const freshField = fresh[0].data.envelope.desired.lyrics;
      if (
        freshField?.authority !== "manual" ||
        !["value", "explicit_empty"].includes(freshField.state) ||
        !publication.can_apply_manual_lyrics ||
        !publication.current_epoch ||
        !publication.current_generation ||
        publication.active_attempt
      ) {
        throw new Error(
          "Состояние изменилось или текст не готов к применению. Обновите данные перед повторной попыткой.",
        );
      }
      if (!mounted.current || scope.current !== requestScope) return;
      const response = await applyLibraryManualLyrics(releaseId, positionId, {
        expected_epoch: publication.current_epoch,
        expected_generation: publication.current_generation,
        expected_metadata_revision: fresh[0].data.revision,
      });
      if (response.status !== 202)
        throw new Error(errorMessage(response.data, response.status));
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          notice: "Применение запущено. Ожидание обновления операции…",
          applyOperationID: response.data.id,
        },
      }));
    } catch (reason) {
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          applying: false,
          error:
            reason instanceof Error
              ? reason.message
              : "Не удалось применить текст.",
          readError:
            reason instanceof Error
              ? reason.message
              : "Не удалось проверить публикацию.",
        },
      }));
    }
  }

  if (error)
    return (
      <section>
        <h1>Медиатека</h1>
        <p role="alert">{error}</p>
        <button
          type="button"
          onClick={() => {
            window.location.hash = "/incoming-groups";
          }}
        >
          К входящим
        </button>
      </section>
    );
  if (!release || loadedReleaseID !== releaseId)
    return <p role="status">Загрузка релиза…</p>;
  return (
    <section className="mx-auto max-w-5xl space-y-6">
      <button
        type="button"
        className="underline"
        onClick={() => {
          window.location.hash = "/incoming-groups";
        }}
      >
        ← К входящим
      </button>
      <header>
        <h1 className="text-2xl font-semibold">{release.title}</h1>
        <p>
          {release.artists
            ?.map((artist) => artist.credit_name || artist.artist_name)
            .join(", ")}
        </p>
      </header>
      {(release.tracks ?? []).map((track) => {
        const renderScope = scope.current;
        const position = positions[track.id];
        const publication = position?.publication;
        const lyricsField = position?.metadata?.envelope.desired.lyrics;
        const hasManualLyrics =
          lyricsField?.authority === "manual" &&
          ["value", "explicit_empty"].includes(lyricsField.state);

        function isPositionScopeCurrent() {
          return (
            mounted.current &&
            currentReleaseID.current === releaseId &&
            scope.current === renderScope
          );
        }

        function refreshReadState() {
          if (!isPositionScopeCurrent()) return;
          void refreshPosition(track.id).catch((reason: unknown) => {
            if (!isPositionScopeCurrent()) return;
            setPositions((current) => {
              if (!isPositionScopeCurrent()) return current;
              return {
                ...current,
                [track.id]: {
                  ...current[track.id],
                  readError:
                    reason instanceof Error
                      ? reason.message
                      : "Не удалось обновить публикацию.",
                },
              };
            });
          });
        }
        return (
          <article
            key={track.id}
            className="space-y-3 rounded border border-stone-300 p-4"
          >
            <h2 className="font-semibold">
              {track.displayed_number}. {track.title}
            </h2>
            <label className="block">
              Сохранённый текст песни
              <textarea
                className="mt-1 block min-h-40 w-full rounded border p-2"
                value={position?.lyrics ?? ""}
                onChange={(event) => {
                  lyricsEditGenerations.current.set(
                    track.id,
                    (lyricsEditGenerations.current.get(track.id) ?? 0) + 1,
                  );
                  setPositions((current) => ({
                    ...current,
                    [track.id]: {
                      ...current[track.id],
                      lyrics: event.target.value,
                      draftDirty: true,
                    },
                  }));
                }}
              />
            </label>
            <div className="flex flex-wrap gap-2">
              <button
                type="button"
                disabled={
                  !position?.metadata ||
                  position.saving ||
                  Boolean(position.readError)
                }
                onClick={() => void saveLyrics(track.id)}
              >
                Сохранить текст
              </button>
              <button
                type="button"
                disabled={
                  !position?.metadata ||
                  position.saving ||
                  Boolean(position.readError)
                }
                onClick={() => void saveLyrics(track.id, true)}
              >
                Сохранить как явно пустой
              </button>
              <button
                type="button"
                disabled={
                  !publication?.can_apply_manual_lyrics ||
                  !hasManualLyrics ||
                  Boolean(position?.readError) ||
                  Boolean(position?.applyOperationID) ||
                  Boolean(publication.active_attempt) ||
                  position?.applying
                }
                onClick={() => void applyLyrics(track.id)}
              >
                Применить сохранённый текст
              </button>
            </div>
            <p>
              Публикация: {publication?.current_path ?? "ещё не опубликована"}
              {publication?.current_generation
                ? ` · поколение ${publication.current_generation}`
                : ""}
            </p>
            {!publication?.current_path &&
              !publication?.current_generation &&
              !publication?.active_attempt &&
              !position?.firstPublicationOperationID && (
                <FirstPublicationPreview
                  releaseId={releaseId}
                  positionId={track.id}
                  onAdmitted={(operationID) => {
                    if (!isPositionScopeCurrent()) return;
                    setPositions((current) => {
                      if (!isPositionScopeCurrent()) return current;
                      return {
                        ...current,
                        [track.id]: {
                          ...current[track.id],
                          firstPublicationOperationID: operationID,
                        },
                      };
                    });
                    refreshReadState();
                  }}
                  onRefresh={refreshReadState}
                />
              )}
            <p>
              {publication?.active_attempt
                ? `Выполняется операция: ${publication.active_attempt.stage} (${publication.active_attempt.state})`
                : publication?.manual_lyrics?.pending
                  ? "Сохранённый текст ожидает применения."
                  : "Нет ожидающего применения текста."}
            </p>
            <p>
              Текущая публикация:{" "}
              {publication?.current_metadata?.tags
                ? "теги загружены"
                : "метаданные не доступны"}
              ; аудио{" "}
              {publication?.current_audio
                ? `${publication.current_audio.codec}/${publication.current_audio.container}`
                : "не опубликовано"}
              ; обложка{" "}
              {publication?.current_assets?.cover.state ?? "неизвестно"}; текст{" "}
              {publication?.current_assets?.lyrics.state ?? "неизвестно"}.
            </p>
            {publication?.manual_lyrics_receipt && (
              <p>
                Квитанция: текст{" "}
                {publication.manual_lyrics_receipt.desired_state} подтверждён в
                поколении{" "}
                {publication.manual_lyrics_receipt.confirmed_generation},
                опубликованное поколение{" "}
                {publication.manual_lyrics_receipt.published_generation}
                {publication.manual_lyrics_receipt.warning_code
                  ? ` · предупреждение: ${publication.manual_lyrics_receipt.warning_code}`
                  : ""}
                .
              </p>
            )}
            {position?.notice && <p role="status">{position.notice}</p>}
            {position?.error && <p role="alert">{position.error}</p>}
            {position?.readError && (
              <div role="alert">
                {position.readError}{" "}
                <button type="button" onClick={refreshReadState}>
                  Обновить данные
                </button>
              </div>
            )}
          </article>
        );
      })}
    </section>
  );
}
