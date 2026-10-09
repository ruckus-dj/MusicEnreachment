import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { subscribeToOperation } from "../../api/client/operations";
import {
  activateToolInstallation,
  checkSettingsMusicbrainz,
  deleteToolInstallation,
  dismissOperation,
  getOperation,
  getSettings,
  listInstallations,
  listOperations,
  listToolCatalog,
  preflightToolInstall,
  preflightToolsRootMove,
  retryOperation,
  startToolInstall,
  startToolsRootMove,
  updateLogLevel,
  updateLrclibSetting,
  updateMusicbrainzSettings,
  updateSettings,
  updateSha256Setting,
} from "../../api/generated/client";
import type {
  CatalogBody,
  InstallationActionInputBodyPackageKind,
  InstallationResponse,
  InstallationsBody,
  InstallPreflightBody,
  MovePreflightBody,
  OperationResponse,
  SetupStateBody,
  UpdateLogLevelBodyLevel,
  UpdateMusicBrainzBodyMode,
  UpdateSettingsBodyPublicationFormat,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import "../setup/setup.css";
import "./settings.css";

const catalogCheckedKey = "melotrove.catalog-checked-at";
const cooldownMs = 24 * 60 * 60 * 1000;
type Kind = InstallationActionInputBodyPackageKind;
type InstallPlan = { kind: Kind; release: string; plan: InstallPreflightBody };
type MovePlan = {
  directory: string;
  removeOld: boolean;
  sourceDirectory: string;
  inputRevision: number;
  sessionRevision: number;
  requestRevision: number;
  plan: MovePreflightBody;
};

type SectionDraft<T> = {
  value: T | undefined;
  baseline: T | undefined;
  revision: number;
};

function useSectionDraft<T>(equal: (left: T, right: T) => boolean = Object.is) {
  const [draft, setDraft] = useState<SectionDraft<T>>({
    value: undefined,
    baseline: undefined,
    revision: 0,
  });
  const current = useRef(draft);
  const pendingRevision = useRef<number | undefined>(undefined);
  const update = useCallback((value: T) => {
    const next = {
      ...current.current,
      value,
      revision: current.current.revision + 1,
    };
    current.current = next;
    setDraft(next);
  }, []);
  const sync = useCallback(
    (value: T) => {
      const previous = current.current;
      const pristine =
        pendingRevision.current === undefined &&
        (previous.value === undefined ||
          (previous.baseline !== undefined &&
            equal(previous.value, previous.baseline)));
      const next = {
        ...previous,
        baseline: value,
        value: pristine ? value : previous.value,
      };
      current.current = next;
      setDraft(next);
    },
    [equal],
  );
  const capture = useCallback(
    () => ({
      value: current.current.value,
      revision: current.current.revision,
    }),
    [],
  );
  const beginSave = useCallback((revision: number) => {
    pendingRevision.current = revision;
  }, []);
  const endSave = useCallback(() => {
    pendingRevision.current = undefined;
  }, []);
  const acknowledge = useCallback((value: T, revision: number) => {
    const previous = current.current;
    const next = {
      ...previous,
      baseline: value,
      value: previous.revision === revision ? value : previous.value,
    };
    current.current = next;
    setDraft(next);
  }, []);
  const dirty =
    draft.value !== undefined &&
    draft.baseline !== undefined &&
    !equal(draft.value, draft.baseline);
  return {
    value: draft.value,
    dirty,
    update,
    sync,
    capture,
    beginSave,
    endSave,
    acknowledge,
  };
}

const sameMusicBrainz = (
  left: { mode: UpdateMusicBrainzBodyMode; baseURL: string },
  right: { mode: UpdateMusicBrainzBodyMode; baseURL: string },
) => left.mode === right.mode && left.baseURL === right.baseURL;

function message(reason: unknown): string {
  if (reason instanceof Error) return reason.message;
  return "Не удалось выполнить запрос.";
}
function apiError(response: { status: number; data?: { detail?: string } }) {
  return new Error(
    response.data?.detail || `Ошибка сервера (${response.status}).`,
  );
}
function successful<T extends { status: number }, S extends T["status"]>(
  response: T,
  status: S,
): Extract<T, { status: S }> {
  if (response.status !== status)
    throw apiError(response as { status: number; data?: { detail?: string } });
  return response as Extract<T, { status: S }>;
}
function activeOperation(operation: OperationResponse) {
  return (
    operation.state === "queued" ||
    operation.state === "running" ||
    operation.state === "failed"
  );
}

export function SettingsScreen() {
  const [state, setState] = useState<SetupStateBody>();
  const [installations, setInstallations] = useState<InstallationResponse[]>(
    [],
  );
  const [catalog, setCatalog] = useState<Partial<Record<Kind, CatalogBody>>>(
    {},
  );
  const [catalogBusy, setCatalogBusy] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [checkedAt, setCheckedAt] = useState(
    () => localStorage.getItem(catalogCheckedKey) || "",
  );
  const [initialLoadComplete, setInitialLoadComplete] = useState(false);
  const [operations, setOperations] = useState<OperationResponse[]>([]);
  const outputDirectory = useSectionDraft<string>();
  const publicationFormat =
    useSectionDraft<UpdateSettingsBodyPublicationFormat>();
  const sourceFileConcurrency = useSectionDraft<string>();
  const musicBrainz = useSectionDraft(sameMusicBrainz);
  const lrclib = useSectionDraft<boolean>();
  const sha256 = useSectionDraft<boolean>();
  const logLevel = useSectionDraft<UpdateLogLevelBodyLevel>();
  const settingsQueue = useRef<Promise<void>>(Promise.resolve());
  const [selectedRelease, setSelectedRelease] = useState<
    Partial<Record<Kind, string>>
  >({});
  const [installDialog, setInstallDialog] = useState<InstallPlan>();
  const [moveDialog, setMoveDialog] = useState(false);
  const [outputDialog, setOutputDialog] = useState(false);
  const [moveDirectory, setMoveDirectory] = useState("");
  const [removeOld, setRemoveOld] = useState(false);
  const [movePlan, setMovePlan] = useState<MovePlan>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const [installDialogError, setInstallDialogError] = useState("");
  const [moveDialogError, setMoveDialogError] = useState("");
  const [outputDialogError, setOutputDialogError] = useState("");
  const [notice, setNotice] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const loadErrorAlert = useRef<HTMLParagraphElement>(null);
  const installDialogAlert = useRef<HTMLParagraphElement>(null);
  const moveDialogAlert = useRef<HTMLParagraphElement>(null);
  const outputDialogAlert = useRef<HTMLParagraphElement>(null);
  const outputDialogRef = useRef<HTMLDialogElement>(null);
  const catalogAlert = useRef<HTMLParagraphElement>(null);
  const automaticCatalogRequested = useRef(false);
  const catalogRequestInFlight = useRef(false);
  const screenLoadInFlight = useRef(false);
  const mountedRef = useRef(true);
  const mountGeneration = useRef(0);
  const screenLoadGeneration = useRef(0);
  const installDialogRef = useRef<HTMLDialogElement>(null);
  const moveDialogRef = useRef<HTMLDialogElement>(null);
  const dialogReturnFocusRef = useRef<HTMLElement | null>(null);
  const dialogFocusRestorePending = useRef(false);
  const moveDirectoryRef = useRef("");
  const moveDirectoryBaseline = useRef<string | undefined>(undefined);
  const removeOldRef = useRef(false);
  const moveInputRevision = useRef(0);
  const moveSessionRevision = useRef(0);
  const moveRequestRevision = useRef(0);
  const movePendingRequest = useRef(0);
  const installSessionRevision = useRef(0);
  const installRequestRevision = useRef(0);
  const installPendingRequest = useRef(0);
  const installStartRevision = useRef(0);
  const moveStartRevision = useRef(0);

  const restoreDialogFocus = useCallback(() => {
    const trigger = dialogReturnFocusRef.current;
    dialogReturnFocusRef.current = null;
    trigger?.focus();
  }, []);

  const invalidateMovePlan = useCallback(() => {
    moveRequestRevision.current += 1;
    const hadPendingRequest = movePendingRequest.current !== 0;
    movePendingRequest.current = 0;
    setMovePlan(undefined);
    if (hadPendingRequest) setBusy(false);
  }, []);
  const setMoveDirectoryValue = useCallback(
    (directory: string) => {
      moveDirectoryRef.current = directory;
      moveInputRevision.current += 1;
      invalidateMovePlan();
      setMoveDirectory(directory);
    },
    [invalidateMovePlan],
  );
  function setRemoveOldValue(remove: boolean) {
    removeOldRef.current = remove;
    moveInputRevision.current += 1;
    invalidateMovePlan();
    setRemoveOld(remove);
  }

  const readSettings = useCallback(() => {
    const request = settingsQueue.current.then(() =>
      getSettings({ cache: "no-store" }),
    );
    settingsQueue.current = request.then(
      () => undefined,
      () => undefined,
    );
    return request;
  }, []);

  const syncForm = useCallback(
    (fresh: SetupStateBody, ack?: (fresh: SetupStateBody) => void) => {
      if (!mountedRef.current) return;
      setState(fresh);
      const settings = fresh.settings;
      outputDirectory.sync(settings.output_directory);
      publicationFormat.sync(
        settings.publication_format === "source" ? "source" : "mka",
      );
      sourceFileConcurrency.sync(String(settings.source_file_concurrency));
      musicBrainz.sync({
        mode:
          settings.musicbrainz_mode === "self-hosted"
            ? "self-hosted"
            : "public",
        baseURL: settings.musicbrainz_base_url,
      });
      lrclib.sync(settings.lrclib_enabled);
      sha256.sync(settings.sha256_enabled);
      logLevel.sync(settings.log_level as UpdateLogLevelBodyLevel);
      if (
        moveDirectoryBaseline.current !== undefined &&
        moveDirectoryBaseline.current !== settings.tools_directory
      ) {
        invalidateMovePlan();
      }
      if (
        moveDirectoryBaseline.current === undefined ||
        moveDirectoryRef.current === moveDirectoryBaseline.current
      ) {
        if (moveDirectoryRef.current !== settings.tools_directory)
          setMoveDirectoryValue(settings.tools_directory);
      }
      moveDirectoryBaseline.current = settings.tools_directory;
      ack?.(fresh);
    },
    [
      setMoveDirectoryValue,
      outputDirectory.sync,
      publicationFormat.sync,
      sourceFileConcurrency.sync,
      musicBrainz.sync,
      lrclib.sync,
      sha256.sync,
      logLevel.sync,
      invalidateMovePlan,
    ],
  );

  const refreshState = useCallback(
    async (
      ack?: (fresh: SetupStateBody) => void,
      isCurrent: () => boolean = () => mountedRef.current,
    ) => {
      const generation = mountGeneration.current;
      const response = successful(await readSettings(), 200);
      if (
        !mountedRef.current ||
        mountGeneration.current !== generation ||
        !isCurrent()
      )
        return response.data;
      syncForm(response.data, ack);
      return response.data;
    },
    [readSettings, syncForm],
  );

  const refreshInstallations = useCallback(
    async (isCurrent: () => boolean = () => mountedRef.current) => {
      const generation = mountGeneration.current;
      const [ffmpeg, fpcalc] = await Promise.all([
        listInstallations({ package_kind: "ffmpeg" }),
        listInstallations({ package_kind: "fpcalc" }),
      ]);
      successful(ffmpeg, 200);
      successful(fpcalc, 200);
      if (
        !mountedRef.current ||
        mountGeneration.current !== generation ||
        !isCurrent()
      )
        return;
      setInstallations([
        ...((ffmpeg.data as InstallationsBody).installations || []),
        ...((fpcalc.data as InstallationsBody).installations || []),
      ]);
    },
    [],
  );

  const refreshOperations = useCallback(
    async (isCurrent: () => boolean = () => mountedRef.current) => {
      const generation = mountGeneration.current;
      const response = successful(await listOperations(), 200);
      const active = (response.data.operations || []).filter(activeOperation);
      if (
        !mountedRef.current ||
        mountGeneration.current !== generation ||
        !isCurrent()
      )
        return active;
      setOperations(active);
      return active;
    },
    [],
  );

  const loadScreen = useCallback(async () => {
    if (!mountedRef.current || screenLoadInFlight.current) return;
    screenLoadInFlight.current = true;
    const generation = ++screenLoadGeneration.current;
    const isCurrent = () =>
      mountedRef.current && screenLoadGeneration.current === generation;
    setBusy(true);
    setInitialLoadComplete(false);
    try {
      const results = await Promise.allSettled([
        refreshState(undefined, isCurrent),
        refreshInstallations(isCurrent),
        refreshOperations(isCurrent),
      ]);
      const failure = results.find(
        (result): result is PromiseRejectedResult =>
          result.status === "rejected",
      );
      if (failure) throw failure.reason;
      if (isCurrent()) {
        setLoadError("");
        setInitialLoadComplete(true);
      }
    } catch (reason) {
      if (isCurrent()) setLoadError(message(reason));
    } finally {
      if (isCurrent()) {
        screenLoadInFlight.current = false;
        setBusy(false);
      }
    }
  }, [refreshInstallations, refreshOperations, refreshState]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      mountGeneration.current += 1;
      screenLoadGeneration.current += 1;
      screenLoadInFlight.current = false;
    };
  }, []);
  useEffect(() => {
    heading.current?.focus();
    void loadScreen();
  }, [loadScreen]);
  useEffect(() => {
    if (error && !installDialog && !moveDialog) alert.current?.focus();
  }, [error, installDialog, moveDialog]);
  useLayoutEffect(() => {
    if (installDialogError && installDialogRef.current?.open)
      installDialogAlert.current?.focus();
  }, [installDialogError]);
  useLayoutEffect(() => {
    if (moveDialogError && moveDialogRef.current?.open)
      moveDialogAlert.current?.focus();
  }, [moveDialogError]);
  useLayoutEffect(() => {
    if (outputDialogError && outputDialogRef.current?.open)
      outputDialogAlert.current?.focus();
  }, [outputDialogError]);
  useLayoutEffect(() => {
    if (
      dialogFocusRestorePending.current &&
      !installDialog &&
      !moveDialog &&
      !outputDialog &&
      !busy
    ) {
      dialogFocusRestorePending.current = false;
      restoreDialogFocus();
    }
  }, [busy, installDialog, moveDialog, outputDialog, restoreDialogFocus]);
  useLayoutEffect(() => {
    if (installDialog) showSettingsDialog(installDialogRef.current);
  }, [installDialog]);
  useLayoutEffect(() => {
    if (moveDialog) showSettingsDialog(moveDialogRef.current);
  }, [moveDialog]);
  useLayoutEffect(() => {
    if (outputDialog) showSettingsDialog(outputDialogRef.current);
  }, [outputDialog]);
  useEffect(() => {
    if (catalogError) catalogAlert.current?.focus();
  }, [catalogError]);
  useEffect(() => {
    if (loadError) loadErrorAlert.current?.focus();
  }, [loadError]);

  const requestCatalog = useCallback(async () => {
    if (!mountedRef.current || catalogRequestInFlight.current) return;
    const generation = mountGeneration.current;
    const isCurrent = () =>
      mountedRef.current && mountGeneration.current === generation;
    catalogRequestInFlight.current = true;
    setCatalogBusy(true);
    setCatalogError("");
    try {
      const [ffmpeg, fpcalc] = await Promise.all([
        listToolCatalog({ package_kind: "ffmpeg" }),
        listToolCatalog({ package_kind: "fpcalc" }),
      ]);
      successful(ffmpeg, 200);
      successful(fpcalc, 200);
      if (!isCurrent()) return;
      setCatalog({
        ffmpeg: ffmpeg.data as CatalogBody,
        fpcalc: fpcalc.data as CatalogBody,
      });
      const timestamp = new Date().toISOString();
      localStorage.setItem(catalogCheckedKey, timestamp);
      setCheckedAt(timestamp);
    } catch (reason) {
      if (isCurrent()) setCatalogError(message(reason));
    } finally {
      catalogRequestInFlight.current = false;
      if (isCurrent()) setCatalogBusy(false);
    }
  }, []);

  useEffect(() => {
    if (!initialLoadComplete || !state || automaticCatalogRequested.current)
      return;
    const last = checkedAt ? Date.parse(checkedAt) : 0;
    if (!last || Date.now() - last >= cooldownMs) {
      automaticCatalogRequested.current = true;
      void requestCatalog();
    }
  }, [checkedAt, initialLoadComplete, requestCatalog, state]);

  async function mutate<
    T extends { status: number },
    C extends { revision: number },
  >(
    action: (saved: C) => Promise<T>,
    status: T["status"],
    successText: string,
    capture: () => C,
    acknowledge: (fresh: SetupStateBody, revision: number) => void,
    beginDraftSave: (revision: number) => void,
    endDraftSave: () => void,
  ) {
    const savedDraft = capture();
    beginDraftSave(savedDraft.revision);
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const response = await action(savedDraft);
      successful(response, status);
      try {
        await refreshState((fresh) => acknowledge(fresh, savedDraft.revision));
      } catch (reason) {
        throw new Error(
          `Настройки приняты сервером, но не удалось подтвердить их чтением: ${message(reason)}`,
        );
      }
      setNotice(successText);
    } catch (reason) {
      setError(message(reason));
    } finally {
      endDraftSave();
      setBusy(false);
    }
  }

  function closeOutputDialog(force = false) {
    if (busy && !force) return;
    dialogFocusRestorePending.current = true;
    setOutputDialogError("");
    setOutputDialog(false);
    const dialog = outputDialogRef.current;
    if (dialog?.open) dialog.close();
  }

  const runningOperation = operations.some(
    (operation) => operation.state === "running",
  );

  async function confirmOutputDirectory() {
    if (busy || runningOperation) return;
    const savedDraft = outputDirectory.capture();
    const output = savedDraft.value;
    if (output === undefined) return;
    outputDirectory.beginSave(savedDraft.revision);
    setBusy(true);
    setOutputDialogError("");
    setNotice("");
    try {
      successful(await updateSettings({ output_directory: output }), 204);
      try {
        await refreshState((fresh) =>
          outputDirectory.acknowledge(
            fresh.settings.output_directory,
            savedDraft.revision,
          ),
        );
      } catch (reason) {
        throw new Error(
          `Каталог публикации принят сервером, но не удалось подтвердить его чтением: ${message(reason)}`,
        );
      }
      setNotice("Каталог публикации изменён.");
      closeOutputDialog(true);
    } catch (reason) {
      setOutputDialogError(message(reason));
    } finally {
      outputDirectory.endSave();
      setBusy(false);
    }
  }

  function validConcurrency(value: string) {
    return (
      /^[1-9]\d*$/.test(value.trim()) &&
      Number.isSafeInteger(Number(value.trim()))
    );
  }

  async function checkMusicBrainz() {
    const savedDraft = musicBrainz.capture();
    const payload = savedDraft.value;
    if (!payload) return;
    musicBrainz.beginSave(savedDraft.revision);
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const saved = await updateMusicbrainzSettings({
        mode: payload.mode,
        base_url: payload.mode === "public" ? "" : payload.baseURL,
      });
      successful(saved, 204);
      try {
        await refreshState((fresh) =>
          musicBrainz.acknowledge(
            {
              mode:
                fresh.settings.musicbrainz_mode === "self-hosted"
                  ? "self-hosted"
                  : "public",
              baseURL: fresh.settings.musicbrainz_base_url,
            },
            savedDraft.revision,
          ),
        );
      } catch (reason) {
        throw new Error(
          `Настройки MusicBrainz приняты сервером, но не удалось подтвердить их чтением: ${message(reason)}`,
        );
      }
      const checked = successful(await checkSettingsMusicbrainz(), 200);
      try {
        await refreshState();
      } catch (reason) {
        throw new Error(
          `MusicBrainz сохранён и проверен, но не удалось подтвердить состояние чтением: ${message(reason)}`,
        );
      }
      if (!checked.data.success)
        throw new Error(checked.data.error || "MusicBrainz недоступен.");
      setNotice("MusicBrainz проверен.");
    } catch (reason) {
      setError(message(reason));
    } finally {
      musicBrainz.endSave();
      setBusy(false);
    }
  }

  function closeInstallDialog() {
    dialogFocusRestorePending.current = true;
    installSessionRevision.current += 1;
    installRequestRevision.current += 1;
    installPendingRequest.current = 0;
    installStartRevision.current += 1;
    setInstallDialogError("");
    setBusy(false);
    const dialog = installDialogRef.current;
    if (dialog?.open) dialog.close();
    else {
      setInstallDialog(undefined);
    }
  }
  function closeMoveDialog() {
    dialogFocusRestorePending.current = true;
    moveSessionRevision.current += 1;
    invalidateMovePlan();
    moveStartRevision.current += 1;
    setMoveDialogError("");
    setBusy(false);
    const dialog = moveDialogRef.current;
    if (dialog?.open) dialog.close();
    else {
      setMoveDialog(false);
    }
  }
  function onDialogKeyDown(
    event: React.KeyboardEvent<HTMLDialogElement>,
    close: () => void,
  ) {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Tab") return;
    const controls = event.currentTarget.querySelectorAll<HTMLElement>(
      'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])',
    );
    if (controls.length === 0) return;
    const first = controls[0];
    const last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    } else if (!event.currentTarget.contains(document.activeElement)) {
      event.preventDefault();
      (event.shiftKey ? last : first).focus();
    }
  }
  const loadCatalogNow = () => {
    void requestCatalog();
  };
  async function preflightInstall(kind: Kind) {
    const release = selectedRelease[kind];
    if (!release) {
      setError("Выберите версию для установки.");
      return;
    }
    const sessionRevision = ++installSessionRevision.current;
    const requestRevision = ++installRequestRevision.current;
    installPendingRequest.current = requestRevision;
    setInstallDialogError("");
    setError("");
    setBusy(true);
    try {
      const response = successful(
        await preflightToolInstall({
          package_kind: kind,
          release_identity: release,
        }),
        200,
      );
      if (
        sessionRevision !== installSessionRevision.current ||
        requestRevision !== installRequestRevision.current
      )
        return;
      const plan = response.data;
      if (plan.conflicts?.length) setInstallDialog({ kind, release, plan });
      else await startInstall(plan, [], sessionRevision);
    } catch (reason) {
      if (
        sessionRevision === installSessionRevision.current &&
        requestRevision === installRequestRevision.current
      )
        setError(message(reason));
    } finally {
      if (installPendingRequest.current === requestRevision) {
        installPendingRequest.current = 0;
        setBusy(false);
      }
    }
  }
  async function startInstall(
    plan: InstallPreflightBody,
    confirmed: string[],
    sessionRevision?: number,
  ) {
    const startRevision = ++installStartRevision.current;
    const started = successful(
      await startToolInstall({
        preflight_token: plan.preflight_token,
        confirmed_conflicts: confirmed,
      }),
      200,
    );
    setOperations((previous) => [
      ...previous.filter((item) => item.id !== started.data.id),
      started.data,
    ]);
    if (
      sessionRevision !== undefined &&
      (sessionRevision !== installSessionRevision.current ||
        startRevision !== installStartRevision.current)
    )
      return;
    closeInstallDialog();
  }
  async function confirmInstall() {
    if (!installDialog) return;
    const sessionRevision = installSessionRevision.current;
    const startRevision = ++installStartRevision.current;
    setBusy(true);
    setInstallDialogError("");
    try {
      const started = successful(
        await startToolInstall({
          preflight_token: installDialog.plan.preflight_token,
          confirmed_conflicts: [...(installDialog.plan.conflicts || [])],
        }),
        200,
      );
      setOperations((previous) => [
        ...previous.filter((item) => item.id !== started.data.id),
        started.data,
      ]);
      if (
        sessionRevision !== installSessionRevision.current ||
        startRevision !== installStartRevision.current
      )
        return;
      closeInstallDialog();
    } catch (reason) {
      if (
        sessionRevision === installSessionRevision.current &&
        startRevision === installStartRevision.current
      )
        setInstallDialogError(message(reason));
    } finally {
      if (
        sessionRevision === installSessionRevision.current &&
        startRevision === installStartRevision.current
      )
        setBusy(false);
    }
  }
  async function activate(item: InstallationResponse, kind: Kind) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      successful(
        await activateToolInstallation(item.id, { package_kind: kind }),
        204,
      );
      await Promise.all([refreshState(), refreshInstallations()]);
      setNotice(`${kind} ${item.release_identity} активирован.`);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  async function remove(item: InstallationResponse, kind: Kind) {
    if (
      item.active ||
      operations.some(
        (operation) =>
          operation.target_installation_id === item.id &&
          activeOperation(operation),
      )
    ) {
      setError("Активную или занятую операцией версию удалить нельзя.");
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      successful(
        await deleteToolInstallation(item.id, { package_kind: kind }),
        204,
      );
      await Promise.all([refreshState(), refreshInstallations()]);
      setNotice(`${kind} ${item.release_identity} удалён.`);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  async function preflightMove() {
    const directory = moveDirectoryRef.current;
    const removeOldFiles = removeOldRef.current;
    const inputRevision = moveInputRevision.current;
    const sessionRevision = moveSessionRevision.current;
    const requestRevision = ++moveRequestRevision.current;
    movePendingRequest.current = requestRevision;
    const sourceDirectory = state?.settings.tools_directory || "";
    setBusy(true);
    setError("");
    setMoveDialogError("");
    try {
      const response = successful(
        await preflightToolsRootMove({
          new_tools_directory: directory,
          remove_old_files: removeOldFiles,
        }),
        200,
      );
      if (
        requestRevision !== moveRequestRevision.current ||
        inputRevision !== moveInputRevision.current ||
        sessionRevision !== moveSessionRevision.current ||
        !moveDialog
      )
        return;
      setMovePlan({
        directory,
        removeOld: removeOldFiles,
        sourceDirectory,
        inputRevision,
        sessionRevision,
        requestRevision,
        plan: {
          ...response.data,
          conflicts: [...(response.data.conflicts || [])],
        },
      });
    } catch (reason) {
      if (
        requestRevision === moveRequestRevision.current &&
        inputRevision === moveInputRevision.current &&
        sessionRevision === moveSessionRevision.current &&
        moveDialog
      )
        setMoveDialogError(message(reason));
    } finally {
      if (movePendingRequest.current === requestRevision) {
        movePendingRequest.current = 0;
        setBusy(false);
      }
    }
  }
  async function confirmMove() {
    if (
      !movePlan ||
      movePlan.sourceDirectory !== state?.settings.tools_directory ||
      movePlan.inputRevision !== moveInputRevision.current ||
      movePlan.sessionRevision !== moveSessionRevision.current ||
      movePlan.requestRevision !== moveRequestRevision.current ||
      movePlan.directory !== moveDirectoryRef.current ||
      movePlan.removeOld !== removeOldRef.current
    ) {
      invalidateMovePlan();
      return;
    }
    const confirmedPlan = movePlan;
    const sessionRevision = moveSessionRevision.current;
    const startRevision = ++moveStartRevision.current;
    setBusy(true);
    setMoveDialogError("");
    try {
      const response = successful(
        await startToolsRootMove({
          preflight_token: confirmedPlan.plan.preflight_token,
          confirmed_conflicts: [...(confirmedPlan.plan.conflicts || [])],
        }),
        200,
      );
      setOperations((previous) => [
        ...previous.filter((item) => item.id !== response.data.id),
        response.data,
      ]);
      if (
        sessionRevision !== moveSessionRevision.current ||
        startRevision !== moveStartRevision.current
      )
        return;
      setMovePlan(undefined);
      closeMoveDialog();
    } catch (reason) {
      if (
        sessionRevision === moveSessionRevision.current &&
        startRevision === moveStartRevision.current
      ) {
        invalidateMovePlan();
        setMoveDialogError(message(reason));
      }
    } finally {
      if (
        sessionRevision === moveSessionRevision.current &&
        startRevision === moveStartRevision.current
      )
        setBusy(false);
    }
  }
  const onOperation = useCallback(
    async (snapshot: OperationResponse) => {
      setOperations((previous) =>
        previous.map((item) => (item.id === snapshot.id ? snapshot : item)),
      );
      if (snapshot.state === "succeeded") {
        try {
          await Promise.all([refreshState(), refreshInstallations()]);
          await refreshOperations();
        } catch (reason) {
          setError(message(reason));
        }
      }
    },
    [refreshInstallations, refreshOperations, refreshState],
  );

  const platformReady = Boolean(
    state?.platform.supported && !state.platform.diagnostic,
  );
  const persistedTime = checkedAt
    ? new Date(checkedAt).toLocaleString()
    : "Каталог ещё не проверялся.";
  return (
    <section
      aria-labelledby="settings-title"
      className="settings-screen mx-auto max-w-5xl py-3"
    >
      <h1 ref={heading} tabIndex={-1} id="settings-title">
        Настройки
      </h1>
      <p className="setup-help">
        После завершения Setup проблемы конфигурации отображаются здесь и не
        возвращают приложение в мастер.
      </p>
      {busy && <p role="status">Выполняется запрос…</p>}
      {!initialLoadComplete && !loadError && (
        <p role="status">Загрузка настроек и состояния сервера…</p>
      )}
      {loadError && (
        <>
          <p ref={loadErrorAlert} tabIndex={-1} role="alert">
            Не удалось загрузить настройки и состояние сервера: {loadError}
          </p>
          <AppButton isDisabled={busy} onPress={() => void loadScreen()}>
            Повторить загрузку
          </AppButton>
        </>
      )}
      {initialLoadComplete && state && (
        <>
          <section
            aria-labelledby="health-title"
            className="setup-panel settings-panel"
          >
            <h2 id="health-title">Состояние конфигурации</h2>
            <p role={state.configuration_health.healthy ? "status" : "alert"}>
              {state.configuration_health.healthy
                ? "Конфигурация исправна."
                : "Есть проблемы конфигурации."}
            </p>
            {!!state.configuration_health.problems?.length && (
              <ul>
                {state.configuration_health.problems.map((problem) => (
                  <li key={problem}>{problem}</li>
                ))}
              </ul>
            )}
            {!platformReady && (
              <p role="alert">
                Платформа недоступна: операции с инструментами отключены.
                Настройки можно исправлять.
              </p>
            )}
          </section>
          <div className="settings-grid">
            <section
              aria-labelledby="runtime-title"
              className="setup-panel settings-panel"
            >
              <h2 id="runtime-title">Публикация</h2>
              <label>
                Output directory
                <input
                  value={outputDirectory.value ?? ""}
                  onChange={(event) =>
                    outputDirectory.update(event.target.value)
                  }
                />
              </label>
              <label>
                Publication format
                <select
                  value={publicationFormat.value ?? "mka"}
                  onChange={(event) =>
                    publicationFormat.update(
                      event.target.value as UpdateSettingsBodyPublicationFormat,
                    )
                  }
                >
                  <option value="source">Исходный формат</option>
                  <option value="mka">MKA remux</option>
                </select>
              </label>
              {(outputDirectory.dirty || publicationFormat.dirty) && (
                <p>Есть несохранённые изменения публикации.</p>
              )}
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    (savedDraft) =>
                      updateSettings({
                        publication_format:
                          savedDraft.value ??
                          (state.settings.publication_format === "source"
                            ? "source"
                            : "mka"),
                      }),
                    204,
                    "Формат публикации сохранён.",
                    publicationFormat.capture,
                    (fresh, revision) =>
                      publicationFormat.acknowledge(
                        fresh.settings.publication_format === "source"
                          ? "source"
                          : "mka",
                        revision,
                      ),
                    publicationFormat.beginSave,
                    publicationFormat.endSave,
                  )
                }
              >
                Сохранить формат
              </AppButton>
              <AppButton
                isDisabled={
                  busy ||
                  runningOperation ||
                  outputDirectory.value === state.settings.output_directory
                }
                onPress={(event) => {
                  dialogReturnFocusRef.current = event.target as HTMLElement;
                  setError("");
                  setOutputDialogError("");
                  setOutputDialog(true);
                }}
              >
                Сменить output
              </AppButton>
            </section>
            <section
              aria-labelledby="analysis-concurrency-title"
              className="setup-panel settings-panel"
            >
              <h2 id="analysis-concurrency-title">Анализ исходников</h2>
              <label>
                Одновременные файлы
                <input
                  aria-describedby="source-file-concurrency-help"
                  aria-invalid={
                    sourceFileConcurrency.value !== undefined &&
                    !validConcurrency(sourceFileConcurrency.value)
                  }
                  inputMode="numeric"
                  value={
                    sourceFileConcurrency.value ??
                    String(state.settings.source_file_concurrency)
                  }
                  onChange={(event) =>
                    sourceFileConcurrency.update(event.target.value)
                  }
                />
              </label>
              <p id="source-file-concurrency-help">
                Положительное целое число файлов, обрабатываемых одновременно.
                Значение должно точно представляться в браузере.
              </p>
              {sourceFileConcurrency.value !== undefined &&
                !validConcurrency(sourceFileConcurrency.value) && (
                  <p role="alert">
                    Введите положительное целое число, точно представимое в
                    браузере.
                  </p>
                )}
              {sourceFileConcurrency.dirty && (
                <p>Есть несохранённое изменение параллельности.</p>
              )}
              <AppButton
                isDisabled={
                  busy ||
                  !sourceFileConcurrency.value ||
                  !validConcurrency(sourceFileConcurrency.value)
                }
                onPress={() =>
                  void mutate(
                    (savedDraft) =>
                      updateSettings({
                        source_file_concurrency: Number(savedDraft.value),
                      }),
                    204,
                    "Параллельность анализа сохранена.",
                    sourceFileConcurrency.capture,
                    (fresh, revision) =>
                      sourceFileConcurrency.acknowledge(
                        String(fresh.settings.source_file_concurrency),
                        revision,
                      ),
                    sourceFileConcurrency.beginSave,
                    sourceFileConcurrency.endSave,
                  )
                }
              >
                Сохранить параллельность
              </AppButton>
            </section>
            <section
              aria-labelledby="providers-title"
              className="setup-panel settings-panel"
            >
              <h2 id="providers-title">Провайдеры метаданных</h2>
              <label>
                MusicBrainz mode
                <select
                  value={musicBrainz.value?.mode ?? "public"}
                  onChange={(event) =>
                    musicBrainz.update({
                      mode: event.target.value as UpdateMusicBrainzBodyMode,
                      baseURL: musicBrainz.value?.baseURL ?? "",
                    })
                  }
                >
                  <option value="public">Public</option>
                  <option value="self-hosted">Self-hosted</option>
                </select>
              </label>
              {musicBrainz.value?.mode === "self-hosted" && (
                <label>
                  MusicBrainz base URL
                  <input
                    value={musicBrainz.value?.baseURL ?? ""}
                    onChange={(event) =>
                      musicBrainz.update({
                        mode: musicBrainz.value?.mode ?? "public",
                        baseURL: event.target.value,
                      })
                    }
                  />
                </label>
              )}
              <p>
                Последняя проверка:{" "}
                {state.settings.musicbrainz_verified_at || "не проверено"}
              </p>
              {musicBrainz.dirty && (
                <p>Есть несохранённые изменения MusicBrainz.</p>
              )}
              <AppButton
                isDisabled={busy}
                onPress={() => void checkMusicBrainz()}
              >
                Сохранить и проверить MusicBrainz
              </AppButton>
              <label className="settings-inline">
                <input
                  type="checkbox"
                  checked={lrclib.value ?? state.settings.lrclib_enabled}
                  onChange={(event) => lrclib.update(event.target.checked)}
                />{" "}
                LRCLIB включён
              </label>
              {lrclib.dirty && <p>Есть несохранённые изменения LRCLIB.</p>}
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    (savedDraft) =>
                      updateLrclibSetting({
                        enabled:
                          savedDraft.value ?? state.settings.lrclib_enabled,
                      }),
                    204,
                    "Настройка LRCLIB сохранена.",
                    lrclib.capture,
                    (fresh, revision) =>
                      lrclib.acknowledge(
                        fresh.settings.lrclib_enabled,
                        revision,
                      ),
                    lrclib.beginSave,
                    lrclib.endSave,
                  )
                }
              >
                Сохранить LRCLIB
              </AppButton>
              <label className="settings-inline">
                <input
                  type="checkbox"
                  checked={sha256.value ?? state.settings.sha256_enabled}
                  onChange={(event) => sha256.update(event.target.checked)}
                />{" "}
                Вычислять SHA-256
              </label>
              <p className="sources-note">
                Настройка применяется к последующим анализам. Её изменение не
                удаляет сохранённые результаты и не запускает массовый пересчёт.
              </p>
              {sha256.dirty && <p>Есть несохранённые изменения SHA-256.</p>}
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    (savedDraft) =>
                      updateSha256Setting({
                        enabled:
                          savedDraft.value ?? state.settings.sha256_enabled,
                      }),
                    204,
                    "Настройка SHA-256 сохранена.",
                    sha256.capture,
                    (fresh, revision) =>
                      sha256.acknowledge(
                        fresh.settings.sha256_enabled,
                        revision,
                      ),
                    sha256.beginSave,
                    sha256.endSave,
                  )
                }
              >
                Сохранить SHA-256
              </AppButton>
            </section>
            <section
              aria-labelledby="logging-title"
              className="setup-panel settings-panel"
            >
              <h2 id="logging-title">Журналирование</h2>
              <label>
                Log level
                <select
                  value={logLevel.value ?? "info"}
                  onChange={(event) =>
                    logLevel.update(
                      event.target.value as UpdateLogLevelBodyLevel,
                    )
                  }
                >
                  <option value="debug">debug</option>
                  <option value="info">info</option>
                  <option value="warn">warn</option>
                  <option value="error">error</option>
                </select>
              </label>
              {logLevel.dirty && (
                <p>Есть несохранённые изменения уровня журнала.</p>
              )}
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    (savedDraft) =>
                      updateLogLevel({
                        level:
                          savedDraft.value ??
                          (state.settings.log_level as UpdateLogLevelBodyLevel),
                      }),
                    204,
                    "Уровень журнала применён.",
                    logLevel.capture,
                    (fresh, revision) =>
                      logLevel.acknowledge(
                        fresh.settings.log_level as UpdateLogLevelBodyLevel,
                        revision,
                      ),
                    logLevel.beginSave,
                    logLevel.endSave,
                  )
                }
              >
                Применить уровень
              </AppButton>
            </section>
            <section
              aria-labelledby="tools-root-title"
              className="setup-panel settings-panel"
            >
              <h2 id="tools-root-title">Каталог инструментов</h2>
              <p>
                Текущий Tools directory:{" "}
                <code>{state.settings.tools_directory}</code>
              </p>
              <AppButton
                isDisabled={!platformReady || busy}
                onPress={(event) => {
                  dialogReturnFocusRef.current = event.target as HTMLElement;
                  setError("");
                  setMoveDialogError("");
                  moveSessionRevision.current += 1;
                  moveInputRevision.current += 1;
                  setMovePlan(undefined);
                  setMoveDialog(true);
                }}
              >
                Перенести каталог
              </AppButton>
            </section>
          </div>
          <section
            aria-labelledby="managed-title"
            className="setup-panel settings-panel"
          >
            <h2 id="managed-title">Managed tools</h2>
            <p>Последняя успешная проверка каталога: {persistedTime}</p>
            {catalogError && (
              <p ref={catalogAlert} tabIndex={-1} role="alert">
                {catalogError}
              </p>
            )}
            <AppButton isDisabled={catalogBusy} onPress={loadCatalogNow}>
              {catalogBusy ? "Обновление каталога…" : "Обновить каталог"}
            </AppButton>
            <div className="settings-grid">
              {(["ffmpeg", "fpcalc"] as const).map((kind) => {
                const installed = installations.filter(
                  (item) => item.package_kind === kind,
                );
                const releases = catalog[kind]?.releases || [];
                const occupied = (item: InstallationResponse) =>
                  operations.some(
                    (op) =>
                      op.target_installation_id === item.id &&
                      activeOperation(op),
                  );
                return (
                  <section
                    key={kind}
                    aria-labelledby={`${kind}-title`}
                    className="setup-tool"
                  >
                    <h3 id={`${kind}-title`}>
                      {kind === "ffmpeg" ? "FFmpeg package" : "fpcalc"}
                    </h3>
                    <h4>Активная версия</h4>
                    <ul>
                      {installed
                        .filter((item) => item.active)
                        .map((item) => (
                          <li key={item.id}>
                            {item.release_identity} — активна
                          </li>
                        ))}
                      {!installed.some((item) => item.active) && (
                        <li>Нет активной версии</li>
                      )}
                    </ul>
                    <h4>Установленные версии</h4>
                    <ul>
                      {installed.map((item) => (
                        <li key={item.id}>
                          {item.release_identity} ({item.state}){" "}
                          {item.active && "— активна"}
                          <AppButton
                            isDisabled={
                              !platformReady ||
                              busy ||
                              item.active ||
                              occupied(item) ||
                              item.state !== "ready"
                            }
                            onPress={() => void activate(item, kind)}
                          >
                            Активировать {item.release_identity}
                          </AppButton>
                          <AppButton
                            isDisabled={
                              !platformReady ||
                              busy ||
                              item.active ||
                              occupied(item)
                            }
                            onPress={() => void remove(item, kind)}
                          >
                            Удалить {item.release_identity}
                          </AppButton>
                        </li>
                      ))}
                    </ul>
                    <h4>Доступные версии</h4>
                    {catalog[kind] ? (
                      <>
                        {catalog[kind]?.notice && (
                          <p>{catalog[kind]?.notice}</p>
                        )}
                        <label>
                          Версия для установки
                          <select
                            aria-label={`Версия ${kind}`}
                            value={selectedRelease[kind] || ""}
                            onChange={(event) =>
                              setSelectedRelease((previous) => ({
                                ...previous,
                                [kind]: event.target.value,
                              }))
                            }
                          >
                            <option value="">Выберите версию</option>
                            {releases.map((release) => (
                              <option
                                key={release.identity}
                                value={release.identity}
                              >
                                {release.identity} ({release.source})
                              </option>
                            ))}
                          </select>
                        </label>
                        <AppButton
                          isDisabled={
                            !platformReady || busy || !selectedRelease[kind]
                          }
                          onPress={(event) => {
                            dialogReturnFocusRef.current =
                              event.target as HTMLElement;
                            setError("");
                            void preflightInstall(kind);
                          }}
                        >
                          Установить без активации
                        </AppButton>
                      </>
                    ) : (
                      <p>
                        {catalogBusy
                          ? "Загрузка каталога…"
                          : "Каталог пока не загружен."}
                      </p>
                    )}
                  </section>
                );
              })}
            </div>
          </section>
          {!!operations.length && (
            <section
              aria-labelledby="operations-title"
              className="setup-panel settings-panel"
            >
              <h2 id="operations-title">Текущие операции</h2>
              {operations.map((operation) => (
                <OperationRow
                  key={operation.id}
                  operation={operation}
                  onSnapshot={onOperation}
                  onDismiss={async (id) => {
                    successful(await dismissOperation(id), 204);
                    setOperations((items) =>
                      items.filter((item) => item.id !== id),
                    );
                  }}
                  onRetry={async (id) => {
                    const retried = successful(await retryOperation(id), 200);
                    setOperations((items) =>
                      items.map((item) =>
                        item.id === id ? retried.data : item,
                      ),
                    );
                  }}
                />
              ))}
            </section>
          )}
        </>
      )}
      {error && (
        <p ref={alert} tabIndex={-1} role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {state && (
        <AppButton isDisabled={busy} onPress={() => void loadScreen()}>
          Обновить состояние
        </AppButton>
      )}
      {outputDialog && (
        <dialog
          ref={outputDialogRef}
          aria-modal="true"
          aria-labelledby="output-dialog-title"
          aria-describedby="output-dialog-warning"
          className="settings-dialog settings-dialog--destructive"
          onClose={() => {
            dialogFocusRestorePending.current = true;
            setOutputDialog(false);
            setOutputDialogError("");
          }}
          onKeyDown={(event) => onDialogKeyDown(event, closeOutputDialog)}
          onCancel={(event) => {
            event.preventDefault();
            closeOutputDialog();
          }}
        >
          <h2 id="output-dialog-title">Сменить каталог публикации?</h2>
          <p id="output-dialog-warning">
            Изменение output отменит все операции в очереди и очистит ссылки на
            прежние файлы в приложении. Сами прежние файлы не будут удалены.
          </p>
          {outputDialogError && (
            <p ref={outputDialogAlert} tabIndex={-1} role="alert">
              {outputDialogError}
            </p>
          )}
          <AppButton
            isDisabled={busy || runningOperation}
            onPress={() => void confirmOutputDirectory()}
          >
            Подтвердить смену output
          </AppButton>
          <AppButton isDisabled={busy} onPress={() => closeOutputDialog()}>
            Отмена
          </AppButton>
        </dialog>
      )}
      {installDialog && (
        <dialog
          ref={installDialogRef}
          aria-modal="true"
          aria-labelledby="install-dialog-title"
          className="settings-dialog"
          onClose={() => {
            dialogFocusRestorePending.current = true;
            setInstallDialog(undefined);
            setInstallDialogError("");
          }}
          onKeyDown={(event) => onDialogKeyDown(event, closeInstallDialog)}
          onCancel={(event) => {
            event.preventDefault();
            closeInstallDialog();
          }}
        >
          <h2 id="install-dialog-title">
            Подтверждение установки {installDialog.release}
          </h2>
          <p>Установка не активирует выбранную версию.</p>
          <ConflictList
            conflicts={installDialog.plan.conflicts || []}
            targets={installDialog.plan.targets || []}
          />
          {installDialogError && (
            <p ref={installDialogAlert} tabIndex={-1} role="alert">
              {installDialogError}
            </p>
          )}
          <AppButton isDisabled={busy} onPress={() => void confirmInstall()}>
            Подтвердить перечисленные конфликты
          </AppButton>
          <AppButton isDisabled={busy} onPress={closeInstallDialog}>
            Отмена
          </AppButton>
        </dialog>
      )}
      {moveDialog && (
        <dialog
          ref={moveDialogRef}
          aria-modal="true"
          aria-labelledby="move-dialog-title"
          className="settings-dialog"
          onClose={() => {
            dialogFocusRestorePending.current = true;
            setMoveDialog(false);
            setMoveDialogError("");
          }}
          onKeyDown={(event) => onDialogKeyDown(event, closeMoveDialog)}
          onCancel={(event) => {
            event.preventDefault();
            closeMoveDialog();
          }}
        >
          <h2 id="move-dialog-title">Перенос Tools directory</h2>
          <label>
            Новый Tools directory
            <input
              value={moveDirectory}
              onChange={(event) => setMoveDirectoryValue(event.target.value)}
            />
          </label>
          <label className="settings-inline">
            <input
              type="checkbox"
              checked={removeOld}
              onChange={(event) => setRemoveOldValue(event.target.checked)}
            />{" "}
            Удалить прежние управляемые файлы после успешного переноса
          </label>
          {moveDialogError && (
            <p ref={moveDialogAlert} tabIndex={-1} role="alert">
              {moveDialogError}
            </p>
          )}
          {!movePlan ? (
            <AppButton isDisabled={busy} onPress={() => void preflightMove()}>
              Проверить перенос
            </AppButton>
          ) : (
            <>
              <p>
                Исходный каталог: <code>{movePlan.sourceDirectory}</code>
              </p>
              <p>
                Каталог назначения: <code>{movePlan.directory}</code>
              </p>
              <p>
                Удалить прежние управляемые файлы:{" "}
                {movePlan.removeOld ? "Да" : "Нет"}
              </p>
              <p>Управляемых файлов: {movePlan.plan.managed_file_count}</p>
              <p>Конфликтов: {movePlan.plan.conflicts?.length || 0}</p>
              <ConflictList conflicts={movePlan.plan.conflicts || []} />
              <AppButton isDisabled={busy} onPress={() => void confirmMove()}>
                Подтвердить перенос
              </AppButton>
            </>
          )}
          <AppButton
            isDisabled={busy}
            onPress={() => {
              setMovePlan(undefined);
              closeMoveDialog();
            }}
          >
            Отмена
          </AppButton>
        </dialog>
      )}
    </section>
  );
}

function showSettingsDialog(dialog: HTMLDialogElement | null) {
  if (!dialog || dialog.open) return;
  dialog.showModal();
  const firstControl = dialog.querySelector<HTMLElement>(
    "button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled)",
  );
  firstControl?.focus();
}

function ConflictList({
  conflicts,
  targets = [],
}: {
  conflicts: string[];
  targets?: string[];
}) {
  return (
    <div>
      <h3>Целевые файлы</h3>
      <ul>
        {targets.map((path) => (
          <li key={path}>
            <code>{path}</code>
          </li>
        ))}
      </ul>
      <h3>Конфликты для подтверждения</h3>
      {conflicts.length ? (
        <ul>
          {conflicts.map((path) => (
            <li key={path}>
              <code>{path}</code>
            </li>
          ))}
        </ul>
      ) : (
        <p>Конфликтов нет.</p>
      )}
    </div>
  );
}

function OperationRow({
  operation: initial,
  onSnapshot,
  onRetry,
  onDismiss,
}: {
  operation: OperationResponse;
  onSnapshot: (snapshot: OperationResponse) => void | Promise<void>;
  onRetry: (id: string) => Promise<void>;
  onDismiss: (id: string) => Promise<void>;
}) {
  const [operation, setOperation] = useState(initial);
  useEffect(() => setOperation(initial), [initial]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const errorRef = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    const unsubscribe = subscribeToOperation(
      operation.id,
      (snapshot) => {
        setOperation(snapshot);
        setError("");
        void onSnapshot(snapshot);
      },
      (reason) => {
        setError(reason.message);
        void getOperation(operation.id, { cache: "no-store" })
          .then((response) => {
            if (response.status !== 200) throw apiError(response);
            setOperation(response.data);
            setError("");
            void onSnapshot(response.data);
          })
          .catch((recoveryError) => setError(message(recoveryError)));
      },
    );
    void getOperation(operation.id, { cache: "no-store" })
      .then((response) => {
        if (response.status !== 200) throw apiError(response);
        setOperation(response.data);
        void onSnapshot(response.data);
      })
      .catch((reason) => setError(message(reason)));
    return unsubscribe;
  }, [operation.id, onSnapshot]);
  useEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);
  async function action(run: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await run();
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  const pending = operation.state === "queued" || operation.state === "running";
  return (
    <fieldset
      aria-label={`Операция ${operation.id}`}
      className="setup-operation"
    >
      <p role="status">
        {operation.kind}: {operation.state}, {operation.stage},{" "}
        {operation.bytes_completed}
        {operation.bytes_total == null ? "" : ` / ${operation.bytes_total}`}{" "}
        bytes. {operation.safe_error || ""}
      </p>
      {pending && <p>Операция выполняется; состояние обновляется с сервера.</p>}
      {operation.state === "failed" && (
        <>
          <AppButton
            isDisabled={busy}
            onPress={() => void action(() => onRetry(operation.id))}
          >
            Повторить операцию {operation.id}
          </AppButton>
          <AppButton
            isDisabled={busy}
            onPress={() => void action(() => onDismiss(operation.id))}
          >
            Закрыть ошибку {operation.id}
          </AppButton>
        </>
      )}
      {error && (
        <p ref={errorRef} tabIndex={-1} role="alert">
          {error}
        </p>
      )}
    </fieldset>
  );
}
