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
  const [output, setOutput] = useState("");
  const [format, setFormat] =
    useState<UpdateSettingsBodyPublicationFormat>("mka");
  const [mode, setMode] = useState<UpdateMusicBrainzBodyMode>("public");
  const [baseURL, setBaseURL] = useState("");
  const [lrclib, setLrclib] = useState(true);
  const [sha256Enabled, setSha256Enabled] = useState<boolean>();
  const [logLevel, setLogLevel] = useState<UpdateLogLevelBodyLevel>("info");
  const [selectedRelease, setSelectedRelease] = useState<
    Partial<Record<Kind, string>>
  >({});
  const [installDialog, setInstallDialog] = useState<InstallPlan>();
  const [moveDialog, setMoveDialog] = useState(false);
  const [moveDirectory, setMoveDirectory] = useState("");
  const [removeOld, setRemoveOld] = useState(false);
  const [movePlan, setMovePlan] = useState<MovePlan>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const catalogAlert = useRef<HTMLParagraphElement>(null);
  const automaticCatalogRequested = useRef(false);
  const catalogRequestInFlight = useRef(false);
  const installDialogRef = useRef<HTMLDialogElement>(null);
  const moveDialogRef = useRef<HTMLDialogElement>(null);
  const dialogReturnFocusRef = useRef<HTMLElement | null>(null);
  const moveDirectoryRef = useRef("");
  const removeOldRef = useRef(false);
  const moveInputRevision = useRef(0);
  const moveSessionRevision = useRef(0);
  const moveRequestRevision = useRef(0);
  const movePendingRequest = useRef(0);

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

  const syncForm = useCallback(
    (fresh: SetupStateBody) => {
      setState(fresh);
      const settings = fresh.settings;
      setOutput(settings.output_directory);
      setFormat(settings.publication_format === "source" ? "source" : "mka");
      setMode(
        settings.musicbrainz_mode === "self-hosted" ? "self-hosted" : "public",
      );
      setBaseURL(settings.musicbrainz_base_url);
      setLrclib(settings.lrclib_enabled);
      setSha256Enabled(settings.sha256_enabled);
      setLogLevel(settings.log_level as UpdateLogLevelBodyLevel);
      if (moveDirectoryRef.current !== settings.tools_directory)
        setMoveDirectoryValue(settings.tools_directory);
    },
    [setMoveDirectoryValue],
  );

  const refreshState = useCallback(async () => {
    const response = successful(await getSettings({ cache: "no-store" }), 200);
    syncForm(response.data);
    return response.data;
  }, [syncForm]);

  const refreshInstallations = useCallback(async () => {
    const [ffmpeg, fpcalc] = await Promise.all([
      listInstallations({ package_kind: "ffmpeg" }),
      listInstallations({ package_kind: "fpcalc" }),
    ]);
    successful(ffmpeg, 200);
    successful(fpcalc, 200);
    setInstallations([
      ...((ffmpeg.data as InstallationsBody).installations || []),
      ...((fpcalc.data as InstallationsBody).installations || []),
    ]);
  }, []);

  const refreshOperations = useCallback(async () => {
    const response = successful(await listOperations(), 200);
    const active = (response.data.operations || []).filter(activeOperation);
    setOperations(active);
    return active;
  }, []);

  const loadScreen = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      await Promise.all([
        refreshState(),
        refreshInstallations(),
        refreshOperations(),
      ]);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
      setInitialLoadComplete(true);
    }
  }, [refreshInstallations, refreshOperations, refreshState]);

  useEffect(() => {
    heading.current?.focus();
    void loadScreen();
  }, [loadScreen]);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  useLayoutEffect(() => {
    if (installDialog) showSettingsDialog(installDialogRef.current);
  }, [installDialog]);
  useLayoutEffect(() => {
    if (moveDialog) showSettingsDialog(moveDialogRef.current);
  }, [moveDialog]);
  useEffect(() => {
    if (catalogError) catalogAlert.current?.focus();
  }, [catalogError]);

  const requestCatalog = useCallback(async () => {
    if (catalogRequestInFlight.current) return;
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
      setCatalog({
        ffmpeg: ffmpeg.data as CatalogBody,
        fpcalc: fpcalc.data as CatalogBody,
      });
      const timestamp = new Date().toISOString();
      localStorage.setItem(catalogCheckedKey, timestamp);
      setCheckedAt(timestamp);
    } catch (reason) {
      setCatalogError(message(reason));
    } finally {
      catalogRequestInFlight.current = false;
      setCatalogBusy(false);
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

  async function mutate<T extends { status: number }>(
    action: () => Promise<T>,
    status: T["status"],
    successText: string,
  ) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const response = await action();
      successful(response, status);
      await refreshState();
      setNotice(successText);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  async function checkMusicBrainz() {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const saved = await updateMusicbrainzSettings({
        mode,
        base_url: mode === "public" ? "" : baseURL,
      });
      successful(saved, 204);
      await refreshState();
      const checked = successful(await checkSettingsMusicbrainz(), 200);
      await refreshState();
      if (!checked.data.success)
        throw new Error(checked.data.error || "MusicBrainz недоступен.");
      setNotice("MusicBrainz проверен.");
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  function restoreDialogFocus() {
    const trigger = dialogReturnFocusRef.current;
    dialogReturnFocusRef.current = null;
    trigger?.focus();
  }
  function closeInstallDialog() {
    const dialog = installDialogRef.current;
    if (dialog?.open) dialog.close();
    else {
      setInstallDialog(undefined);
      restoreDialogFocus();
    }
  }
  function closeMoveDialog() {
    moveSessionRevision.current += 1;
    invalidateMovePlan();
    const dialog = moveDialogRef.current;
    if (dialog?.open) dialog.close();
    else {
      setMoveDialog(false);
      restoreDialogFocus();
    }
  }
  function onDialogClose(setClosed: () => void) {
    setClosed();
    restoreDialogFocus();
  }
  function onDialogKeyDown(
    event: React.KeyboardEvent<HTMLDialogElement>,
    close: () => void,
  ) {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
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
    setBusy(true);
    setError("");
    try {
      const response = successful(
        await preflightToolInstall({
          package_kind: kind,
          release_identity: release,
        }),
        200,
      );
      const plan = response.data;
      if (plan.conflicts?.length) setInstallDialog({ kind, release, plan });
      else await startInstall(plan, []);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  async function startInstall(plan: InstallPreflightBody, confirmed: string[]) {
    const started = successful(
      await startToolInstall({
        preflight_token: plan.preflight_token,
        confirmed_conflicts: confirmed,
      }),
      200,
    );
    closeInstallDialog();
    setOperations((previous) => [
      ...previous.filter((item) => item.id !== started.data.id),
      started.data,
    ]);
  }
  async function confirmInstall() {
    if (!installDialog) return;
    setBusy(true);
    setError("");
    try {
      await startInstall(
        installDialog.plan,
        installDialog.plan.conflicts || [],
      );
    } catch (reason) {
      setError(message(reason));
    } finally {
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
        setError(message(reason));
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
    setBusy(true);
    setError("");
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
      setMovePlan(undefined);
      closeMoveDialog();
    } catch (reason) {
      invalidateMovePlan();
      setError(message(reason));
    } finally {
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
      {!state && !error && (
        <p role="status">Загрузка настроек и состояния сервера…</p>
      )}
      {state && (
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
                  value={output}
                  onChange={(event) => setOutput(event.target.value)}
                />
              </label>
              <label>
                Publication format
                <select
                  value={format}
                  onChange={(event) =>
                    setFormat(
                      event.target.value as UpdateSettingsBodyPublicationFormat,
                    )
                  }
                >
                  <option value="source">Исходный формат</option>
                  <option value="mka">MKA remux</option>
                </select>
              </label>
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    () =>
                      updateSettings({
                        output_directory: output,
                        publication_format: format,
                      }),
                    204,
                    "Настройки публикации сохранены.",
                  )
                }
              >
                Сохранить публикацию
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
                  value={mode}
                  onChange={(event) =>
                    setMode(event.target.value as UpdateMusicBrainzBodyMode)
                  }
                >
                  <option value="public">Public</option>
                  <option value="self-hosted">Self-hosted</option>
                </select>
              </label>
              {mode === "self-hosted" && (
                <label>
                  MusicBrainz base URL
                  <input
                    value={baseURL}
                    onChange={(event) => setBaseURL(event.target.value)}
                  />
                </label>
              )}
              <p>
                Последняя проверка:{" "}
                {state.settings.musicbrainz_verified_at || "не проверено"}
              </p>
              <AppButton
                isDisabled={busy}
                onPress={() => void checkMusicBrainz()}
              >
                Сохранить и проверить MusicBrainz
              </AppButton>
              <label className="settings-inline">
                <input
                  type="checkbox"
                  checked={lrclib}
                  onChange={(event) => setLrclib(event.target.checked)}
                />{" "}
                LRCLIB включён
              </label>
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    () => updateLrclibSetting({ enabled: lrclib }),
                    204,
                    "Настройка LRCLIB сохранена.",
                  )
                }
              >
                Сохранить LRCLIB
              </AppButton>
              <label className="settings-inline">
                <input
                  type="checkbox"
                  checked={sha256Enabled ?? state.settings.sha256_enabled}
                  onChange={(event) => setSha256Enabled(event.target.checked)}
                />{" "}
                Вычислять SHA-256
              </label>
              <p className="sources-note">
                Настройка применяется к последующим анализам. Её изменение не
                удаляет сохранённые результаты и не запускает массовый пересчёт.
              </p>
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    () =>
                      updateSha256Setting({
                        enabled: sha256Enabled ?? state.settings.sha256_enabled,
                      }),
                    204,
                    "Настройка SHA-256 сохранена.",
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
                  value={logLevel}
                  onChange={(event) =>
                    setLogLevel(event.target.value as UpdateLogLevelBodyLevel)
                  }
                >
                  <option value="debug">debug</option>
                  <option value="info">info</option>
                  <option value="warn">warn</option>
                  <option value="error">error</option>
                </select>
              </label>
              <AppButton
                isDisabled={busy}
                onPress={() =>
                  void mutate(
                    () => updateLogLevel({ level: logLevel }),
                    204,
                    "Уровень журнала применён.",
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
      {installDialog && (
        <dialog
          ref={installDialogRef}
          aria-modal="true"
          aria-labelledby="install-dialog-title"
          className="settings-dialog"
          onClose={() => onDialogClose(() => setInstallDialog(undefined))}
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
          onClose={() => onDialogClose(() => setMoveDialog(false))}
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
