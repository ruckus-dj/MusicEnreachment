import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { subscribeToOperation } from "../../api/client/operations";
import {
  applyLibraryManualLyrics,
  getGetLibraryReleaseCoverImageUrl,
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
import { useI18n } from "../../i18n/I18nProvider";
import {
  type LibraryReleaseMessage,
  type LibraryReleaseText,
  LocalizedLibraryReleaseError,
  libraryReleaseError,
  libraryReleaseMessage,
  libraryReleaseText,
  renderLibraryReleaseText,
} from "./libraryReleaseMessages";
import "./library.css";
import { AggregatePublicationPreview } from "./AggregatePublicationPreview";
import {
  FirstPublicationPreview,
  type PublicationPreviewController,
  type PublicationPreviewState,
} from "./FirstPublicationPreview";
import { GenreMetadataEditor } from "./GenreMetadataEditor";
import { LibraryDeletionControl } from "./LibraryDeletionControl";
import {
  LibraryPositionHeadingView,
  libraryPositionPageClassName,
  libraryPositionTitleId,
} from "./LibraryPositionHeadingView";
import { LibraryPositionTagEvidence } from "./LibraryPositionTagEvidence";
import { LibraryPositionTagsDisclosure } from "./LibraryPositionTagsDisclosure";
import { LibraryPositionTechnicalDetailsView } from "./LibraryPositionTechnicalDetailsView";
import { LibraryPositionWorkspace } from "./LibraryPositionWorkspace";
import {
  PositionIdentity,
  PositionSources,
  ReleaseCreditList,
  ReleaseMediaSummary,
} from "./LibraryReleaseFacts";
import { ReleaseCoverEditor } from "./newReleaseCoverEditor";
import {
  PublicationCurrentDetailsView,
  PublicationCurrentSummaryView,
} from "./PublicationCurrentSummaryView";
import { PublicationTagSnapshotView } from "./PublicationTagSnapshotView";
import {
  ReleaseMetadataEditor,
  ScopedLibraryMetadataEditor,
} from "./ReleaseMetadataEditor";

type PositionData = {
  metadata?: LibraryMetadataResponse;
  publication?: LibraryPublicationResponse;
  lyrics: string;
  draftDirty?: boolean;
  error?: LibraryReleaseText;
  readError?: LibraryReleaseText;
  saving?: boolean;
  applying?: boolean;
  applyOperationID?: string;
  firstPublicationOperationID?: string;
  cleanupRetryOperationIDs?: Record<string, string>;
  cleanupRetryPendingIDs?: string[];
  notice?: LibraryReleaseText;
  primaryArtistSaving?: boolean;
  writePending?: boolean;
};

function errorMessage(data: { detail?: string }, status: number): Error {
  if (data.detail) return new Error(data.detail);
  return new LocalizedLibraryReleaseError(
    libraryReleaseText("requestFailed", { status }),
  );
}

function isTerminalOperation(state: string) {
  return ["succeeded", "failed", "cancelled"].includes(state);
}

function lyricsSourceLabel(source: string, locale: "en" | "ru") {
  const key =
    source === "none"
      ? "lyricsSourceNone"
      : source === "source"
        ? "lyricsSourceLibrary"
        : source === "lrclib"
          ? "lyricsSourceLrclib"
          : undefined;
  return key ? libraryReleaseMessage(locale, key) : source;
}

function lyricsOutcomeLabel(outcome: string, locale: "en" | "ru") {
  const key =
    outcome === "not_requested"
      ? "lyricsOutcomeNotRequested"
      : outcome === "exact_round_trip"
        ? "lyricsOutcomeVerified"
        : outcome === "rebuilt_without_lyrics"
          ? "lyricsOutcomeRebuiltWithout"
          : undefined;
  return key ? libraryReleaseMessage(locale, key) : outcome;
}

function lyricsWarningLabel(code: string, locale: "en" | "ru") {
  const keys: Record<string, LibraryReleaseMessage> = {
    lyrics_source_selection_required: "warningSourceSelection",
    lyrics_settings_unavailable: "warningSettingsUnavailable",
    lyrics_lookup_metadata_incomplete: "warningMetadataIncomplete",
    lyrics_provider_unavailable: "warningProviderUnavailable",
    lyrics_lookup_failed: "warningLookupFailed",
    lyrics_lookup_invalid_synced_text: "warningInvalidSyncedText",
    readback_mismatch: "warningReadbackMismatch",
  };
  const key = keys[code];
  return key ? libraryReleaseMessage(locale, key) : code;
}

export function LibraryReleaseScreen({
  releaseId,
  positionId,
}: {
  releaseId: string;
  positionId?: string;
}) {
  const { locale } = useI18n();
  const text = (
    key: LibraryReleaseMessage,
    values?: Record<string, string | number>,
  ) => libraryReleaseMessage(locale, key, values);
  const displayText = (value: LibraryReleaseText) =>
    renderLibraryReleaseText(locale, value);
  const scope = useRef({ releaseId, generation: 0 });
  if (scope.current.releaseId !== releaseId) {
    scope.current = { releaseId, generation: scope.current.generation + 1 };
  }
  const [release, setRelease] = useState<LibraryReleaseDetailResponse>();
  const [loadedReleaseID, setLoadedReleaseID] = useState("");
  const [positions, setPositions] = useState<Record<string, PositionData>>({});
  const [tagInspection, setTagInspection] = useState<{
    releaseId: string;
    positionId: string;
    variantId?: string;
    open: boolean;
  }>();
  useEffect(() => {
    setTagInspection((current) =>
      current?.releaseId === releaseId && current.positionId === positionId
        ? current
        : undefined,
    );
  }, [releaseId, positionId]);
  const [selectedCover, setSelectedCover] = useState<{
    scope: typeof scope.current;
    releaseID: string;
    generation?: string;
  }>();
  const deletionIdentityByPosition = useRef(new Map<string, string>());
  const aggregateControllers = useRef(
    new Map<string, PublicationPreviewController>(),
  );
  const [aggregateStates, setAggregateStates] = useState<
    Record<string, PublicationPreviewState>
  >({});
  const [, setAggregateControllerVersion] = useState(0);
  const aggregateCallbacks = useRef(
    new Map<
      string,
      {
        onController: (controller: PublicationPreviewController | null) => void;
        onStateChange: (state: PublicationPreviewState) => void;
      }
    >(),
  );
  const [error, setError] = useState<LibraryReleaseText | "">("");
  const [genreContextGeneration, setGenreContextGeneration] = useState(0);
  const [genreRefreshError, setGenreRefreshError] = useState<
    LibraryReleaseText | ""
  >("");
  const [musicBrainzRefresh, setMusicBrainzRefresh] = useState<{
    scope: typeof scope.current;
    operationID?: string;
    pending: boolean;
    active: boolean;
    notice?: LibraryReleaseText;
    error?: LibraryReleaseText;
  }>({ scope: scope.current, pending: false, active: false });
  const musicBrainzRefreshPending = useRef<
    { scope: typeof scope.current; token: number } | undefined
  >(undefined);
  const musicBrainzRefreshToken = useRef(0);
  const [releasePrimaryArtist, setReleasePrimaryArtist] = useState<{
    scope: typeof scope.current;
    token: number;
    saving: boolean;
    error: LibraryReleaseText | "";
  }>({
    scope: scope.current,
    token: 0,
    saving: false,
    error: "" as LibraryReleaseText | "",
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

  function aggregateCallbacksFor(positionID: string) {
    let callbacks = aggregateCallbacks.current.get(positionID);
    if (!callbacks) {
      callbacks = {
        onController: (controller) => {
          if (controller)
            aggregateControllers.current.set(positionID, controller);
          else aggregateControllers.current.delete(positionID);
          setAggregateControllerVersion((version) => version + 1);
        },
        onStateChange: (state) => {
          setAggregateStates((current) => ({
            ...current,
            [positionID]: state,
          }));
        },
      };
      aggregateCallbacks.current.set(positionID, callbacks);
    }
    return callbacks;
  }

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
    async (positionId: string, invalidateCurrentTags = false) => {
      const requestScope = scope.current;
      const readGeneration =
        (positionReadGenerations.current.get(positionId) ?? 0) + 1;
      positionReadGenerations.current.set(positionId, readGeneration);
      if (invalidateCurrentTags) {
        setPositions((current) => {
          const position = current[positionId];
          if (!position?.publication?.current_metadata) return current;
          return {
            ...current,
            [positionId]: {
              ...position,
              publication: {
                ...position.publication,
                current_metadata: undefined,
              },
            },
          };
        });
      }
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
        throw errorMessage(metadata.data, metadata.status);
      if (publication.status !== 200)
        throw errorMessage(publication.data, publication.status);
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
        throw errorMessage(response.data, response.status);
      setRelease(response.data);
      return response.data;
    },
    [],
  );

  const metadataRequestScope = scope.current;
  async function refreshAfterMetadataSave() {
    const requestScope = metadataRequestScope;
    if (!mounted.current || scope.current !== requestScope) return;
    // The PATCH is already committed. Invalidate every preview immediately,
    // even if the follow-up release detail read fails or is superseded.
    setGenreContextGeneration((generation) => generation + 1);
    setGenreRefreshError("");
    try {
      const detail = await refreshReleaseDetail(requestScope);
      if (!detail || !mounted.current || scope.current !== requestScope) return;
    } catch (reason) {
      if (!mounted.current || scope.current !== requestScope) return;
      setGenreRefreshError(libraryReleaseError(reason, "refreshPreviewFailed"));
    }
  }

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
                  ? libraryReleaseText("musicBrainzUpdated")
                  : undefined,
              error:
                snapshot.state === "succeeded"
                  ? undefined
                  : snapshot.safe_error ||
                    libraryReleaseText("musicBrainzFailed", {
                      state: snapshot.state,
                    }),
            }
          : current,
      );
      if (snapshot.state !== "succeeded") return;
      // A provider refresh can change the read-only genre baseline without
      // changing local metadata revision. Ask the editor to reload its GET data.
      setGenreContextGeneration((generation) => generation + 1);
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
                  error: libraryReleaseError(reason, "refreshReleaseFailed"),
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
                error: libraryReleaseError(reason, "refreshReleaseFailed"),
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
          throw errorMessage(response.data, response.status);
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
                    readError: libraryReleaseError(
                      reason,
                      "publicationLoadFailed",
                    ),
                  },
                }));
            }),
          ),
        );
      })
      .catch((reason: unknown) => {
        if (active && scope.current.releaseId === releaseId)
          setError(libraryReleaseError(reason, "releaseLoadFailed"));
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
                error: libraryReleaseError(reason, "checkMusicBrainzFailed"),
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
        diagnostic?: LibraryReleaseText,
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
            throw errorMessage(response.data, response.status);
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
                error: diagnostic ?? libraryReleaseText("cleanupCheckFailed"),
                readError:
                  diagnostic ?? libraryReleaseText("cleanupCheckFailed"),
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
                  libraryReleaseText("cleanupStillWarning"),
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
                            readError: libraryReleaseError(
                              reason,
                              "publicationRefreshFailed",
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
                            ? libraryReleaseText("operationCompleted")
                            : libraryReleaseText("operationEnded", {
                                state: snapshot.state,
                              }),
                      },
                    };
                  });
                  const cleanupIDs = entry.cleanupWarnings.get(positionID);
                  if (cleanupIDs?.size)
                    void reconcileCleanup(
                      positionID,
                      operationID,
                      cleanupIDs,
                      libraryReleaseText("cleanupStillWarning"),
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
                    libraryReleaseText("cleanupStillWarning"),
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
                      readError: libraryReleaseError(
                        reason,
                        "publicationRefreshFailed",
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
                libraryReleaseText("cleanupRetryUnavailable"),
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
                      readError: libraryReleaseError(
                        reason,
                        "publicationRefreshFailed",
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
        throw errorMessage(response.data, response.status);
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
          notice: libraryReleaseText("lyricsSaved"),
        },
      }));
    } catch (reason) {
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          error: libraryReleaseError(reason, "saveLyricsFailed"),
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
        throw errorMessage(fresh[0].data, fresh[0].status);
      if (fresh[1].status !== 200)
        throw errorMessage(fresh[1].data, fresh[1].status);
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
        throw new LocalizedLibraryReleaseError(
          libraryReleaseText("changedOrLyricsNotReady"),
        );
      }
      if (!mounted.current || scope.current !== requestScope) return;
      const response = await applyLibraryManualLyrics(releaseId, positionId, {
        expected_epoch: publication.current_epoch,
        expected_generation: publication.current_generation,
        expected_metadata_revision: fresh[0].data.revision,
      });
      if (response.status !== 202)
        throw errorMessage(response.data, response.status);
      if (!mounted.current || scope.current !== requestScope) return;
      setPositions((current) => ({
        ...current,
        [positionId]: {
          ...current[positionId],
          notice: libraryReleaseText("applyStarted"),
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
          error: libraryReleaseError(reason, "applyLyricsFailed"),
          readError: libraryReleaseError(reason, "verifyPublicationFailed"),
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
        throw errorMessage(response.data, response.status);
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
            error: libraryReleaseError(reason, "retryCleanupFailed"),
            ...(projectionReadFailed
              ? {
                  readError: libraryReleaseText("verifyBeforeRetry"),
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
      if (result.status !== 200) throw errorMessage(result.data, result.status);
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
          error: libraryReleaseError(reason, "chooseArtistFailed"),
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
      if (result.status !== 200) throw errorMessage(result.data, result.status);
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
        error: libraryReleaseError(reason, "chooseReleaseArtistFailed"),
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
        throw errorMessage(response.data, response.status);
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
        error: libraryReleaseError(reason, "startMusicBrainzFailed"),
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

  function handleDeletionSnapshot(
    target: "release" | "publication",
    snapshot: OperationResponse,
    requestScope: typeof scope.current,
    targetPositionId?: string,
  ) {
    if (
      !mounted.current ||
      scope.current !== requestScope ||
      currentReleaseID.current !== releaseId
    )
      return;
    // A terminal deletion can remove the file even if the readback fails
    // (including a partially successful release deletion).
    const invalidateCurrentTags = isTerminalOperation(snapshot.state);
    if (target === "release") {
      void refreshReleaseDetail(requestScope).catch(() => undefined);
      void Promise.all(
        (release?.tracks ?? []).map((track) =>
          refreshPosition(track.id, invalidateCurrentTags).catch(
            () => undefined,
          ),
        ),
      );
      if (snapshot.state === "succeeded") window.location.hash = "/library";
      return;
    }
    if (targetPositionId) {
      void refreshPosition(targetPositionId, invalidateCurrentTags).catch(
        () => undefined,
      );
      void refreshReleaseDetail(requestScope).catch(() => undefined);
    }
  }

  if (error)
    return (
      <section>
        <h1>{text("library")}</h1>
        <p role="alert">{displayText(error)}</p>
        <button
          type="button"
          onClick={() => {
            window.location.hash = "/incoming-groups";
          }}
        >
          {text("incoming")}
        </button>
      </section>
    );
  if (!release || loadedReleaseID !== releaseId)
    return <p role="status">{text("loadingRelease")}</p>;
  const releaseRenderScope = scope.current;
  const selectedCoverGeneration =
    selectedCover?.scope === releaseRenderScope &&
    selectedCover.releaseID === releaseId
      ? selectedCover.generation
      : undefined;
  const selectedTrack = positionId
    ? (release.tracks ?? []).find((track) => track.id === positionId)
    : undefined;
  const visibleTracks = positionId
    ? selectedTrack
      ? [selectedTrack]
      : []
    : (release.tracks ?? []);
  const positionLabels = new Map(
    (release.tracks ?? []).map((track) => [
      track.id,
      `${track.displayed_number.trim() || track.position}. ${track.title}`,
    ]),
  );
  const showPosition = Boolean(selectedTrack);
  const body = (
    <section
      className={`library-release-screen space-y-6 ${showPosition ? "min-w-0" : "mx-auto max-w-5xl"}`}
    >
      <nav
        className="library-release-navigation"
        aria-label={text("libraryNavigation")}
        hidden={showPosition}
      >
        <a className="underline" href="#/library">
          {text("libraryCatalog")}
        </a>
        {positionId ? (
          <a className="underline" href="#/incoming-groups">
            {text("incoming")}
          </a>
        ) : null}
      </nav>
      <header hidden={showPosition}>
        <div className="library-release-heading">
          <span
            className="library-release-cover"
            aria-hidden={!selectedCoverGeneration}
          >
            {selectedCoverGeneration ? (
              <img
                src={getGetLibraryReleaseCoverImageUrl(
                  releaseId,
                  selectedCoverGeneration,
                )}
                alt={text("selectedReleaseCover")}
              />
            ) : null}
          </span>
          <div className="library-release-title">
            <h1 className="text-2xl font-semibold">{release.title}</h1>
            <ReleaseCreditList credits={release.artists} />
          </div>
        </div>
        {(release.artists?.length ?? 0) > 1 && (
          <label className="block">
            {text("primaryReleaseArtist")}
            <select
              aria-label={text("primaryReleaseArtist")}
              value={release.primary_artist.selected_artist_id || ""}
              disabled={releasePrimaryArtistSaving}
              onChange={(event) =>
                void selectReleasePrimaryArtist(event.target.value || null)
              }
            >
              <option value="">{text("notSelected")}</option>
              {uniqueArtists(release.artists).map((artist) => (
                <option key={artist.artist_id} value={artist.artist_id}>
                  {artist.credit_name || artist.artist_name}
                </option>
              ))}
            </select>
            {releasePrimaryArtistError && (
              <p role="alert">{displayText(releasePrimaryArtistError)}</p>
            )}
          </label>
        )}
        {!positionId ? (
          <div className="mt-3">
            <LibraryDeletionControl
              target={{ type: "release", releaseId }}
              releaseTitle={release.title}
              positionLabels={positionLabels}
              onSnapshot={(snapshot) =>
                handleDeletionSnapshot("release", snapshot, releaseRenderScope)
              }
            />
          </div>
        ) : null}
      </header>
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
              ? text("refreshMusicBrainzRunning")
              : text("refreshMusicBrainz")}
          </button>
          {musicBrainzRefresh.scope === scope.current &&
            musicBrainzRefresh.active && (
              <p role="status">{text("waitingMusicBrainz")}</p>
            )}
          {musicBrainzRefresh.scope === scope.current &&
            musicBrainzRefresh.notice && (
              <p role="status">{displayText(musicBrainzRefresh.notice)}</p>
            )}
          {musicBrainzRefresh.scope === scope.current &&
            musicBrainzRefresh.error && (
              <p role="alert">{displayText(musicBrainzRefresh.error)}</p>
            )}
        </div>
      )}
      {genreRefreshError ? (
        <p role="alert">{displayText(genreRefreshError)}</p>
      ) : null}
      <div className="contents" hidden={showPosition} data-release-only>
        <ReleaseMetadataEditor
          releaseId={releaseId}
          refreshToken={`${scope.current.generation}:${release.metadata_revision}:${genreContextGeneration}`}
          onSaved={refreshAfterMetadataSave}
          visible={!showPosition}
        />
        <ReleaseMediaSummary release={release} />
      </div>
      {!positionId && (
        <AggregatePublicationPreview
          key={releaseId}
          positions={visibleTracks.map((track) => ({
            id: track.id,
            label: `${track.displayed_number.trim() || track.position}. ${track.title}`,
            current: Boolean(
              positions[track.id]?.publication?.current_path ||
                positions[track.id]?.publication?.current_generation,
            ),
          }))}
          controllers={aggregateControllers.current}
          states={aggregateStates}
        />
      )}
      {positionId && !selectedTrack && (
        <a className="underline" href={`#/library/releases/${releaseId}`}>
          {text("fullRelease")}
        </a>
      )}
      <div className="contents" hidden={showPosition} data-release-only>
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
                  notice: libraryReleaseText("coverApplyAdmitted"),
                };
              }
              return next;
            });
          }}
          onCoverSnapshot={(snapshotReleaseID, generation) => {
            if (
              scope.current !== releaseRenderScope ||
              snapshotReleaseID !== releaseId
            )
              return;
            setSelectedCover({
              scope: releaseRenderScope,
              releaseID: snapshotReleaseID,
              generation,
            });
          }}
        />
      </div>
      {positionId && !selectedTrack ? (
        <p role="alert">{text("positionNotFound")}</p>
      ) : null}
      {(release.tracks ?? []).map((track) => {
        const showWorkspace = positionId === track.id;
        const renderScope = scope.current;
        const position = positions[track.id];
        const publication = position?.publication;
        const currentTags =
          publication?.current_generation && !position?.readError
            ? publication.current_metadata?.tags
            : undefined;
        const hasCurrentPublication = publication
          ? Boolean(publication.current_generation || publication.current_path)
          : Boolean(track.publication?.generation);
        const inspection =
          showWorkspace &&
          tagInspection?.releaseId === releaseId &&
          tagInspection.positionId === track.id
            ? tagInspection
            : undefined;
        const inspectedSource = (track.sources ?? []).find(
          (source) => source.media_variant_id === inspection?.variantId,
        );
        const currentDeletionIdentity =
          publication?.current_generation ??
          publication?.current_path ??
          track.publication?.generation ??
          track.publication?.relative_path;
        if (currentDeletionIdentity)
          deletionIdentityByPosition.current.set(
            track.id,
            currentDeletionIdentity,
          );
        // Once the position is read, its snapshot supersedes release detail,
        // including when it reports that publication was removed.
        const publicationSummary = publication
          ? {
              relative_path: publication.current_path,
              validated_at: publication.current_validated_at,
              codec: publication.current_audio?.codec,
              container: publication.current_audio?.container,
              generation: publication.current_generation,
            }
          : track.publication;
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
                  readError: libraryReleaseError(
                    reason,
                    "publicationRefreshFailed",
                  ),
                },
              };
            });
          });
        }
        return (
          <Fragment key={track.id}>
            <article
              aria-labelledby={
                showWorkspace ? libraryPositionTitleId : undefined
              }
              className="grid min-w-0 gap-3"
              hidden={!showWorkspace}
            >
              <LibraryPositionWorkspace
                locale={locale}
                sources={
                  showWorkspace ? (
                    <PositionSources
                      track={track}
                      inspection={{
                        value: inspection?.open
                          ? inspectedSource?.media_variant_id
                          : undefined,
                        onChange: (variantId) =>
                          setTagInspection({
                            releaseId,
                            positionId: track.id,
                            variantId,
                            open: true,
                          }),
                      }}
                    />
                  ) : undefined
                }
                editors={
                  showWorkspace ? (
                    <>
                      <ReleaseCreditList credits={credits} />
                      {track.recording_id ? (
                        <GenreMetadataEditor
                          owner="recording"
                          ownerId={track.recording_id}
                          refreshToken={`${scope.current.generation}:${release.metadata_revision}:${genreContextGeneration}`}
                          onSaved={refreshAfterMetadataSave}
                        />
                      ) : null}
                      {track.recording_id ? (
                        <ScopedLibraryMetadataEditor
                          scope="recording"
                          ownerId={track.recording_id}
                          refreshToken={`${scope.current.generation}:${release.metadata_revision}:${genreContextGeneration}`}
                          onSaved={refreshAfterMetadataSave}
                        />
                      ) : null}
                      <ScopedLibraryMetadataEditor
                        scope="position"
                        ownerId={track.id}
                        releaseId={releaseId}
                        refreshToken={`${scope.current.generation}:${release.metadata_revision}:${genreContextGeneration}`}
                        onSaved={refreshAfterMetadataSave}
                      />
                      {credits.length > 1 && (
                        <label className="block">
                          {text("primaryTrackArtist")}
                          <select
                            aria-label={`${text("primaryTrackArtist")} ${track.displayed_number}`}
                            value={
                              track.primary_artist.selected_artist_id || ""
                            }
                            disabled={
                              !position?.metadata || position.writePending
                            }
                            onChange={(event) =>
                              void selectPrimaryArtist(
                                track.id,
                                event.target.value || null,
                              )
                            }
                          >
                            <option value="">{text("notSelected")}</option>
                            {uniqueArtists(credits).map((artist) => (
                              <option
                                key={artist.artist_id}
                                value={artist.artist_id}
                              >
                                {artist.credit_name || artist.artist_name}
                              </option>
                            ))}
                          </select>
                        </label>
                      )}
                      <label className="block">
                        {text("savedLyrics")}
                        <textarea
                          className="mt-1 block min-h-40 w-full rounded border p-2"
                          value={position?.lyrics ?? ""}
                          onChange={(event) => {
                            lyricsEditGenerations.current.set(
                              track.id,
                              (lyricsEditGenerations.current.get(track.id) ??
                                0) + 1,
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
                          {text("saveLyrics")}
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
                          {text("saveExplicitlyEmpty")}
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
                          {text("applySavedLyrics")}
                        </button>
                      </div>
                    </>
                  ) : undefined
                }
                publication={
                  showWorkspace ? (
                    <>
                      <PublicationCurrentSummaryView
                        locale={locale}
                        published={hasCurrentPublication}
                      />
                      <LibraryDeletionControl
                        target={{
                          type: "publication",
                          releaseId,
                          positionId: track.id,
                        }}
                        releaseTitle={release.title}
                        targetIdentity={deletionIdentityByPosition.current.get(
                          track.id,
                        )}
                        positionLabel={`${track.displayed_number.trim() || track.position}. ${track.title}`}
                        positionLabels={positionLabels}
                        showButton={hasCurrentPublication}
                        onSnapshot={(snapshot) =>
                          handleDeletionSnapshot(
                            "publication",
                            snapshot,
                            renderScope,
                            track.id,
                          )
                        }
                      />
                      {publication?.current_generation &&
                        publication.cover_embedding && (
                          <p>
                            {text("coverEmbeddingOutcome")}{" "}
                            {publication.cover_embedding.outcome === "verified"
                              ? text("verified")
                              : text("unverified")}
                            {publication.cover_embedding.warning_code
                              ? ` · ${publication.cover_embedding.warning_code}`
                              : ""}
                          </p>
                        )}
                      {publication?.initial_lyrics && (
                        <fieldset aria-label={text("lyricsPublicationResult")}>
                          <p>
                            {text("lyricsInCurrentPublication")}{" "}
                            {lyricsOutcomeLabel(
                              publication.initial_lyrics.outcome,
                              locale,
                            )}
                            {` · ${text("lyricsSource")}: ${lyricsSourceLabel(publication.initial_lyrics.source, locale)}`}
                          </p>
                          {publication.initial_lyrics
                            .resolution_warning_code && (
                            <p>
                              {text("lyricsWarningLookup")}{" "}
                              {lyricsWarningLabel(
                                publication.initial_lyrics
                                  .resolution_warning_code,
                                locale,
                              )}
                            </p>
                          )}
                          {publication.initial_lyrics.outcome_warning_code && (
                            <p>
                              {text("lyricsWarningPublication")}{" "}
                              {lyricsWarningLabel(
                                publication.initial_lyrics.outcome_warning_code,
                                locale,
                              )}
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
                                void retryCleanup(
                                  track.id,
                                  warning.original_operation_id,
                                )
                              }
                            >
                              {position?.cleanupRetryPendingIDs?.includes(
                                warning.original_operation_id,
                              )
                                ? text("cleanupRetryRunning")
                                : text("retrySourceCleanup")}
                            </button>
                          )}
                          {warning.active_retry_operation_id &&
                            !finishedOperations.current.has(
                              warning.active_retry_operation_id,
                            ) && <p role="status">{text("cleanupRunning")}</p>}
                        </div>
                      ))}
                      <p>
                        {publication?.active_attempt
                          ? text("operationRunning", {
                              stage: publication.active_attempt.stage,
                              state: publication.active_attempt.state,
                            })
                          : publication?.manual_lyrics?.pending
                            ? text("savedLyricsPending")
                            : text("noLyricsPending")}
                      </p>
                      {publication?.manual_lyrics_receipt && (
                        <p>
                          {text("receipt")}{" "}
                          {publication.manual_lyrics_receipt.desired_state}{" "}
                          {text("confirmedInGeneration")}{" "}
                          {
                            publication.manual_lyrics_receipt
                              .confirmed_generation
                          }
                          ,{text("publishedGeneration")}{" "}
                          {
                            publication.manual_lyrics_receipt
                              .published_generation
                          }
                          {publication.manual_lyrics_receipt.warning_code
                            ? ` · ${text("warning")}: ${publication.manual_lyrics_receipt.warning_code}`
                            : ""}
                          .
                        </p>
                      )}
                      {position?.notice && (
                        <p role="status">{displayText(position.notice)}</p>
                      )}
                      {position?.error && (
                        <p role="alert">{displayText(position.error)}</p>
                      )}
                      {position?.readError && (
                        <div role="alert">
                          {displayText(position.readError)}{" "}
                          <button type="button" onClick={refreshReadState}>
                            {text("refreshData")}
                          </button>
                        </div>
                      )}
                    </>
                  ) : undefined
                }
                actions={
                  !publication?.active_attempt &&
                  !position?.firstPublicationOperationID ? (
                    <FirstPublicationPreview
                      releaseId={releaseId}
                      positionId={track.id}
                      aggregateManaged={!showWorkspace}
                      comparisonOnly={Boolean(
                        publication?.current_path ||
                          publication?.current_generation,
                      )}
                      refreshToken={`${scope.current.generation}:${release.metadata_revision}:${genreContextGeneration}:${publication?.current_generation ?? ""}`}
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
                      {...aggregateCallbacksFor(track.id)}
                    />
                  ) : undefined
                }
                technicalDetails={
                  showWorkspace ? (
                    <LibraryPositionTechnicalDetailsView
                      locale={locale}
                      current={
                        hasCurrentPublication && (
                          <>
                            <PublicationCurrentDetailsView
                              locale={locale}
                              details={{
                                path: publicationSummary?.relative_path,
                                container: publicationSummary?.container,
                                codec: publicationSummary?.codec,
                                observed: publicationSummary?.validated_at
                                  ? {
                                      kind: "verified",
                                      value: publicationSummary.validated_at,
                                    }
                                  : undefined,
                              }}
                            />
                            {publicationSummary?.generation && (
                              <p>
                                {text("generation", {
                                  generation: publicationSummary.generation,
                                })}
                              </p>
                            )}
                            {publication?.current_generation && (
                              <fieldset aria-label={text("actualCoverRegion")}>
                                <p className="wrap-anywhere">
                                  {text("actualCover")}:{" "}
                                  {publication.current_assets?.cover.state ??
                                    text("unknown")}
                                  {publication.current_assets?.cover.sha256
                                    ? ` · SHA-256: ${publication.current_assets.cover.sha256}`
                                    : ""}
                                </p>
                              </fieldset>
                            )}
                            {publication && (
                              <p>
                                {text("lyrics")}{" "}
                                {publication.current_assets?.lyrics.state ??
                                  text("lyricsUnknown")}
                                .
                              </p>
                            )}
                            <PublicationTagSnapshotView
                              locale={locale}
                              tags={currentTags}
                            />
                          </>
                        )
                      }
                    >
                      <PositionIdentity track={track} />
                    </LibraryPositionTechnicalDetailsView>
                  ) : undefined
                }
              />
              {showWorkspace && (
                <LibraryPositionTagsDisclosure
                  locale={locale}
                  open={inspection?.open ?? false}
                  onOpenChange={(open) => {
                    if (open === (inspection?.open ?? false)) return;
                    setTagInspection({
                      releaseId,
                      positionId: track.id,
                      variantId: inspection?.variantId,
                      open,
                    });
                  }}
                >
                  <LibraryPositionTagEvidence
                    key={inspectedSource?.media_variant_id}
                    locale={locale}
                    open={inspection?.open ?? false}
                    source={inspectedSource}
                    current={
                      currentTags
                        ? { state: "saved", tags: currentTags }
                        : position?.readError
                          ? { state: "unavailable" }
                          : !publication
                            ? { state: "loading" }
                            : {
                                state: hasCurrentPublication
                                  ? "unavailable"
                                  : "absent",
                              }
                    }
                  />
                </LibraryPositionTagsDisclosure>
              )}
            </article>
            {!positionId && position?.readError ? (
              <div className="library-release-position-read-error" role="alert">
                {displayText(position.readError)}{" "}
                <button type="button" onClick={refreshReadState}>
                  {text("refreshData")}
                </button>
              </div>
            ) : null}
          </Fragment>
        );
      })}
    </section>
  );
  const credits = selectedTrack?.artists ?? [];
  const artist = credits
    .map(
      (credit, index) =>
        `${credit.credit_name || credit.artist_name}${index < credits.length - 1 ? (credit.join_phrase ?? " ") : ""}`,
    )
    .join("");
  return (
    <section
      aria-labelledby={selectedTrack ? libraryPositionTitleId : undefined}
      className={selectedTrack ? libraryPositionPageClassName : "contents"}
    >
      {selectedTrack && (
        <LibraryPositionHeadingView
          locale={locale}
          displayNumber={
            selectedTrack.displayed_number.trim() ||
            String(selectedTrack.position)
          }
          title={selectedTrack.title}
          artist={artist}
          releaseTitle={release.title}
          releaseHref={`#/library/releases/${encodeURIComponent(release.id)}`}
          libraryHref="#/library"
        />
      )}
      {body}
    </section>
  );
}
