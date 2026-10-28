import { useCallback, useEffect, useRef, useState } from "react";
import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";
import { subscribeToOperation } from "../../api/client/operations";
import {
  applyLibraryManualLyrics,
  getLibraryPositionMetadata,
  getLibraryPublication,
  getLibraryRelease,
  patchLibraryPositionMetadata,
  refreshLibraryReleaseMusicbrainz,
  retryPublicationSourceCleanup,
  setLibraryPositionPrimaryArtist,
  setLibraryReleasePrimaryArtist,
} from "../../api/generated/client";
import type {
  LibraryMetadataResponse,
  LibraryPublicationResponse,
  LibraryReleaseDetailResponse,
  OperationResponse,
} from "../../api/generated/client.schemas";
import "./library.css";
import { FirstPublicationPreview } from "./FirstPublicationPreview";
import { ReleaseCoverEditor } from "./newReleaseCoverEditor";

const initialLyricsSourceLabels: Record<string, string> = {
  none: "источник не выбран",
  source: "источник медиатеки",
  lrclib: "LRCLIB",
};

const initialLyricsOutcomeLabels: Record<string, string> = {
  not_requested: "текст не запрашивался",
  exact_round_trip: "текст включён и проверен",
  rebuilt_without_lyrics: "публикация пересоздана без текста",
};

const initialLyricsWarningLabels: Record<string, string> = {
  lyrics_source_selection_required: "необходимо выбрать источник текста",
  lyrics_settings_unavailable: "настройки поиска текста недоступны",
  lyrics_lookup_metadata_incomplete:
    "недостаточно метаданных для поиска текста",
  lyrics_provider_unavailable: "провайдер текста недоступен",
  lyrics_lookup_failed: "не удалось найти текст",
  lyrics_lookup_invalid_synced_text: "найденный текст не прошёл проверку",
  readback_mismatch: "текст не прошёл проверку после записи",
};

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
  cleanupRetryOperationIDs?: Record<string, string>;
  cleanupRetryPendingIDs?: string[];
  notice?: string;
  primaryArtistSaving?: boolean;
  writePending?: boolean;
};

function errorMessage(data: { detail?: string }, status: number) {
  return apiResponseError(
    { status, data },
    `Запрос не выполнен (HTTP ${status}).`,
  ).message;
}

function isTerminalOperation(state: string) {
  return ["succeeded", "failed", "cancelled"].includes(state);
}

export function LibraryReleaseScreen({
  releaseId,
  positionId,
}: {
  releaseId: string;
  positionId?: string;
}) {
  const scope = useRef({ releaseId, generation: 0 });
  if (scope.current.releaseId !== releaseId) {
    scope.current = { releaseId, generation: scope.current.generation + 1 };
  }
  const [release, setRelease] = useState<LibraryReleaseDetailResponse>();
  const [loadedReleaseID, setLoadedReleaseID] = useState("");
  const [positions, setPositions] = useState<Record<string, PositionData>>({});
  const [error, setError] = useState("");
  const [musicBrainzRefresh, setMusicBrainzRefresh] = useState<{
    scope: typeof scope.current;
    operationID?: string;
    pending: boolean;
    active: boolean;
    notice?: string;
    error?: string;
  }>({ scope: scope.current, pending: false, active: false });
  const musicBrainzRefreshPending = useRef<
    { scope: typeof scope.current; token: number } | undefined
  >(undefined);
  const musicBrainzRefreshToken = useRef(0);
  const [releasePrimaryArtist, setReleasePrimaryArtist] = useState({
    scope: scope.current,
    token: 0,
    saving: false,
    error: "",
  });
  const releasePrimaryArtistSaving =
    releasePrimaryArtist.scope === scope.current && releasePrimaryArtist.saving;
  const releasePrimaryArtistError =
    releasePrimaryArtist.scope === scope.current
      ? releasePrimaryArtist.error
      : "";
  const releasePrimaryArtistPending = useRef<
    { scope: typeof scope.current; token: number } | undefined
  >(undefined);
  const mounted = useRef(false);
  const currentReleaseID = useRef(releaseId);
  currentReleaseID.current = releaseId;
  const monitors = useRef(
    new Map<
      string,
      {
        stop: () => void;
        positions: Set<string>;
        cleanupWarnings: Map<string, Set<string>>;
        publicationPositions: Set<string>;
      }
    >(),
  );
  const finishedOperations = useRef(new Set<string>());
  const positionReadGenerations = useRef(new Map<string, number>());
  const lyricsEditGenerations = useRef(new Map<string, number>());
  const releaseReadGeneration = useRef(0);
  const positionWriteLocks = useRef(
    new Map<string, { scope: typeof scope.current; token: number }>(),
  );
  const writeToken = useRef(0);
  const cleanupRetryLocks = useRef(
    new Map<string, { scope: typeof scope.current; token: number }>(),
  );

  function acquirePositionWrite(positionId: string) {
    if (positionWriteLocks.current.get(positionId)?.scope === scope.current)
      return undefined;
    const lock = { scope: scope.current, token: ++writeToken.current };
    positionWriteLocks.current.set(positionId, lock);
    return lock;
  }

  function releasePositionWrite(
    positionId: string,
    lock: { scope: typeof scope.current; token: number },
  ) {
    if (positionWriteLocks.current.get(positionId) !== lock) return;
    positionWriteLocks.current.delete(positionId);
    if (!mounted.current || scope.current !== lock.scope) return;
    setPositions((current) => ({
      ...current,
      [positionId]: {
        ...current[positionId],
        writePending: false,
        saving: false,
        primaryArtistSaving: false,
      },
    }));
  }

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
        return undefined;
      if (metadata.status !== 200)
        throw new Error(errorMessage(metadata.data, metadata.status));
      if (publication.status !== 200)
        throw new Error(errorMessage(publication.data, publication.status));
      const field = metadata.data.envelope.desired.synced_lyrics;
      const warningIDs = new Set(
        (publication.data.source_cleanup_warnings ?? []).map(
          (warning) => warning.original_operation_id,
        ),
      );
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          metadata: metadata.data,
          publication: publication.data,
          cleanupRetryOperationIDs: Object.fromEntries(
            Object.entries(
              current[positionId]?.cleanupRetryOperationIDs ?? {},
            ).filter(([warningID]) => warningIDs.has(warningID)),
          ),
          cleanupRetryPendingIDs: (
            current[positionId]?.cleanupRetryPendingIDs ?? []
          ).filter((warningID) => warningIDs.has(warningID)),
          lyrics: current[positionId]?.draftDirty
            ? (current[positionId]?.lyrics ?? "")
            : field?.state === "value" && typeof field.value === "string"
              ? field.value
              : "",
          error: undefined,
          readError: undefined,
        },
      }));
      return publication.data;
    },
    [releaseId],
  );

  const refreshReleaseDetail = useCallback(
    async (
      requestScope: typeof scope.current,
    ): Promise<LibraryReleaseDetailResponse | undefined> => {
      if (!mounted.current || scope.current !== requestScope) return undefined;
      const generation = ++releaseReadGeneration.current;
      const response = await getLibraryRelease(requestScope.releaseId, {
        cache: "no-store",
      });
      if (
        !mounted.current ||
        scope.current !== requestScope ||
        releaseReadGeneration.current !== generation
      )
        return undefined;
      if (response.status !== 200)
        throw new Error(errorMessage(response.data, response.status));
      setRelease(response.data);
      return response.data;
    },
    [],
  );

  const completeMusicBrainzRefresh = useCallback(
    (
      snapshot: OperationResponse,
      requestScope: typeof scope.current,
      operationID: string,
    ) => {
      if (!mounted.current || scope.current !== requestScope) return;
      if (!isTerminalOperation(snapshot.state)) return;
      if (finishedOperations.current.has(operationID)) return;
      finishedOperations.current.add(operationID);
      setMusicBrainzRefresh((current) =>
        current.scope === requestScope && current.operationID === operationID
          ? {
              ...current,
              pending: false,
              active: false,
              notice:
                snapshot.state === "succeeded"
                  ? "Данные MusicBrainz обновлены. Обновление публикаций выполняется отдельно."
                  : undefined,
              error:
                snapshot.state === "succeeded"
                  ? undefined
                  : snapshot.safe_error ||
                    `Обновление MusicBrainz завершилось со статусом: ${snapshot.state}.`,
            }
          : current,
      );
      if (snapshot.state !== "succeeded") return;
      void (async () => {
        try {
          const detail = await refreshReleaseDetail(requestScope);
          if (!detail || !mounted.current || scope.current !== requestScope)
            return;
          await Promise.all(
            (detail.tracks ?? []).map((track) => refreshPosition(track.id)),
          );
        } catch (reason) {
          if (!mounted.current || scope.current !== requestScope) return;
          setMusicBrainzRefresh((current) =>
            current.scope === requestScope &&
            current.operationID === operationID
              ? {
                  ...current,
                  error: sharedErrorMessage(
                    reason,
                    "Не удалось обновить данные релиза.",
                  ),
                }
              : current,
          );
        }
      })();
    },
    [refreshPosition, refreshReleaseDetail],
  );

  const rereadMusicBrainzRelease = useCallback(
    async (requestScope: typeof scope.current, operationID: string) => {
      if (!mounted.current || scope.current !== requestScope) return;
      setMusicBrainzRefresh((current) =>
        current.scope === requestScope && current.operationID === operationID
          ? { ...current, active: false, pending: true }
          : current,
      );
      try {
        const detail = await refreshReleaseDetail(requestScope);
        if (!mounted.current || scope.current !== requestScope) return;
        if (!detail) {
          setMusicBrainzRefresh((current) =>
            current.scope === requestScope &&
            current.operationID === operationID
              ? { ...current, pending: false, operationID: undefined }
              : current,
          );
          return;
        }
        await Promise.all(
          (detail.tracks ?? []).map((track) => refreshPosition(track.id)),
        );
        if (!mounted.current || scope.current !== requestScope) return;
        setMusicBrainzRefresh((current) =>
          current.scope === requestScope && current.operationID === operationID
            ? { ...current, pending: false, operationID: undefined }
            : current,
        );
      } catch (reason) {
        if (!mounted.current || scope.current !== requestScope) return;
        setMusicBrainzRefresh((current) =>
          current.scope === requestScope
            ? {
                ...current,
                pending: false,
                operationID: undefined,
                error: sharedErrorMessage(
                  reason,
                  "Не удалось обновить данные релиза.",
                ),
              }
            : current,
        );
      }
    },
    [refreshPosition, refreshReleaseDetail],
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
                    readError: sharedErrorMessage(
                      reason,
                      "Не удалось загрузить данные публикации.",
                    ),
                  },
                }));
            }),
          ),
        );
      })
      .catch((reason: unknown) => {
        if (active && scope.current.releaseId === releaseId)
          setError(sharedErrorMessage(reason, "Не удалось загрузить релиз."));
      });
    return () => {
      active = false;
      for (const monitor of monitors.current.values()) monitor.stop();
      monitors.current.clear();
      finishedOperations.current.clear();
      positionReadGenerations.current.clear();
    };
  }, [releaseId, refreshPosition]);

  const musicBrainzRefreshScope = musicBrainzRefresh.scope;
  const musicBrainzRefreshOperationID =
    musicBrainzRefresh.scope === scope.current && musicBrainzRefresh.active
      ? musicBrainzRefresh.operationID
      : undefined;

  useEffect(() => {
    const requestScope = musicBrainzRefreshScope;
    const operationID = musicBrainzRefreshOperationID;
    if (!operationID) return;
    return subscribeToOperation(
      operationID,
      (snapshot) => {
        if (!mounted.current || scope.current !== requestScope) return;
        if (["queued", "running"].includes(snapshot.state)) {
          setMusicBrainzRefresh((current) =>
            current.scope === requestScope &&
            current.operationID === operationID
              ? { ...current, pending: false, active: true, error: undefined }
              : current,
          );
          return;
        }
        completeMusicBrainzRefresh(snapshot, requestScope, operationID);
      },
      (reason) => {
        if (!mounted.current || scope.current !== requestScope) return;
        setMusicBrainzRefresh((current) =>
          current.scope === requestScope && current.operationID === operationID
            ? {
                ...current,
                error: sharedErrorMessage(
                  reason,
                  "Не удалось проверить обновление MusicBrainz.",
                ),
              }
            : current,
        );
      },
      () => {
        void rereadMusicBrainzRelease(requestScope, operationID);
      },
    );
  }, [
    musicBrainzRefreshScope,
    musicBrainzRefreshOperationID,
    completeMusicBrainzRefresh,
    rereadMusicBrainzRelease,
  ]);

  // Keep one wake-only SSE subscription per operation, including attempts that
  // were already active when the release screen was opened.
  useEffect(() => {
    const requestScope = scope.current;
    const byOperation = new Map<string, Set<string>>();
    const cleanupByOperation = new Map<string, Map<string, Set<string>>>();
    const publicationByOperation = new Map<string, Set<string>>();
    const associate = (operationID: string | undefined, positionID: string) => {
      if (!operationID || finishedOperations.current.has(operationID)) return;
      const associated = byOperation.get(operationID) ?? new Set<string>();
      associated.add(positionID);
      byOperation.set(operationID, associated);
    };
    const associateCleanup = (
      operationID: string | undefined,
      positionID: string,
      originalID: string,
    ) => {
      associate(operationID, positionID);
      if (!operationID || finishedOperations.current.has(operationID)) return;
      const byPosition = cleanupByOperation.get(operationID) ?? new Map();
      const warningIDs = byPosition.get(positionID) ?? new Set<string>();
      warningIDs.add(originalID);
      byPosition.set(positionID, warningIDs);
      cleanupByOperation.set(operationID, byPosition);
    };
    const associatePublication = (
      operationID: string | undefined,
      positionID: string,
    ) => {
      associate(operationID, positionID);
      if (!operationID || finishedOperations.current.has(operationID)) return;
      const associated =
        publicationByOperation.get(operationID) ?? new Set<string>();
      associated.add(positionID);
      publicationByOperation.set(operationID, associated);
    };
    for (const [positionId, position] of Object.entries(
      loadedReleaseID === releaseId ? positions : {},
    )) {
      associatePublication(position.applyOperationID, positionId);
      associatePublication(position.firstPublicationOperationID, positionId);
      associatePublication(
        position.publication?.active_attempt?.id,
        positionId,
      );
      for (const warning of position.publication?.source_cleanup_warnings ??
        []) {
        associateCleanup(
          warning.active_retry_operation_id,
          positionId,
          warning.original_operation_id,
        );
        associateCleanup(
          position.cleanupRetryOperationIDs?.[warning.original_operation_id],
          positionId,
          warning.original_operation_id,
        );
      }
    }
    for (const [operationID, monitor] of monitors.current) {
      const nextPositions = byOperation.get(operationID);
      if (!nextPositions) {
        monitor.stop();
        monitors.current.delete(operationID);
      } else {
        monitor.positions = nextPositions;
        monitor.cleanupWarnings =
          cleanupByOperation.get(operationID) ?? new Map();
        monitor.publicationPositions =
          publicationByOperation.get(operationID) ?? new Set();
      }
    }
    for (const [operationID, positionIDs] of byOperation) {
      if (monitors.current.has(operationID)) continue;
      const entry = {
        stop: () => {},
        positions: positionIDs,
        cleanupWarnings: cleanupByOperation.get(operationID) ?? new Map(),
        publicationPositions:
          publicationByOperation.get(operationID) ?? new Set(),
      };
      const reconcileCleanup = async (
        positionID: string,
        operationID: string,
        originalIDs: Set<string>,
        diagnostic?: string,
      ) => {
        const readGeneration =
          (positionReadGenerations.current.get(positionID) ?? 0) + 1;
        positionReadGenerations.current.set(positionID, readGeneration);
        try {
          const response = await getLibraryPublication(releaseId, positionID, {
            cache: "no-store",
          });
          if (
            !mounted.current ||
            currentReleaseID.current !== releaseId ||
            scope.current !== requestScope ||
            positionReadGenerations.current.get(positionID) !== readGeneration
          )
            return;
          if (response.status !== 200)
            throw new Error(errorMessage(response.data, response.status));
          const publication = response.data;
          finishedOperations.current.add(operationID);
          const remaining = new Set(
            (publication.source_cleanup_warnings ?? []).map(
              (warning) => warning.original_operation_id,
            ),
          );
          setPositions((current) => {
            if (
              !mounted.current ||
              scope.current !== requestScope ||
              positionReadGenerations.current.get(positionID) !== readGeneration
            )
              return current;
            const previous = current[positionID];
            const retryIDs = { ...previous?.cleanupRetryOperationIDs };
            for (const [originalID, retryID] of Object.entries(retryIDs))
              if (retryID === operationID) delete retryIDs[originalID];
            const pending = (previous?.cleanupRetryPendingIDs ?? []).filter(
              (originalID) => !originalIDs.has(originalID),
            );
            const stillWarned = [...originalIDs].some((id) =>
              remaining.has(id),
            );
            return {
              ...current,
              [positionID]: {
                ...previous,
                publication,
                cleanupRetryOperationIDs: retryIDs,
                cleanupRetryPendingIDs: pending,
                error: stillWarned
                  ? (diagnostic ?? previous?.error)
                  : undefined,
              },
            };
          });
        } catch {
          if (
            !mounted.current ||
            scope.current !== requestScope ||
            positionReadGenerations.current.get(positionID) !== readGeneration
          )
            return;
          setPositions((current) => {
            if (
              !mounted.current ||
              scope.current !== requestScope ||
              positionReadGenerations.current.get(positionID) !== readGeneration
            )
              return current;
            return {
              ...current,
              [positionID]: {
                ...current[positionID],
                cleanupRetryPendingIDs: (
                  current[positionID]?.cleanupRetryPendingIDs ?? []
                ).filter((originalID) => !originalIDs.has(originalID)),
                error:
                  diagnostic ??
                  "Не удалось проверить очистку источников. Обновите данные публикации.",
                readError:
                  diagnostic ??
                  "Не удалось проверить очистку источников. Обновите данные публикации.",
              },
            };
          });
        }
      };
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
            const cleanupIDs = entry.cleanupWarnings.get(positionID);
            const isTerminal = ["succeeded", "failed", "cancelled"].includes(
              snapshot.state,
            );
            if (cleanupIDs?.size && isTerminal) {
              finishedOperations.current.add(operationID);
              if (!entry.publicationPositions.has(positionID))
                void reconcileCleanup(
                  positionID,
                  operationID,
                  cleanupIDs,
                  "Повтор очистки завершён, предупреждение источника остаётся актуальным.",
                );
            }
            if (!entry.publicationPositions.has(positionID)) {
              if (cleanupIDs?.size && !isTerminal)
                void refreshPosition(positionID).catch((reason: unknown) => {
                  if (!mounted.current || scope.current !== requestScope)
                    return;
                  setPositions((current) => ({
                    ...current,
                    ...(mounted.current && scope.current === requestScope
                      ? {
                          [positionID]: {
                            ...current[positionID],
                            readError: sharedErrorMessage(
                              reason,
                              "Не удалось обновить публикацию.",
                            ),
                          },
                        }
                      : {}),
                  }));
                });
              continue;
            }
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
                        applying:
                          current[positionID]?.applyOperationID === operationID
                            ? false
                            : current[positionID]?.applying,
                        applyOperationID:
                          current[positionID]?.applyOperationID === operationID
                            ? undefined
                            : current[positionID]?.applyOperationID,
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
                  const cleanupIDs = entry.cleanupWarnings.get(positionID);
                  if (cleanupIDs?.size)
                    void reconcileCleanup(
                      positionID,
                      operationID,
                      cleanupIDs,
                      "Повтор очистки завершён, предупреждение источника остаётся актуальным.",
                    );
                }
              })
              .catch((reason: unknown) => {
                if (
                  !mounted.current ||
                  currentReleaseID.current !== releaseId ||
                  scope.current !== requestScope
                )
                  return;
                const cleanupIDs = entry.cleanupWarnings.get(positionID);
                if (cleanupIDs?.size && isTerminal) {
                  void reconcileCleanup(
                    positionID,
                    operationID,
                    cleanupIDs,
                    "Повтор очистки завершён, предупреждение источника остаётся актуальным.",
                  );
                  return;
                }
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
                      readError: sharedErrorMessage(
                        reason,
                        "Не удалось обновить публикацию.",
                      ),
                    },
                  };
                });
              });
          }
        },
        (operationError) => {
          // The event stream is only a wake-up/error channel. Re-read both views;
          // on reconnect, its subsequent snapshot callback does the same.
          const missingOperation = /snapshot request failed \(404\)/i.test(
            operationError.message,
          );
          if (missingOperation) finishedOperations.current.add(operationID);
          for (const positionID of entry.positions) {
            const cleanupIDs = entry.cleanupWarnings.get(positionID);
            if (missingOperation && cleanupIDs?.size) {
              void reconcileCleanup(
                positionID,
                operationID,
                cleanupIDs,
                "Повтор очистки больше недоступен. Предупреждение источника остаётся актуальным.",
              );
              continue;
            }
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
                      readError: sharedErrorMessage(
                        reason,
                        "Не удалось обновить публикацию.",
                      ),
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
    if (!position?.metadata) return;
    const requestScope = scope.current;
    const writeLock = acquirePositionWrite(positionId);
    if (!writeLock) return;
    const submittedEditGeneration =
      lyricsEditGenerations.current.get(positionId) ?? 0;
    setPositions((current) => ({
      ...current,
      [positionId]: {
        ...current[positionId],
        saving: true,
        writePending: true,
        error: undefined,
        notice: undefined,
      },
    }));
    try {
      const field = explicitEmpty
        ? { state: "explicit_empty" }
        : {
            state: "value",
            value: position.lyrics,
          };
      const response = await patchLibraryPositionMetadata(
        releaseId,
        positionId,
        {
          expected_revision: position.metadata.revision,
          changes: { synced_lyrics: field },
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
      const savedField = response.data.envelope.desired.synced_lyrics;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          metadata: response.data,
          ...((lyricsEditGenerations.current.get(positionId) ?? 0) ===
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
          error: sharedErrorMessage(reason, "Не удалось сохранить текст."),
        },
      }));
    } finally {
      releasePositionWrite(positionId, writeLock);
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
      const freshField = fresh[0].data.envelope.desired.synced_lyrics;
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
          error: sharedErrorMessage(reason, "Не удалось применить текст."),
          readError: sharedErrorMessage(
            reason,
            "Не удалось проверить публикацию.",
          ),
        },
      }));
    }
  }

  async function retryCleanup(positionId: string, originalOperationID: string) {
    const position = positions[positionId];
    if (
      !position?.publication?.source_cleanup_warnings?.some(
        (warning) =>
          warning.original_operation_id === originalOperationID &&
          warning.can_retry,
      ) ||
      position.cleanupRetryPendingIDs?.includes(originalOperationID) ||
      cleanupRetryLocks.current.has(originalOperationID)
    )
      return;
    const requestScope = scope.current;
    const retryToken = { scope: requestScope, token: ++writeToken.current };
    cleanupRetryLocks.current.set(originalOperationID, retryToken);
    setPositions((current) => ({
      ...current,
      [positionId]: {
        ...current[positionId],
        cleanupRetryPendingIDs: Array.from(
          new Set([
            ...(current[positionId]?.cleanupRetryPendingIDs ?? []),
            originalOperationID,
          ]),
        ),
        error: undefined,
      },
    }));
    try {
      const response = await retryPublicationSourceCleanup(originalOperationID);
      if (response.status !== 202)
        throw new Error(errorMessage(response.data, response.status));
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          cleanupRetryOperationIDs: {
            ...current[positionId]?.cleanupRetryOperationIDs,
            [originalOperationID]: response.data.id,
          },
        },
      }));
      await refreshPosition(positionId);
    } catch (reason) {
      if (!mounted.current || scope.current !== requestScope) return;
      let warningGone = false;
      let projectionReadFailed = false;
      const readGeneration =
        (positionReadGenerations.current.get(positionId) ?? 0) + 1;
      positionReadGenerations.current.set(positionId, readGeneration);
      try {
        const refreshed = await getLibraryPublication(releaseId, positionId, {
          cache: "no-store",
        });
        if (
          !mounted.current ||
          scope.current !== requestScope ||
          cleanupRetryLocks.current.get(originalOperationID) !== retryToken ||
          positionReadGenerations.current.get(positionId) !== readGeneration
        )
          return;
        if (refreshed.status === 200) {
          warningGone = !refreshed.data.source_cleanup_warnings?.some(
            (warning) => warning.original_operation_id === originalOperationID,
          );
          setPositions((current) => {
            if (
              !mounted.current ||
              scope.current !== requestScope ||
              positionReadGenerations.current.get(positionId) !== readGeneration
            )
              return current;
            return {
              ...current,
              [positionId]: {
                ...current[positionId],
                publication: refreshed.data,
                ...(warningGone
                  ? {
                      cleanupRetryOperationIDs: Object.fromEntries(
                        Object.entries(
                          current[positionId]?.cleanupRetryOperationIDs ?? {},
                        ).filter(
                          ([warningID]) => warningID !== originalOperationID,
                        ),
                      ),
                    }
                  : {}),
                cleanupRetryPendingIDs: (
                  current[positionId]?.cleanupRetryPendingIDs ?? []
                ).filter((id) => id !== originalOperationID),
                ...(warningGone ? { error: undefined } : {}),
              },
            };
          });
        } else {
          projectionReadFailed = true;
        }
      } catch {
        // A failed read cannot prove that the warning has disappeared.
        if (
          !mounted.current ||
          scope.current !== requestScope ||
          cleanupRetryLocks.current.get(originalOperationID) !== retryToken ||
          positionReadGenerations.current.get(positionId) !== readGeneration
        )
          return;
        projectionReadFailed = true;
      }
      if (
        !mounted.current ||
        scope.current !== requestScope ||
        cleanupRetryLocks.current.get(originalOperationID) !== retryToken ||
        positionReadGenerations.current.get(positionId) !== readGeneration
      )
        return;
      if (warningGone) return;
      setPositions((current) => {
        if (
          !mounted.current ||
          scope.current !== requestScope ||
          positionReadGenerations.current.get(positionId) !== readGeneration
        )
          return current;
        return {
          ...current,
          [positionId]: {
            ...current[positionId],
            cleanupRetryPendingIDs: (
              current[positionId]?.cleanupRetryPendingIDs ?? []
            ).filter((id) => id !== originalOperationID),
            error: sharedErrorMessage(
              reason,
              "Не удалось повторить очистку источников.",
            ),
            ...(projectionReadFailed
              ? {
                  readError:
                    "Не удалось проверить публикацию. Обновите данные перед повторной попыткой.",
                }
              : {}),
          },
        };
      });
    } finally {
      if (cleanupRetryLocks.current.get(originalOperationID) === retryToken)
        cleanupRetryLocks.current.delete(originalOperationID);
    }
  }

  async function selectPrimaryArtist(
    positionId: string,
    artistID: string | null,
  ) {
    const track = release?.tracks?.find((item) => item.id === positionId);
    const position = positions[positionId];
    if (!track || !position?.metadata) return;
    const requestScope = scope.current;
    const writeLock = acquirePositionWrite(positionId);
    if (!writeLock) return;
    setPositions((current) => ({
      ...current,
      [positionId]: {
        ...current[positionId],
        primaryArtistSaving: true,
        writePending: true,
        error: undefined,
      },
    }));
    try {
      const result = await setLibraryPositionPrimaryArtist(
        releaseId,
        positionId,
        {
          artist_id: artistID,
          expected_revision: position.metadata.revision,
          expected_track_revision: track.track_metadata_revision,
        },
      );
      if (!mounted.current || scope.current !== requestScope) return;
      if (result.status !== 200)
        throw new Error(errorMessage(result.data, result.status));
      await refreshReleaseDetail(requestScope);
      if (!mounted.current || scope.current !== requestScope) return;
      await refreshPosition(positionId);
      if (!mounted.current || scope.current !== requestScope) return;
    } catch (reason) {
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          error: sharedErrorMessage(reason, "Не удалось выбрать исполнителя."),
        },
      }));
    } finally {
      releasePositionWrite(positionId, writeLock);
    }
  }

  async function selectReleasePrimaryArtist(artistID: string | null) {
    if (!release || releasePrimaryArtistSaving) return;
    const requestScope = scope.current;
    if (releasePrimaryArtistPending.current?.scope === requestScope) return;
    const pending = { scope: requestScope, token: ++writeToken.current };
    releasePrimaryArtistPending.current = pending;
    setReleasePrimaryArtist({ ...pending, saving: true, error: "" });
    try {
      const result = await setLibraryReleasePrimaryArtist(releaseId, {
        artist_id: artistID,
        expected_revision: release.metadata_revision,
      });
      if (!mounted.current || scope.current !== requestScope) return;
      if (result.status !== 200)
        throw new Error(errorMessage(result.data, result.status));
      if (!(await refreshReleaseDetail(requestScope))) return;
    } catch (reason) {
      if (
        !mounted.current ||
        scope.current !== requestScope ||
        releasePrimaryArtistPending.current !== pending
      )
        return;
      setReleasePrimaryArtist({
        ...pending,
        saving: false,
        error: sharedErrorMessage(
          reason,
          "Не удалось выбрать исполнителя релиза.",
        ),
      });
    } finally {
      if (releasePrimaryArtistPending.current === pending)
        releasePrimaryArtistPending.current = undefined;
      if (mounted.current && scope.current === requestScope)
        setReleasePrimaryArtist((current) =>
          current.scope === requestScope && current.token === pending.token
            ? { ...current, saving: false }
            : current,
        );
    }
  }

  async function refreshMusicBrainz() {
    if (
      !release?.can_refresh_musicbrainz ||
      musicBrainzRefreshPending.current?.scope === scope.current ||
      (musicBrainzRefresh.scope === scope.current && musicBrainzRefresh.active)
    )
      return;
    const requestScope = scope.current;
    const pending = {
      scope: requestScope,
      token: ++musicBrainzRefreshToken.current,
    };
    musicBrainzRefreshPending.current = pending;
    setMusicBrainzRefresh({
      scope: requestScope,
      pending: true,
      active: false,
    });
    try {
      const response = await refreshLibraryReleaseMusicbrainz(releaseId);
      if (!mounted.current || scope.current !== requestScope) return;
      if (response.status !== 202)
        throw new Error(errorMessage(response.data, response.status));
      setMusicBrainzRefresh({
        scope: requestScope,
        operationID: response.data.id,
        pending: false,
        active: !isTerminalOperation(response.data.state),
      });
      if (isTerminalOperation(response.data.state))
        completeMusicBrainzRefresh(
          response.data,
          requestScope,
          response.data.id,
        );
    } catch (reason) {
      if (!mounted.current || scope.current !== requestScope) return;
      setMusicBrainzRefresh({
        scope: requestScope,
        pending: false,
        active: false,
        error: sharedErrorMessage(
          reason,
          "Не удалось запустить обновление MusicBrainz.",
        ),
      });
    } finally {
      if (musicBrainzRefreshPending.current === pending)
        musicBrainzRefreshPending.current = undefined;
    }
  }

  function uniqueArtists(artists: LibraryReleaseDetailResponse["artists"]) {
    return Array.from(
      new Map(
        (artists ?? []).map((artist) => [artist.artist_id, artist]),
      ).values(),
    );
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
  const selectedTrack = positionId
    ? (release.tracks ?? []).find((track) => track.id === positionId)
    : undefined;
  const visibleTracks = positionId
    ? selectedTrack
      ? [selectedTrack]
      : []
    : (release.tracks ?? []);
  return (
    <section className="library-release-screen mx-auto max-w-5xl space-y-6">
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
        {(release.artists?.length ?? 0) > 1 && (
          <label className="block">
            Основной исполнитель релиза
            <select
              aria-label="Основной исполнитель релиза"
              value={release.primary_artist.selected_artist_id || ""}
              disabled={releasePrimaryArtistSaving}
              onChange={(event) =>
                void selectReleasePrimaryArtist(event.target.value || null)
              }
            >
              <option value="">Не выбран</option>
              {uniqueArtists(release.artists).map((artist) => (
                <option key={artist.artist_id} value={artist.artist_id}>
                  {artist.credit_name || artist.artist_name}
                </option>
              ))}
            </select>
            {releasePrimaryArtistError && (
              <p role="alert">{releasePrimaryArtistError}</p>
            )}
          </label>
        )}
        {release.can_refresh_musicbrainz && (
          <div className="mt-3 space-y-2">
            <button
              type="button"
              disabled={
                (musicBrainzRefresh.scope === scope.current &&
                  (musicBrainzRefresh.pending || musicBrainzRefresh.active)) ||
                musicBrainzRefreshPending.current?.scope === scope.current
              }
              onClick={() => void refreshMusicBrainz()}
            >
              {musicBrainzRefresh.scope === scope.current &&
              (musicBrainzRefresh.pending || musicBrainzRefresh.active)
                ? "Обновление MusicBrainz выполняется…"
                : "Обновить данные MusicBrainz"}
            </button>
            {musicBrainzRefresh.scope === scope.current &&
              musicBrainzRefresh.active && (
                <p role="status">Ожидание обновления данных MusicBrainz…</p>
              )}
            {musicBrainzRefresh.scope === scope.current &&
              musicBrainzRefresh.notice && (
                <p role="status">{musicBrainzRefresh.notice}</p>
              )}
            {musicBrainzRefresh.scope === scope.current &&
              musicBrainzRefresh.error && (
                <p role="alert">{musicBrainzRefresh.error}</p>
              )}
          </div>
        )}
      </header>
      {positionId && (
        <a className="underline" href={`#/library/releases/${releaseId}`}>
          ← Полный релиз
        </a>
      )}
      <ReleaseCoverEditor
        releaseId={releaseId}
        epoch={
          Object.values(positions).find(
            (position) => position.publication?.active_epoch,
          )?.publication?.active_epoch
        }
        onOperationsAdmitted={(operations) => {
          const requestScope = scope.current;
          setPositions((current) => {
            if (!mounted.current || scope.current !== requestScope)
              return current;
            const next = { ...current };
            for (const operation of operations) {
              next[operation.position_id] = {
                ...next[operation.position_id],
                applyOperationID: operation.operation_id,
                applying: true,
                notice:
                  "Применение обложки запущено. Ожидается проверка опубликованных данных.",
              };
            }
            return next;
          });
        }}
      />
      {positionId && !selectedTrack ? (
        <p role="alert">Позиция не найдена в этом релизе.</p>
      ) : null}
      {visibleTracks.map((track) => {
        const renderScope = scope.current;
        const position = positions[track.id];
        const publication = position?.publication;
        const lyricsField = position?.metadata?.envelope.desired.synced_lyrics;
        const hasManualLyrics =
          lyricsField?.authority === "manual" &&
          ["value", "explicit_empty"].includes(lyricsField.state);
        const credits = track.artists ?? [];

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
                  readError: sharedErrorMessage(
                    reason,
                    "Не удалось обновить публикацию.",
                  ),
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
            {credits.length > 1 && (
              <label className="block">
                Основной исполнитель композиции
                <select
                  aria-label={`Основной исполнитель композиции ${track.displayed_number}`}
                  value={track.primary_artist.selected_artist_id || ""}
                  disabled={!position?.metadata || position.writePending}
                  onChange={(event) =>
                    void selectPrimaryArtist(
                      track.id,
                      event.target.value || null,
                    )
                  }
                >
                  <option value="">Не выбран</option>
                  {uniqueArtists(credits).map((artist) => (
                    <option key={artist.artist_id} value={artist.artist_id}>
                      {artist.credit_name || artist.artist_name}
                    </option>
                  ))}
                </select>
              </label>
            )}
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
                  position.writePending ||
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
                  position.writePending ||
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
            {publication?.current_generation && (
              <fieldset aria-label="Фактическая обложка текущей публикации">
                <p>
                  Фактическая обложка:{" "}
                  {publication.current_assets?.cover.state ?? "unknown"}
                  {publication.current_assets?.cover.sha256
                    ? ` · SHA-256: ${publication.current_assets.cover.sha256}`
                    : ""}
                </p>
                {publication.cover_embedding && (
                  <p>
                    Встраивание выбранной обложки:{" "}
                    {publication.cover_embedding.outcome === "verified"
                      ? "проверено"
                      : "не подтверждено"}
                    {publication.cover_embedding.warning_code
                      ? ` · ${publication.cover_embedding.warning_code}`
                      : ""}
                  </p>
                )}
              </fieldset>
            )}
            {publication?.initial_lyrics && (
              <fieldset aria-label="Результат текста песни для текущей публикации">
                <p>
                  Текст песни в текущей публикации:{" "}
                  {initialLyricsOutcomeLabels[
                    publication.initial_lyrics.outcome
                  ] ?? publication.initial_lyrics.outcome}
                  {` · источник: ${initialLyricsSourceLabels[publication.initial_lyrics.source] ?? publication.initial_lyrics.source}`}
                </p>
                {publication.initial_lyrics.resolution_warning_code && (
                  <p>
                    Предупреждение поиска:{" "}
                    {initialLyricsWarningLabels[
                      publication.initial_lyrics.resolution_warning_code
                    ] ?? publication.initial_lyrics.resolution_warning_code}
                  </p>
                )}
                {publication.initial_lyrics.outcome_warning_code && (
                  <p>
                    Предупреждение публикации:{" "}
                    {initialLyricsWarningLabels[
                      publication.initial_lyrics.outcome_warning_code
                    ] ?? publication.initial_lyrics.outcome_warning_code}
                  </p>
                )}
              </fieldset>
            )}
            {publication?.source_cleanup_warnings?.map((warning) => (
              <div key={warning.original_operation_id} role="alert">
                <p>{warning.safe_error}</p>
                {warning.can_retry && (
                  <button
                    type="button"
                    disabled={
                      position?.cleanupRetryPendingIDs?.includes(
                        warning.original_operation_id,
                      ) ||
                      cleanupRetryLocks.current.has(
                        warning.original_operation_id,
                      ) ||
                      Boolean(position?.readError)
                    }
                    onClick={() =>
                      void retryCleanup(track.id, warning.original_operation_id)
                    }
                  >
                    {position?.cleanupRetryPendingIDs?.includes(
                      warning.original_operation_id,
                    )
                      ? "Повтор выполняется…"
                      : "Повторить очистку источников"}
                  </button>
                )}
                {warning.active_retry_operation_id &&
                  !finishedOperations.current.has(
                    warning.active_retry_operation_id,
                  ) && <p role="status">Повторная очистка выполняется.</p>}
              </div>
            ))}
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
                ? Object.entries(publication.current_metadata.tags)
                    .map(
                      ([name, values]) =>
                        `${name}: ${(values ?? []).join(", ")}`,
                    )
                    .join("; ") || "теги отсутствуют"
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
