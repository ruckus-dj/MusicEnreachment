import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  navigateHash,
  pushHashState,
  replaceHashState,
  useMeloTroveHashLocation,
} from "../ui/app/hashLocation";
import {
  bindMeloTroveHomeModel,
  bindMeloTroveReleaseModel,
  bindMeloTroveRoute,
  bindMeloTroveScreen,
  type MeloTroveModel,
  type MeloTroveProvider,
  type MeloTroveScreens,
} from "../ui/app/MeloTroveProvider";
import { type MeloTroveRoute, meloTrovePaths } from "../ui/app/MeloTroveRoute";
import type { IncomingCreationModel } from "../ui/features/incoming/IncomingCreationOutlet";
import { incomingPublicationMessages } from "../ui/features/incoming/incomingPublicationMessages";
import { incomingPublicationCounts } from "../ui/features/incoming/incomingPublicationModel";
import { catalogMessage } from "../ui/features/library/catalogMessages";
import type { LibraryCatalogPageState } from "../ui/features/library/LibraryCatalogPageView";
import type { LibraryCatalogResults } from "../ui/features/library/LibraryCatalogResults";
import {
  type LibrarySelectionPreviewPosition,
  type LibrarySelectionPreviewState,
  maxLibraryPreviewPositions,
} from "../ui/features/library/LibrarySelectionPreviewView";
import {
  type LibraryArtistAddress,
  type LibraryPositionFrom,
  type LibraryPositionTab,
  libraryPositionHref,
  libraryReleaseHref,
  parseLibraryArtistAddress,
  parseLibraryPositionAddress,
  parseLibraryReleaseAddress,
} from "../ui/features/library/libraryDetailRoute";
import { libraryReleaseMessage } from "../ui/features/library/libraryReleaseMessages";
import { matchingMessage } from "../ui/features/matching/matchingWorkspaceMessages";
import {
  setupStepHref,
  setupStepIndex,
} from "../ui/features/setup/setupStepRoute";
import {
  parseSourcesRoute,
  type SourcesRoute,
  sourcesHref,
} from "../ui/features/sources/sourcesRoute";
import { demoMeloTroveMessages } from "./demoMeloTroveMessages";
import {
  browseDemoDirectory,
  demoDirectoryStart,
} from "./fixtures/directoryBrowseDemo";
import { createLocalCatalogDemoState } from "./fixtures/localCatalogDemo";
import {
  appendDemoLocalRecord,
  checkDemoReleases,
  demoRecordingWithCredits,
  findDemoArtists,
  findDemoRecordings,
  localReleaseSuggestions,
  newDemoLocalRecord,
  selectDemoRelease,
} from "./fixtures/localReleaseDemo";
import { themePreferenceStore, useI18n } from "./providers/demoPreferences";
import { readyAutomaticIncomingFolders } from "./providers/incoming/automaticIncomingAnalysis";
import {
  DemoMeloTroveIncomingBinder,
  type IncomingWorkspaceAnchorProps,
} from "./providers/incoming/IncomingWorkspaceAnchor";
import {
  type IncomingGroupsRoute,
  incomingGroupMatchingHref,
  incomingGroupsListHref,
  parseIncomingGroupsRoute,
} from "./providers/incoming/incomingGroupsRoute";
import {
  createIncomingState,
  incomingMatchingCount,
  runAutomaticMatching,
} from "./providers/incoming/incomingModel";
import {
  createPublicationWalkthroughReleases,
  demoPrimaryArtistPublication,
  incomingPublicationEditions,
  incomingPublicationQueueHref,
  incomingPublicationReadyHref,
} from "./providers/incoming/incomingPublicationFixtures";
import {
  combineLibraryAlbumEntries,
  libraryArtistPositionIds,
  libraryOffsetSlice,
  libraryPageCount,
  libraryRootHref,
  libraryTrackPositionIds,
  parseLibraryRoute,
  sliceTrackReleasePage,
  useLibraryRouteState,
} from "./providers/library";
import { ArtistScreen } from "./providers/library/ArtistScreen";
import {
  type CatalogArtist,
  catalogData,
  type OriginalRelease,
  type OriginalTrack,
  originalFiles,
  originalProviders,
  originalReleases,
  recordingIdentity,
  recoveryFixture,
} from "./providers/library/catalogData";
import {
  albumSearchText,
  catalogArtistMatchesForAxes,
  missingCatalogGroupsForAxes,
  normalizeCatalogSearch,
  releaseMatchesFilterAxes,
  trackMatchesFilterAxes,
  trackSearchText,
} from "./providers/library/catalogViewModel";
import type { DeletionOperation } from "./providers/library/DeletionRequest";
import { DemoMeloTroveReleaseBinder } from "./providers/library/DemoLibraryReleaseWorkspace";
import {
  DemoLibraryAlbumRowsProvider,
  libraryCatalogArtistResults,
  libraryCatalogTrackResults,
} from "./providers/library/LibraryCatalogRows";
import { DemoLibraryCatalogBinder } from "./providers/library/LibraryCatalogScreen";
import {
  completeManagedLibraryDeletion,
  managedDeletionObservation,
} from "./providers/library/libraryDeletionModel";
import { demoReleasePrimaryArtistId } from "./providers/library/libraryPrimaryArtistDemo";
import { SelectionDock } from "./providers/library/SelectionDock";
import {
  type DemoLibraryProviderRefresh,
  useDemoLibraryProviderRefresh,
} from "./providers/library/useDemoLibraryProviderRefresh";
import type { DemoReleaseCoverState } from "./providers/library/useReleaseCoverEditorDemoModel";
import type { ReleaseOverviewScreenProps } from "./providers/library/useReleaseOverviewDemoModel";
import type { TrackScreenProps } from "./providers/library/useTrackScreenDemoProvider";
import { DemoLocalCatalogBinder } from "./providers/local-catalog";
import { releaseDraftToRecord } from "./providers/local-catalog/localCatalogModel";
import { localProviderReleases } from "./providers/local-catalog/localProviderModel";
import {
  DemoLocalReleaseGenreCatalogProvider,
  DemoLocalReleaseGenreLoadProvider,
} from "./providers/local-release/DemoLocalReleaseGenreCatalogProvider";
import {
  commitMatchingReceiptPublication,
  type MatchingReceiptPublicationBatch,
  type MatchingReceiptPublicationBatchEntry,
  type MatchingReceiptPublicationJobPlan,
  type MatchingReceiptPublicationQueuePort,
  type MatchingReceiptPublicationSettlement,
  type MatchingReceiptPublicationSubmission,
  settleMatchingReceiptPublicationJob,
} from "./providers/matching/matchingReceiptPublicationQueue";
import { DemoOperationsBinder } from "./providers/operations/OperationsScreen";
import {
  type DemoPublicationJob,
  useOperationsDemoProvider,
} from "./providers/operations/useOperationsDemoProvider";
import {
  DemoPositionPublication,
  type DemoPositionPublicationSource,
} from "./providers/publication/ContextPublicationPreview";
import {
  type ContextualPublicationPreparation,
  commitContextualPublication,
  settleContextualPublication,
} from "./providers/publication/contextualPublicationJob";
import {
  type DemoPublicationCatalog,
  type DemoPublicationRow,
  planDemoPublication,
  settleDemoPublication,
} from "./providers/publication/libraryPublicationDemo";
import type { ManagedPathObservation } from "./providers/publication/managedPublicationDeletionModel";
import {
  fixturePublicationDestination,
  type PublicationDestination,
} from "./providers/publication/publicationOutput";
import { defaultPublicationTemplate } from "./providers/settings/publicationTemplateDemo";
import {
  DemoSettingsBinder,
  initialDemoSettings,
} from "./providers/settings/SettingsScreen";
import { DemoMeloTroveSetupBinder } from "./providers/setup/SetupScreen";
import {
  getSetupDraftStorage,
  mergeSetupDraft,
  readSetupDraft,
  restoreSetupSettings,
  type SetupDraftSnapshot,
  setupSettingsDraft,
  writeSetupDraft,
} from "./providers/setup/setupDraft";
import { useSetupToolDownloadsDemoProvider } from "./providers/setup/useSetupToolDownloadsDemoProvider";
import {
  DemoMeloTroveSourcesBinder,
  initialSourceRoots,
} from "./providers/sources/SourcesScreen";
import { useSourceArtifactCleanupDemoExecution } from "./providers/sources/sourceArtifactCleanupDemoProvider";
import { removeRootFromInventory } from "./providers/sources/sourceInventory";
import {
  initialSourceProcessingState,
  resetSourceProcessingQueue,
  sourceProcessingLocks,
} from "./providers/sources/sourceProcessingModel";
import { useSourceScanDemoProvider } from "./providers/sources/useSourceScanDemoProvider";

type Track = (typeof catalogData.releases)[number]["tracks"][number];
type PublicationJob = DemoPublicationJob &
  (
    | { kind: "aggregate"; plan: DemoPublicationRow }
    | { kind: "contextual"; plan: ContextualPublicationPreparation }
    | { kind: "receipt"; plan: MatchingReceiptPublicationJobPlan }
  );

const DemoProviderRefreshContext = createContext<
  DemoLibraryProviderRefresh | undefined
>(undefined);

/** Test-only injection point for provider refresh results. */
export function DemoMeloTroveProviderRefresh({
  value,
  children,
}: {
  value: DemoLibraryProviderRefresh | undefined;
  children: ReactNode;
}) {
  return (
    <DemoProviderRefreshContext.Provider value={value}>
      {children}
    </DemoProviderRefreshContext.Provider>
  );
}

/**
 * One route-independent demo model. It stays mounted above every route swap so
 * admitted downloads, publications, scans and drafts survive navigation.
 */
export function useDemoMeloTroveModel(route: MeloTroveRoute): MeloTroveModel {
  const providerRefresh = useContext(DemoProviderRefreshContext);
  return useDemoMeloTroveState(route, providerRefresh);
}

function useDemoMeloTroveState(
  route: MeloTroveRoute,
  providerRefresh: DemoLibraryProviderRefresh | undefined,
): MeloTroveModel {
  const { locale } = useI18n();
  const demoCopy = demoMeloTroveMessages[locale];
  const releaseProviderRefresh = useDemoLibraryProviderRefresh(
    providerRefresh,
    locale,
  );
  const msg = (
    key: Parameters<typeof catalogMessage>[1],
    values?: Record<string, string | number>,
  ) => catalogMessage(locale, key, values);
  const [localProvider, setLocalProvider] = useState(
    createLocalCatalogDemoState,
  );
  const [incoming, setIncoming] = useState(createIncomingState);
  const [initialSetupDraft] = useState<SetupDraftSnapshot | null>(() =>
    readSetupDraft(getSetupDraftStorage()),
  );
  const [initialSetupSettings] = useState(() =>
    restoreSetupSettings(initialDemoSettings, initialSetupDraft),
  );
  const setupDraftRef = useRef<SetupDraftSnapshot | null>(initialSetupDraft);
  const [storedSetupStep, setStoredSetupStep] = useState(
    () => initialSetupDraft?.step ?? 0,
  );
  const persistSetupDraft = useCallback(
    (patch: Partial<Omit<SetupDraftSnapshot, "version">>) => {
      const next = mergeSetupDraft(setupDraftRef.current, patch);
      setupDraftRef.current = next;
      writeSetupDraft(getSetupDraftStorage(), next);
    },
    [],
  );
  // Host-owned so admitted downloads and moves settle across route changes.
  const {
    settings,
    setSettings,
    activity: toolDownloads,
    downloads: toolDownloadSnapshots,
    retry: retryToolDownload,
    dismiss: dismissToolDownload,
    moves: toolMoveSnapshots,
    startMove: startToolMove,
    retryMove: retryToolMove,
    dismissMove: dismissToolMove,
  } = useSetupToolDownloadsDemoProvider(initialSetupSettings);
  const publicationDestination: PublicationDestination = {
    outputDirectory:
      settings.currentOutput ?? fixturePublicationDestination.outputDirectory,
    format: settings.publicationFormat || fixturePublicationDestination.format,
  };
  const loadGenres = async () => {
    setSettings((current) => ({ ...current, genresLoaded: true }));
  };
  const routeSetupStep =
    route.kind === "setup" ? setupStepIndex(route.hash) : undefined;
  const setSetupStep = useCallback(
    (next: number, options?: { replace?: boolean }) => {
      setStoredSetupStep(next);
      persistSetupDraft({ step: next });
      const href = setupStepHref(next);
      if (options?.replace) replaceHashState(href);
      else pushHashState(href);
    },
    [persistSetupDraft],
  );
  const recordSetupGenreCatalog = useCallback(
    (endpoint: string) => {
      persistSetupDraft({ genreCatalogEndpoint: endpoint });
    },
    [persistSetupDraft],
  );
  useEffect(() => {
    if (route.kind !== "setup" || routeSetupStep === undefined) return;
    setStoredSetupStep(routeSetupStep);
    persistSetupDraft({ step: routeSetupStep });
  }, [route.kind, routeSetupStep, persistSetupDraft]);
  useEffect(() => {
    // A running download can settle after leaving Setup; keep the narrow
    // Setup evidence current, but do not create a draft outside Setup.
    if (route.kind !== "setup" && setupDraftRef.current === null) return;
    persistSetupDraft({ settings: setupSettingsDraft(settings) });
  }, [persistSetupDraft, route.kind, settings]);
  const [files, setFiles] = useState(() => structuredClone(originalFiles));
  const [sourceRoots, setSourceRoots] = useState(() =>
    structuredClone(initialSourceRoots),
  );
  const [sourceProcessing, setSourceProcessing] = useState(() =>
    structuredClone(initialSourceProcessingState),
  );
  const sourceScans = useSourceScanDemoProvider({
    files,
    workDirectory: settings.workDirectory,
    updateRoots: setSourceRoots,
    updateProcessing: setSourceProcessing,
  });
  // Host-owned so an in-flight cleanup survives route changes.
  const sourceCleanupExecution = useSourceArtifactCleanupDemoExecution({
    locale,
    processing: sourceProcessing,
    updateProcessing: setSourceProcessing,
  });
  const hasAudio = (track: {
    published: unknown;
    sourceIds?: string[];
    sourceId?: string | null;
  }) =>
    Boolean(track.published) ||
    [
      ...new Set([
        ...(track.sourceIds ?? []),
        ...(track.sourceId ? [track.sourceId] : []),
      ]),
    ].some((id) => files[id]?.available && files[id]?.locations?.length);
  const {
    view,
    query,
    filter,
    page,
    offset,
    publication,
    availability,
    searchInput,
    isLibraryRoute,
    setView,
    setFilter,
    setPublication,
    setAvailability,
    setPage,
    setSearch,
    reset: resetLibrarySearch,
  } = useLibraryRouteState(route.hash);
  const [releases, renderReleases] = useState(
    createPublicationWalkthroughReleases,
  );
  const releasesRef = useRef(releases);
  const setReleases = (
    update:
      | OriginalRelease[]
      | ((current: OriginalRelease[]) => OriginalRelease[]),
  ) => {
    const next =
      typeof update === "function" ? update(releasesRef.current) : update;
    releasesRef.current = next;
    renderReleases(next);
  };
  const [releaseCovers, setReleaseCovers] = useState<
    Record<string, DemoReleaseCoverState>
  >({});
  const [artists, setArtists] = useState<CatalogArtist[]>(() =>
    structuredClone(catalogData.artists),
  );
  const publicationEditions = incomingPublicationEditions(releases, artists);
  const incomingCount =
    incomingMatchingCount(incoming.groups) +
    incomingPublicationCounts(publicationEditions).total;
  const providers = [
    ...originalProviders,
    ...localProviderReleases(localProvider),
  ];
  const providerArtists = [
    ...artists,
    ...localProvider.items.artists
      .filter((artist) => artist.provider === "Local")
      .map((artist) => ({
        id: artist.id,
        name: artist.name ?? "",
        sortName: artist.sortName,
        genre: msg("localCatalog"),
        color: "slate",
      })),
  ];
  const setImportedReleases = (nextReleases: OriginalRelease[]) => {
    const localProviderKeys = new Set(
      providers
        .filter((release) => release.provider === "Local")
        .map((release) => release.providerId ?? release.id),
    );
    const importedArtistIds = new Set(
      nextReleases
        .filter(
          (release) =>
            release.providerId && localProviderKeys.has(release.providerId),
        )
        .flatMap((release) => [
          release.artistId,
          ...(release.credits ?? []).map((credit) => credit.artistId ?? null),
          ...release.tracks.flatMap((track) => [
            track.artistId,
            ...(track.recordingCredits ?? []).map((credit) => credit.artistId),
          ]),
        ])
        .filter((id): id is string => Boolean(id)),
    );
    setArtists((current) => {
      const known = new Set(current.map((artist) => artist.id));
      return [
        ...current,
        ...providerArtists.filter(
          (artist) => importedArtistIds.has(artist.id) && !known.has(artist.id),
        ),
      ];
    });
    setReleases(nextReleases);
  };
  const observedAnalysis = useRef(new Map<string, string>());
  const readyIncomingFolders = useMemo(
    () =>
      readyAutomaticIncomingFolders({
        files,
        roots: sourceRoots,
        processing: sourceProcessing,
      }),
    [files, sourceRoots, sourceProcessing],
  );
  useEffect(() => {
    const completed: ReadonlySet<string>[] = [];
    for (const folder of readyIncomingFolders) {
      if (observedAnalysis.current.get(folder.key) === folder.signature)
        continue;
      observedAnalysis.current.set(folder.key, folder.signature);
      completed.push(folder.fileIds);
    }
    if (completed.length === 0) return;
    const nextIncoming = structuredClone(incoming);
    const nextReleases = structuredClone(releases);
    const minimumScore = Number(settings.matchingPairThreshold ?? "0.70");
    const result = runAutomaticMatching(
      nextIncoming,
      files,
      nextReleases,
      completed,
      minimumScore,
      settings.publicationAutoCompleteAlbums === true,
      providers,
      providerArtists,
      publicationDestination,
    );
    if (!result.publications && !result.links && !result.retired) return;
    if (result.publications) {
      nextIncoming.notice = "";
      nextIncoming.noticeDescriptor = null;
      nextIncoming.uiNotice = {
        namespace: "incoming",
        id: "automaticPublished",
        params: { count: result.publications },
      };
    }
    setIncoming(nextIncoming);
    setImportedReleases(nextReleases);
  }, [
    readyIncomingFolders,
    files,
    incoming,
    releases,
    settings.matchingPairThreshold,
    settings.publicationAutoCompleteAlbums,
    providers,
    providerArtists,
    publicationDestination,
    setImportedReleases,
  ]);
  const fixture = {
    ...catalogData,
    artists,
    releases: releases.map((release) => ({
      ...release,
      tracks: release.tracks.map((track) => ({
        ...track,
        sourceIds: track.sourceIds ?? [],
        published: Boolean(track.published),
      })),
    })),
  };
  const [selectionPreview, setSelectionPreview] = useState<{
    positions: LibrarySelectionPreviewPosition[];
    state: LibrarySelectionPreviewState;
  } | null>(null);
  useEffect(() => {
    setSelectionPreview(null);
  }, [route.hash]);
  const [deletions, setDeletions] = useState<DeletionOperation[]>([]);
  const [publicationJobs, setPublicationJobs] = useState<PublicationJob[]>([]);
  const [receiptPublicationBatches, setReceiptPublicationBatches] = useState<
    MatchingReceiptPublicationBatch[]
  >([]);
  // Synchronous mirror: two confirmations in one render must see the first
  // confirmation's jobs before React has published scheduler state.
  const publicationJobsRef = useRef<PublicationJob[]>([]);
  const replacePublicationJobs = (next: PublicationJob[]) => {
    publicationJobsRef.current = next;
    setPublicationJobs(next);
  };
  const publicationCatalog: DemoPublicationCatalog = {
    releases,
    files,
    artists,
    destination: publicationDestination,
    hasCustomTemplate: Boolean(
      settings.publicationTemplate &&
        settings.publicationTemplate !== defaultPublicationTemplate,
    ),
    firstPublicationBlocker: ({ release_id }) => {
      const release = releases.find(({ id }) => id === release_id);
      return release?.id === demoPrimaryArtistPublication.releaseId &&
        !demoReleasePrimaryArtistId(release, artists)
        ? "release_primary_artist_required"
        : undefined;
    },
    inventoryVersion: incoming.inventoryVersion,
  };
  const publicationPending = (jobId: string) => {
    const state = operationsProvider.publicationStates?.get(jobId);
    return !state || state === "queued" || state === "running";
  };
  const addPublicationJobs = (jobs: readonly PublicationJob[]) => {
    const current = publicationJobsRef.current;
    const next = [...current];
    const accepted: PublicationJob[] = [];
    for (const job of jobs) {
      if (
        next.some(
          (item) =>
            item.releaseId === job.releaseId &&
            item.positionId === job.positionId &&
            publicationPending(item.id),
        )
      )
        continue;
      next.push(job);
      accepted.push(job);
    }
    if (accepted.length) replacePublicationJobs(next);
    return accepted;
  };
  const queuePublications = (plans: readonly DemoPublicationRow[]) => {
    const jobs = plans.flatMap((plan) => {
      const release = releases.find(({ id }) => id === plan.release_id);
      const track = release?.tracks.find(({ id }) => id === plan.position_id);
      return track
        ? [
            {
              id: `publication-${crypto.randomUUID()}`,
              kind: "aggregate" as const,
              releaseId: plan.release_id,
              positionId: plan.position_id,
              title: track.title,
              createdAt: Date.now(),
              plan,
            },
          ]
        : [];
    });
    addPublicationJobs(jobs);
  };
  const submitReceiptPublication = (
    submission: MatchingReceiptPublicationSubmission,
  ) => {
    const alreadyAdmitted = new Set(
      publicationJobsRef.current.flatMap((job) =>
        job.kind === "receipt" &&
        job.plan.receipt.id === submission.receipt.id &&
        publicationPending(job.id)
          ? [job.positionId]
          : [],
      ),
    );
    const submitted = submission.entries.filter(
      (entry) => !alreadyAdmitted.has(entry.positionId),
    );
    if (!submitted.length) return;
    const release = releasesRef.current.find(
      (item) => item.id === submission.receipt.releaseId,
    );
    const jobs: PublicationJob[] = [];
    const rejected = new Map<string, MatchingReceiptPublicationBatchEntry>();
    for (const entry of submitted) {
      const track = release?.tracks.find(
        (item) => String(item.id) === entry.positionId,
      );
      const plan = {
        receipt: submission.receipt,
        selections: submission.selections,
        positionId: entry.positionId,
        preview: entry.preview,
      };
      const proof = settleMatchingReceiptPublicationJob(
        {
          ...publicationCatalog,
          releases: releasesRef.current,
          artists: providerArtists,
          savedReceipts: incoming.receipts,
        },
        plan,
      );
      if (!proof.applied || !track) {
        rejected.set(entry.positionId, {
          trackId: entry.positionId,
          jobId: null,
          preview: entry.preview,
          notice: proof.applied
            ? "matchingReceipt.apply.blocked"
            : proof.notice,
        });
        continue;
      }
      jobs.push({
        kind: "receipt",
        id: `publication-${crypto.randomUUID()}`,
        releaseId: submission.receipt.releaseId,
        positionId: entry.positionId,
        title: track.title,
        createdAt: Date.now(),
        plan,
      });
    }
    const accepted = addPublicationJobs(jobs);
    const acceptedIds = new Set(accepted.map((job) => job.id));
    const entries: MatchingReceiptPublicationBatchEntry[] = submitted.map(
      ({ positionId, preview }) => {
        const job = jobs.find(
          ({ positionId: queued }) => queued === positionId,
        );
        return (
          rejected.get(positionId) ??
          (job && acceptedIds.has(job.id)
            ? { trackId: positionId, jobId: job.id, preview }
            : {
                trackId: positionId,
                jobId: null,
                preview,
                notice: "matchingReceipt.apply.blocked",
              })
        );
      },
    );
    if (!entries.length) return;
    const batch = {
      id: `receipt-publication-${crypto.randomUUID()}`,
      receiptId: submission.receipt.id,
      entries,
      results: {},
    };
    setReceiptPublicationBatches((current) => [
      ...current.filter(
        (batch) =>
          batch.receiptId !== submission.receipt.id ||
          batch.entries.some(
            (entry) => entry.jobId && publicationPending(entry.jobId),
          ),
      ),
      batch,
    ]);
  };
  const [residualDeletions, setResidualDeletions] = useState(
    () => new Map<DeletionOperation, DeletionOperation>(),
  );
  const queueDeletion = (operation: DeletionOperation) => {
    setDeletions((previous) => [...previous, operation]);
    navigateHash(meloTrovePaths.operations);
  };
  const recordReceiptPublicationResult = (
    jobId: string,
    settlement: MatchingReceiptPublicationSettlement,
  ) => {
    setReceiptPublicationBatches((current) =>
      current.map((batch) => {
        const entry = batch.entries.find((item) => item.jobId === jobId);
        if (!entry) return batch;
        return {
          ...batch,
          results: {
            ...batch.results,
            [entry.trackId]: settlement.applied
              ? { status: "succeeded" as const }
              : {
                  status: "failed" as const,
                  notice: settlement.notice,
                },
          },
        };
      }),
    );
  };
  const operationsProvider = useOperationsDemoProvider({
    deletions,
    publicationJobs,
    settlePublication: (job) => {
      const queued = publicationJobsRef.current.find(
        (item) => item.id === job.id,
      );
      if (!queued) return false;
      if (queued.kind === "contextual") {
        const result = settleContextualPublication(
          {
            ...publicationCatalog,
            releases: releasesRef.current,
            providers,
            artists: providerArtists,
          },
          queued.plan,
        );
        if (!result) return false;
        setReleases((current) =>
          commitContextualPublication(current, queued.plan, result),
        );
        return true;
      }
      if (queued.kind === "receipt") {
        const settlement = settleMatchingReceiptPublicationJob(
          {
            releases: releasesRef.current,
            files: publicationCatalog.files,
            artists: providerArtists,
            destination: publicationCatalog.destination,
            savedReceipts: incoming.receipts,
          },
          queued.plan,
        );
        recordReceiptPublicationResult(queued.id, settlement);
        if (!settlement.applied) return false;
        setReleases((current) =>
          commitMatchingReceiptPublication(current, settlement),
        );
        return true;
      }
      const result = settleDemoPublication(
        { ...publicationCatalog, releases: releasesRef.current },
        queued.plan,
      );
      if (!result) return false;
      setReleases((current) =>
        current.map((release) =>
          release.id !== job.releaseId
            ? release
            : {
                ...release,
                tracks: release.tracks.map((track) =>
                  track.id !== job.positionId
                    ? track
                    : {
                        ...track,
                        published: result,
                        metadataBaseline: result.tags,
                        metadataBaselineExtras: {
                          coverHash: result.coverHash ?? null,
                          lyrics: result.lyrics ?? null,
                        },
                      },
                ),
              },
        ),
      );
      return true;
    },
    settleDeletion: (operation) => completeDeletion(operation),
    recover: (snapshot) => {
      const currentOutput = settings.currentOutput ?? "/srv/music";
      if (
        !snapshot.path.startsWith(`${currentOutput.replace(/\/$/, "")}/`) ||
        !releases.some((release) =>
          release.tracks.some((track) => track.id === recoveryFixture.trackId),
        )
      )
        return false;
      setReleases((previous) =>
        previous.map((release) => ({
          ...release,
          tracks: release.tracks.map((track) =>
            track.id === recoveryFixture.trackId
              ? {
                  ...track,
                  published: structuredClone(snapshot),
                  recovered: true,
                  metadataBaseline: structuredClone(snapshot.tags),
                  metadataBaselineExtras: {
                    coverHash: snapshot.coverHash ?? null,
                    lyrics: snapshot.lyrics ?? null,
                  },
                }
              : track,
          ),
        })),
      );
      return true;
    },
  });
  const receiptPublicationPort: MatchingReceiptPublicationQueuePort = {
    batches: receiptPublicationBatches,
    states: operationsProvider.publicationStates ?? new Map(),
    submit: submitReceiptPublication,
  };
  const completeDeletion = (operation: DeletionOperation) => {
    const confirmedOperation = residualDeletions.get(operation) ?? operation;
    const currentReleases = releasesRef.current;
    const tracks = currentReleases.flatMap((release) => release.tracks);
    const selectedTracks = new Set(confirmedOperation.libraryTrackIds);
    // Fixtures supply observations, not real unlink or database commit results.
    const paths = Object.fromEntries(
      confirmedOperation.paths.map((path) => {
        const owners = tracks.filter((track) => track.published?.path === path);
        const observation = owners[0]?.observedFile;
        let state: ManagedPathObservation = "unknown";
        if (owners.some((track) => !selectedTracks.has(track.id)))
          state = "replaced";
        else if (owners.length) state = managedDeletionObservation(observation);
        return [path, { state, deleteSucceeded: state === "present" }];
      }),
    );
    const next = completeManagedLibraryDeletion(
      confirmedOperation,
      currentReleases,
      artists,
      {
        paths,
        metadataCommitSucceeded: true,
      },
    );
    if (next.status === "stale") return next.status;
    setReleases(next.releases);
    setArtists(next.artists);
    if (next.status !== "completed") {
      setResidualDeletions((previous) =>
        new Map(previous).set(operation, next.residualOperation),
      );
      if (
        next.residualOperation.paths.length < confirmedOperation.paths.length
      ) {
        setIncoming((previous) => ({
          ...previous,
          plan: null,
          inventoryVersion: previous.inventoryVersion + 1,
        }));
      }
      return next.status;
    }
    setResidualDeletions((previous) => {
      const updated = new Map(previous);
      updated.delete(operation);
      return updated;
    });
    setDeletions((previous) => previous.filter((item) => item !== operation));
    setIncoming((previous) => ({
      ...previous,
      plan: null,
      inventoryVersion: previous.inventoryVersion + 1,
    }));
    setSelected(
      (previous) =>
        new Set(
          [...previous].filter((id) =>
            next.releases.some((release) =>
              release.tracks.some((track) => track.id === id),
            ),
          ),
        ),
    );
    return next.status;
  };
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [expanded, setExpanded] = useState<string | null>(null);
  const [editions, setEditions] = useState<Record<string, string>>({});
  const normalized = normalizeCatalogSearch(query);
  const filterAxes = { publication, availability };
  const matchesTrack = (track: Track) =>
    trackMatchesFilterAxes(track, filterAxes, hasAudio);
  const matchesRelease = (tracks: Track[]) =>
    releaseMatchesFilterAxes(tracks, filterAxes, hasAudio);
  const missingGroups = missingCatalogGroupsForAxes(
    fixture.absentGroups,
    fixture.releases,
    fixture.artists,
    query,
    filterAxes,
  );
  const unrepresentedMissingGroups = missingCatalogGroupsForAxes(
    fixture.absentGroups,
    fixture.releases,
    fixture.artists,
    "",
    { publication: "all", availability: "all" },
  );
  const visibleArtists = fixture.artists.filter((artist) =>
    catalogArtistMatchesForAxes(
      artist,
      fixture.releases,
      missingGroups,
      query,
      filterAxes,
      (track) => hasAudio(track as Track),
    ),
  );
  const visibleReleaseIds = new Set(
    fixture.releases
      .filter(
        (release) =>
          normalizeCatalogSearch(
            albumSearchText(release, fixture.artists),
          ).includes(normalized) && matchesRelease(release.tracks),
      )
      .map((release) => release.id),
  );
  const orderedVisibleReleases = releases.filter((release) =>
    visibleReleaseIds.has(release.id),
  );
  const visibleTrackIds = new Set(
    fixture.releases.flatMap((release) =>
      release.tracks
        .filter(
          (track) =>
            normalizeCatalogSearch(
              trackSearchText(track, release, fixture.artists),
            ).includes(normalized) && matchesTrack(track),
        )
        .map((track) => track.id),
    ),
  );
  const visibleTrackRefs = releases.flatMap((release) =>
    release.tracks
      .filter((track) => visibleTrackIds.has(track.id))
      .map((track) => ({ release, track })),
  );
  const albumEntries = combineLibraryAlbumEntries(
    orderedVisibleReleases,
    missingGroups,
  );
  const libraryTotalItems =
    view === "artists"
      ? visibleArtists.length
      : view === "albums"
        ? albumEntries.length
        : visibleTrackRefs.length;
  const totalPages = libraryPageCount(libraryTotalItems);
  const currentPage = Math.min(page, totalPages);
  const pageArtists = libraryOffsetSlice(visibleArtists, offset);
  const pageAlbumEntries = libraryOffsetSlice(albumEntries, offset);
  const pageAlbumReleases = pageAlbumEntries.flatMap((entry) =>
    entry.kind === "release" ? [entry.release] : [],
  );
  const pageTracks = libraryOffsetSlice(visibleTrackRefs, offset);
  const pageTrackReleases = sliceTrackReleasePage(releases, pageTracks);
  const visiblePositionIds = new Set(
    view === "artists"
      ? libraryArtistPositionIds(pageArtists, releases)
      : view === "albums"
        ? pageAlbumReleases.flatMap((release) =>
            release.tracks.map((track) => track.id),
          )
        : libraryTrackPositionIds(pageTracks),
  );
  useEffect(() => {
    if (!isLibraryRoute) return;
    // Wait for the hook to adopt the whole route before clamping its offset.
    const adopted = parseLibraryRoute(route.hash);
    if (
      adopted.view !== view ||
      adopted.query !== query ||
      adopted.offset !== offset ||
      adopted.publication !== publication ||
      adopted.availability !== availability
    )
      return;
    if (page > totalPages) setPage(totalPages, totalPages);
  }, [
    availability,
    isLibraryRoute,
    offset,
    page,
    publication,
    query,
    route.hash,
    setPage,
    totalPages,
    view,
  ]);
  const update = (ids: string[], checked: boolean) => {
    setSelected((previous) => {
      const next = new Set(previous);
      for (const id of ids) {
        if (checked) next.add(id);
        else next.delete(id);
      }
      return next;
    });
  };
  const openSelectionPreview = (ids: string[]) => {
    const selectedIds = new Set(ids);
    const positions = releases.flatMap((release) =>
      release.tracks
        .filter((track) => selectedIds.has(track.id))
        .map((track) => ({
          release_id: release.id,
          position_id: track.id,
          title: track.title,
          release_title: release.title,
        })),
    );
    setSelectionPreview({
      positions,
      state:
        positions.length > maxLibraryPreviewPositions
          ? { status: "limit" }
          : {
              status: "ready",
              rows: planDemoPublication(publicationCatalog, positions),
            },
    });
  };
  const selectionPreviewView = selectionPreview
    ? {
        locale,
        positions: selectionPreview.positions,
        state: selectionPreview.state,
        onClose: () => setSelectionPreview(null),
      }
    : undefined;
  const libraryArtistScreen = (address: LibraryArtistAddress) => {
    const artistId = address.artistId;
    return bindMeloTroveRoute(
      ArtistScreen,
      {
        artistId,
        artists,
        releases,
        files,
        genresLoaded: settings.genresLoaded,
        onLoadGenres: loadGenres,
        selected,
        onSelect: update,
        onQueueDeletion: queueDeletion,
        onUpdateArtist: (updated) =>
          setArtists((current) =>
            current.map((artist) =>
              artist.id === updated.id ? updated : artist,
            ),
          ),
      },
      {
        key: artistId,
        selection: {
          dock: (
            <SelectionDock
              selected={selected}
              releases={releases}
              onSelect={update}
              onPreview={openSelectionPreview}
              onQueueDeletion={queueDeletion}
            />
          ),
          preview: selectionPreviewView,
        },
      },
    );
  };
  const libraryReleaseScreen = (address: {
    releaseId: string;
    positionId?: string;
    tab?: LibraryPositionTab;
    from?: LibraryPositionFrom;
  }) => {
    const release = releases.find((item) => item.id === address.releaseId);
    if (!release)
      return bindMeloTroveReleaseModel({
        locale,
        selection: { kind: "unavailable" },
        overview: { libraryHref: libraryRootHref(), heading: null },
        positions: [],
      });
    const track = address.positionId
      ? release.tracks.find((item) => item.id === address.positionId)
      : undefined;
    const fromPublicationQueue = address.from === "publication-queue";
    const walkthrough = release.id === demoPrimaryArtistPublication.releaseId;
    const artistReady =
      !walkthrough || Boolean(demoReleasePrimaryArtistId(release, artists));
    const isBlockedPosition = (item: OriginalRelease["tracks"][number]) =>
      !item.published && !artistReady;
    const primaryArtistResolution =
      walkthrough &&
      !address.positionId &&
      new URLSearchParams(route.hash.split("?", 2)[1]).get("resolve") ===
        "primary-artist"
        ? {
            ready: artistReady,
            trackHref: demoPrimaryArtistPublication.trackHref,
            backHref: artistReady
              ? incomingPublicationReadyHref
              : incomingPublicationQueueHref,
          }
        : undefined;
    const missingPosition =
      address.positionId && !track
        ? {
            message: libraryReleaseMessage(locale, "positionNotFound"),
            releaseHref: libraryReleaseHref(release.id),
            releaseLabel: libraryReleaseMessage(locale, "fullRelease"),
          }
        : undefined;
    const overview: ReleaseOverviewScreenProps = {
      release,
      primaryArtistResolution,
      artists,
      files,
      onUpdateRelease: (updated) =>
        setReleases((current) =>
          current.map((item) => (item.id === updated.id ? updated : item)),
        ),
      onQueueDeletion: queueDeletion,
      publication: {
        catalog: publicationCatalog,
        pending: publicationJobs.filter((job) => publicationPending(job.id)),
        onQueue: queuePublications,
      },
      genresLoaded: settings.genresLoaded,
      onLoadGenres: loadGenres,
      coverState: releaseCovers[release.id],
      onCoverStateChange: (state) =>
        setReleaseCovers((current) => ({
          ...current,
          [release.id]: state,
        })),
    };
    const lastPositionPublication = (positionId: string) =>
      publicationJobs
        .filter(
          (job) =>
            job.releaseId === release.id && job.positionId === positionId,
        )
        .at(-1);
    const contextualPublications = new Map<
      string,
      DemoPositionPublicationSource
    >();
    for (const item of release.tracks) {
      if (isBlockedPosition(item)) continue;
      const last = lastPositionPublication(item.id);
      if (last && publicationPending(last.id)) continue;
      contextualPublications.set(item.id, {
        key: `${release.id}:${item.id}`,
        Component: DemoPositionPublication,
        props: {
          destination: publicationDestination,
          release,
          positionId: item.id,
          state: incoming,
          files,
          releases,
          providers,
          artists: providerArtists,
          hasCustomTemplate: Boolean(publicationCatalog.hasCustomTemplate),
          onQueue: (plan) => {
            addPublicationJobs([
              {
                kind: "contextual",
                id: `publication-${crypto.randomUUID()}`,
                releaseId: release.id,
                positionId: item.id,
                title: item.title,
                createdAt: Date.now(),
                plan,
              },
            ]);
          },
        },
      });
    }
    let position: TrackScreenProps | null = null;
    if (track) {
      const identity = recordingIdentity(track);
      const lastPublication = lastPositionPublication(track.id);
      const lastPublicationState = lastPublication
        ? operationsProvider.publicationStates?.get(lastPublication.id)
        : undefined;
      position = {
        files,
        providers,
        artists,
        publicationPreviewNotice:
          lastPublicationState === "succeeded"
            ? libraryReleaseMessage(locale, "operationCompleted")
            : lastPublicationState === "failed"
              ? libraryReleaseMessage(locale, "operationEnded", {
                  state: "failed",
                })
              : undefined,
        onQueueDeletion: queueDeletion,
        release,
        track,
        publicationIssue: isBlockedPosition(track)
          ? {
              message: incomingPublicationMessages[locale].reasonLabel(
                demoPrimaryArtistPublication.reason,
              ),
              action: {
                label:
                  incomingPublicationMessages[locale].nextStepLabel(
                    "primaryArtist",
                  ),
                href: demoPrimaryArtistPublication.resolveHref,
              },
              backHref: fromPublicationQueue
                ? incomingPublicationQueueHref
                : undefined,
            }
          : undefined,
        queueBackHref: fromPublicationQueue
          ? artistReady
            ? incomingPublicationReadyHref
            : incomingPublicationQueueHref
          : undefined,
        tab: address.tab ?? "overview",
        onTabChange: (tab) =>
          replaceHashState(
            libraryPositionHref(release.id, track.id, {
              tab: tab === "overview" ? undefined : tab,
              from: address.from,
            }),
          ),
        genresLoaded: settings.genresLoaded,
        onLoadGenres: loadGenres,
        onRecordingChange: (updateTrack) => {
          setReleases((previous) =>
            previous.map((item) => ({
              ...item,
              tracks: item.tracks.map((candidate) =>
                (identity && recordingIdentity(candidate) === identity) ||
                (!identity &&
                  item.id === release.id &&
                  candidate.id === track.id)
                  ? updateTrack(candidate)
                  : candidate,
              ),
            })),
          );
        },
        onMutate: (updateTrack) => {
          setReleases((previous) =>
            previous.map((item) =>
              item.id === release.id
                ? {
                    ...item,
                    tracks: item.tracks.map((candidate) =>
                      candidate.id === track.id
                        ? updateTrack(candidate)
                        : candidate,
                    ),
                  }
                : item,
            ),
          );
        },
      };
    }
    return bindMeloTroveScreen(DemoMeloTroveReleaseBinder, {
      overview,
      position,
      notFound: missingPosition,
      contextualPublications,
      providerRefresh: releaseProviderRefresh(release.id),
    });
  };
  const ownCatalogScreen = () =>
    bindMeloTroveRoute(DemoLocalCatalogBinder, {
      state: localProvider,
      onLoadGenres: loadGenres,
      onState: setLocalProvider,
      genresLoaded: settings.genresLoaded,
    });
  const incomingScreen = (incomingRoute: IncomingGroupsRoute) => {
    const matchingRoute =
      incomingRoute.view === "matching" ? incomingRoute : undefined;
    const groupId = matchingRoute?.groupId;
    const creatingLocalRelease = matchingRoute?.creatingLocalRelease === true;
    const group = groupId
      ? incoming.groups.find((item) => item.id === groupId)
      : undefined;
    let creation: IncomingCreationModel | null = null;
    if (creatingLocalRelease && matchingRoute) {
      if (!group) {
        creation = {
          kind: "fallback",
          fallback: {
            backHref: incomingGroupMatchingHref(matchingRoute.groupId),
            backLabel: matchingMessage(locale, "backToIncoming"),
            feedback: {
              kind: "error",
              message: matchingMessage(locale, "groupNotFound"),
            },
          },
        };
      } else {
        const returnToMatching = (
          selection: {
            prepareReleaseId?: string;
            localReleaseId?: string;
          } = {},
          releaseId?: string,
        ) => {
          navigateHash(
            incomingGroupMatchingHref(group.id, releaseId, selection),
            true,
          );
        };
        const suggested = localReleaseSuggestions(
          group,
          files,
          providerArtists,
        );
        creation = {
          kind: "editor",
          key: group.id,
          editor: {
            locale,
            incomingLabel: group.title,
            suggestedArtist: suggested.artist,
            suggestedTitle: suggested.title,
            genreCatalogProvider: DemoLocalReleaseGenreCatalogProvider,
            artists: localProvider.items.artists,
            recordings: localProvider.items.recordings.map((recording) =>
              demoRecordingWithCredits(recording, localProvider.items.artists),
            ),
            checkRelease: async (artist, title) =>
              checkDemoReleases(providers, providerArtists, artist, title),
            findRecordings: async (query) =>
              findDemoRecordings(localProvider.items.recordings, query).map(
                (recording) =>
                  demoRecordingWithCredits(
                    recording,
                    localProvider.items.artists,
                  ),
              ),
            findArtists: async (query) =>
              findDemoArtists(localProvider.items.artists, query),
            saveArtist: (data) => {
              const artist = newDemoLocalRecord(data);
              setLocalProvider((current) =>
                appendDemoLocalRecord(current, "artists", artist),
              );
              return artist;
            },
            saveRecording: (data) => {
              const recording = newDemoLocalRecord({
                ...data,
                artistIds: data.credits?.map((credit) => credit.artistId),
              });
              setLocalProvider((current) =>
                appendDemoLocalRecord(current, "recordings", recording),
              );
              return recording;
            },
            onUseRelease: (id) => {
              const candidate = providers.find((item) => item.id === id);
              if (!candidate) return;
              if (candidate.provider !== "Local") {
                returnToMatching(
                  {
                    prepareReleaseId: candidate.providerId ?? candidate.id,
                  },
                  matchingRoute.releaseId,
                );
                return;
              }
              setIncoming((current) =>
                selectDemoRelease(current, group.id, candidate),
              );
              returnToMatching({ localReleaseId: candidate.id });
            },
            onSave: (draft) => {
              const release = releaseDraftToRecord(
                draft,
                undefined,
                localProvider.items.artists,
              );
              setLocalProvider((current) =>
                appendDemoLocalRecord(current, "releases", release),
              );
              setIncoming((current) =>
                selectDemoRelease(
                  current,
                  group.id,
                  { id: release.id, title: draft.title },
                  true,
                ),
              );
              returnToMatching({ localReleaseId: release.id });
            },
            onCancel: () =>
              navigateHash(
                incomingGroupMatchingHref(group.id, matchingRoute.releaseId, {
                  localReleaseId: matchingRoute.localReleaseId,
                  prepareReleaseId: matchingRoute.prepareReleaseId,
                }),
                true,
              ),
          },
        };
      }
    }
    return bindMeloTroveScreen(DemoMeloTroveIncomingGenreBinder, {
      loaded: settings.genresLoaded,
      onLoad: loadGenres,
      route: incomingRoute,
      list: {
        destination: publicationDestination,
        state: incoming,
        onState: setIncoming,
        files,
        providers,
        roots: sourceRoots,
        artists: providerArtists,
        releases,
        onReleases: setImportedReleases,
        publication: receiptPublicationPort,
        publicationEditions,
      },
      creation,
    });
  };
  const settingsScreen = () =>
    bindMeloTroveRoute(DemoSettingsBinder, {
      settings,
      sourceRoots: sourceRoots.map((root) => root.path),
      browseDirectory: browseDemoDirectory,
      resolveDirectoryPath: demoDirectoryStart,
      onSettings: setSettings,
      toolActivity: toolDownloads,
      toolMoves: {
        active: toolMoveSnapshots.some(
          ({ status }) => status === "queued" || status === "running",
        ),
        visible: toolMoveSnapshots.some(({ status }) => status !== "succeeded"),
        start: startToolMove,
      },
      workDirectoryBusy:
        sourceProcessingLocks(sourceProcessing).blocksWorkDirectoryChange,
      onOutputReset: () => {
        setSourceProcessing((previous) => resetSourceProcessingQueue(previous));
        operationsProvider.reset();
        replacePublicationJobs([]);
        setReceiptPublicationBatches([]);
        setReleases((previous) =>
          previous.map((release) => ({
            ...release,
            tracks: release.tracks.map((track) => ({
              ...track,
              published: null,
              metadataBaseline: undefined,
              metadataBaselineExtras: undefined,
            })),
          })),
        );
        setDeletions([]);
        setIncoming((previous) => ({
          ...previous,
          plan: null,
          inventoryVersion: previous.inventoryVersion + 1,
        }));
      },
    });
  const setupScreen = () =>
    bindMeloTroveScreen(DemoMeloTroveSetupBinder, {
      settings,
      onSettings: setSettings,
      step: routeSetupStep,
      fallbackStep: storedSetupStep,
      stepSync: true,
      onStepChange: setSetupStep,
      toolActivity: toolDownloads,
      initialDraft: {
        toolsDirectory: setupDraftRef.current?.toolsDirectory,
        lrclib: setupDraftRef.current?.lrclib,
        publicationAutoCompleteAlbums:
          setupDraftRef.current?.publicationAutoCompleteAlbums,
      },
      onDraftChange: persistSetupDraft,
      initialGenreCatalogEndpoint: setupDraftRef.current?.genreCatalogEndpoint,
      onGenreCatalogReady: recordSetupGenreCatalog,
    });
  const sourcesScreen = (sourceRoute: SourcesRoute) => {
    return bindMeloTroveScreen(DemoMeloTroveSourcesBinder, {
      roots: sourceRoots,
      onRoots: setSourceRoots,
      files,
      selectedRootId: sourceRoute.view === "list" ? undefined : sourceRoute.id,
      selectedLocationId:
        sourceRoute.view === "location" ? sourceRoute.locationId : undefined,
      workDirectory: settings.workDirectory,
      toolsPath: settings.toolsPath,
      currentOutput: settings.currentOutput ?? "/srv/music",
      processing: sourceProcessing,
      scans: sourceScans,
      onProcessingState: setSourceProcessing,
      cleanupExecution: sourceCleanupExecution,
      onRemove: (id: string) => {
        const next = removeRootFromInventory(files, releases, id);
        setIncoming((previous) => {
          const updated = structuredClone(previous);
          for (const group of updated.groups) {
            group.fileIds = group.fileIds.filter(
              (fileId) => !next.orphaned.has(fileId),
            );
            for (const assignments of Object.values(group.drafts))
              for (const assignment of Object.values(assignments)) {
                assignment.sourceIds = assignment.sourceIds.filter(
                  (fileId) => !next.orphaned.has(fileId),
                );
                if (
                  assignment.selectedSourceId &&
                  next.orphaned.has(assignment.selectedSourceId)
                )
                  assignment.selectedSourceId = null;
                for (const fileId of next.orphaned)
                  delete assignment.sourceMeta[fileId];
              }
          }
          updated.inventoryVersion++;
          return updated;
        });
        setFiles(next.files);
        setReleases(next.releases);
        setSourceRoots((previous) => previous.filter((root) => root.id !== id));
      },
    });
  };
  const operationsScreen = () =>
    bindMeloTroveRoute(DemoOperationsBinder, {
      deletions,
      provider: operationsProvider,
      scanAttempts: sourceScans.attempts,
      sourceRoots,
      onScanRoot: sourceScans.start,
      sourceCleanup: sourceCleanupExecution,
      toolDownloads: toolDownloadSnapshots,
      onRetryToolDownload: retryToolDownload,
      onDismissToolDownload: dismissToolDownload,
      toolMoves: toolMoveSnapshots,
      onRetryToolMove: retryToolMove,
      onDismissToolMove: dismissToolMove,
      positionHrefForTrack: (positionId) => {
        const release = releases.find((item) =>
          item.tracks.some((track) => track.id === positionId),
        );
        return release
          ? libraryPositionHref(release.id, positionId)
          : undefined;
      },
      isTargetCurrent: (href) => {
        const sourceAddress = parseSourcesRoute(href);
        if (sourceAddress && sourceAddress.view !== "list")
          return sourceRoots.some((root) => root.id === sourceAddress.id);
        const incomingTarget = parseIncomingGroupsRoute(href);
        if (incomingTarget?.view === "matching")
          return incoming.groups.some(
            (group) => group.id === incomingTarget.groupId && !group.processed,
          );
        const positionAddress = parseLibraryPositionAddress(href);
        if (positionAddress)
          return releases.some(
            (release) =>
              release.id === positionAddress.releaseId &&
              release.tracks.some(
                (track) => track.id === positionAddress.positionId,
              ),
          );
        const releaseAddress = parseLibraryReleaseAddress(href);
        if (releaseAddress)
          return releases.some(
            (release) => release.id === releaseAddress.releaseId,
          );
        const artistAddress = parseLibraryArtistAddress(href);
        if (artistAddress)
          return artists.some((artist) => artist.id === artistAddress.artistId);
        return false;
      },
    });
  const homeScreen = () =>
    bindMeloTroveHomeModel({ kind: "ready", count: sourceRoots.length });
  const librarySummary = {
    artists: fixture.artists.length,
    releases: releases.length,
    positions: releases.reduce(
      (total, release) => total + release.tracks.length,
      0,
    ),
    published: releases.reduce(
      (total, release) =>
        total + release.tracks.filter((track) => track.published).length,
      0,
    ),
  };
  const catalogIsEmpty =
    librarySummary.artists === 0 &&
    librarySummary.releases === 0 &&
    librarySummary.positions === 0;
  const isPristine = query === "" && filter === "all";
  const hasResults =
    view === "artists"
      ? pageArtists.length > 0
      : view === "albums"
        ? pageAlbumEntries.length > 0
        : pageTracks.length > 0;
  const libraryPageState: LibraryCatalogPageState = hasResults
    ? { status: "results" }
    : {
        status: "empty",
        reason:
          catalogIsEmpty && isPristine
            ? "library"
            : filter === "published" && librarySummary.published === 0
              ? "unpublished"
              : isPristine
                ? "view"
                : "filtered",
        onReset: !isPristine ? resetLibrarySearch : undefined,
        addSourceHref: catalogIsEmpty && isPristine ? sourcesHref : undefined,
      };

  const missingCounts = new Map(
    fixture.artists.map((artist) => [
      artist.id,
      unrepresentedMissingGroups.filter((group) => group.artistId === artist.id)
        .length,
    ]),
  );
  const pageSelection = { selectedIds: selected, onChange: update };
  const libraryResults: LibraryCatalogResults =
    view === "artists"
      ? {
          kind: "artists",
          ...libraryCatalogArtistResults({
            artists: pageArtists,
            releases,
            missingCounts,
            hasAudio: (track) => hasAudio(track as Track),
            pageSelection,
          }),
        }
      : view === "tracks"
        ? {
            kind: "tracks",
            ...libraryCatalogTrackResults({
              locale,
              artists: fixture.artists,
              releases: pageTrackReleases,
              hasAudio: (track) => hasAudio(track as Track),
              files,
              filter,
              pageSelection,
            }),
          }
        : {
            kind: "albums",
            DataProvider: DemoLibraryAlbumRowsProvider,
          };

  const libraryCatalogScreen = () =>
    bindMeloTroveRoute(
      DemoLibraryCatalogBinder,
      {
        locale,
        summary: librarySummary,
        incomingHref: incomingGroupsListHref(),
        view,
        search: searchInput,
        onSearch: setSearch,
        filter,
        onFilter: setFilter,
        publication,
        availability,
        onPublicationChange: setPublication,
        onAvailabilityChange: setAvailability,
        onResetFilters: resetLibrarySearch,
        onViewChange: (nextView) => {
          setView(nextView);
          setExpanded(null);
        },
        state: libraryPageState,
        results: libraryResults,
        albumRows:
          view === "albums"
            ? {
                locale,
                artists: fixture.artists,
                releases: pageAlbumReleases,
                albumEntries: pageAlbumEntries,
                selectedEditions: editions,
                onSelectEdition: (groupId, editionKey) =>
                  setEditions((previous) => ({
                    ...previous,
                    [groupId]: editionKey,
                  })),
                hasAudio: (track) => hasAudio(track as Track),
                files,
                filter,
                expanded,
                onToggleAlbum: (id) => setExpanded(expanded === id ? null : id),
                pageSelection,
              }
            : null,
        page: currentPage,
        totalPages,
        totalItems: libraryTotalItems,
        hasPrevious: currentPage > 1,
        hasNext: currentPage < totalPages,
        onPageChange: (next) => setPage(next, totalPages),
      },
      {
        selection: {
          dock: (
            <SelectionDock
              selected={selected}
              visiblePositionIds={visiblePositionIds}
              releases={releases}
              onSelect={update}
              onPreview={openSelectionPreview}
              onQueueDeletion={queueDeletion}
            />
          ),
          preview: selectionPreviewView,
        },
      },
    );
  const screens: MeloTroveScreens = {
    home: homeScreen,
    setup: setupScreen,
    sources: sourcesScreen,
    incoming: incomingScreen,
    libraryRelease: libraryReleaseScreen,
    libraryArtist: libraryArtistScreen,
    libraryCatalog: libraryCatalogScreen,
    ownCatalog: ownCatalogScreen,
    settings: settingsScreen,
    operations: operationsScreen,
  };
  return {
    locale,
    themeStore: themePreferenceStore,
    footer: { kind: "label", label: demoCopy.footer },
    documentTitleSuffix: demoCopy.documentTitleSuffix,
    setupLink: true,
    incomingCount,
    startup: { kind: "ready" },
    screens,
  };
}

/**
 * The demo implements data hooks and the browser adapter only. Every route is
 * parsed and its portable screen selected by MeloTroveUI.
 */
export const demoMeloTroveProvider = {
  useLocation: useMeloTroveHashLocation,
  useModel: useDemoMeloTroveModel,
} satisfies MeloTroveProvider;

function DemoMeloTroveIncomingGenreBinder({
  loaded,
  onLoad,
  ...props
}: IncomingWorkspaceAnchorProps & {
  loaded: boolean;
  onLoad: () => void | Promise<void>;
  children: ReactNode;
}) {
  return (
    <DemoLocalReleaseGenreLoadProvider loaded={loaded} onLoad={onLoad}>
      <DemoMeloTroveIncomingBinder {...props} />
    </DemoLocalReleaseGenreLoadProvider>
  );
}
