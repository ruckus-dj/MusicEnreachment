import { useCallback, useEffect, useRef, useState } from "react";
import { Radio, RadioGroup } from "react-aria-components";
import { createSource, listSources } from "../../api/generated/client";
import type { SourceRootResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathField } from "../../components/DirectoryPathField";
import { useI18n } from "../../i18n/I18nProvider";
import { useHashLocation } from "../../navigation/hashLocation";
import { SourceArtifactCleanupPanel } from "./SourceArtifactCleanupPanel";
import { SourceDetailScreen } from "./SourceDetailScreen";
import { SourceInspectorScreen } from "./SourceInspectorScreen";
import { SourceRootList, type SourceRootListRow } from "./SourceRootList";
import { sourceMessage } from "./sourceMessages";
import { SourceScansProvider, useSourceScans } from "./sourceScans";
import {
  inventoryAvailabilityNote,
  inventoryEmptyNote,
  inventoryLabel,
  inventoryScopeNote,
  lastScanLabel,
  message,
  renderSourceError,
  sourceFailure,
} from "./sourcesApi";
import "./sources.css";
import {
  parseSourcesRoute,
  type SourcesRoute,
  sourcesListRoute,
} from "./sourcesRoute";

// AppShell supplies its parsed route, so the mounted application has only one
// hash subscription. The standalone entry remains useful for embedding/tests.
export function SourcesScreen({ route }: { route?: SourcesRoute } = {}) {
  return (
    <SourceScansProvider>
      {route ? <SourcesView route={route} /> : <StandaloneSourcesScreen />}
    </SourceScansProvider>
  );
}

function StandaloneSourcesScreen() {
  const hash = useHashLocation();
  return <SourcesView route={parseSourcesRoute(hash) ?? sourcesListRoute} />;
}

function SourcesView({ route }: { route: SourcesRoute }) {
  switch (route.view) {
    case "location":
      return (
        <SourceInspectorScreen
          key={`${route.id}/${route.locationId}`}
          sourceId={route.id}
          locationId={route.locationId}
        />
      );
    case "detail":
      return <SourceDetailScreen key={route.id} sourceId={route.id} />;
    case "list":
      return <SourceListScreen />;
  }
}

// Registration copy follows the approved prototype wording. It stays local to
// this screen because the approved directory list already owns its own copy.
const registrationCopy = {
  en: {
    newSource: "New source",
    name: "Source name",
    namePlaceholder: "For example, Music library",
    serverDirectory: "Server directory",
    onlyDirectory: "Choose a directory; files are not uploaded.",
    processingMode: "Processing mode · choose explicitly",
    inPlace: "In place",
    inPlaceHelp: "Analysis reads the original file without modifying it.",
    staged: "Using a work copy",
    stagedHelp:
      "Processing uses an application-owned audio copy in the shared server work directory. The directory is required for processing, not for adding or scanning a source.",
    sourcePolicy: "Analysis does not modify originals.",
    nameRequired: "Enter a source name.",
    directoryRequired: "Choose a server directory.",
    modeRequired:
      "Choose a processing mode explicitly: In place or Using a work copy.",
    sourceTableHelp:
      "Analysis leaves original files unchanged. Mode changes apply to future processing.",
    addSource: "Add source",
    cancel: "Cancel",
  },
  ru: {
    newSource: "Новый источник",
    name: "Имя источника",
    namePlaceholder: "Например, Музыкальная библиотека",
    serverDirectory: "Каталог на сервере",
    onlyDirectory: "Выберите каталог; файлы не загружаются.",
    processingMode: "Режим обработки · выберите явно",
    inPlace: "На месте",
    inPlaceHelp: "Анализ читает исходный файл, не изменяя его.",
    staged: "Через рабочую копию",
    stagedHelp:
      "Для обработки используется принадлежащая приложению рабочая копия аудио в общей рабочей папке сервера. Папка требуется для обработки, но не для добавления или сканирования источника.",
    sourcePolicy: "Анализ не изменяет исходники.",
    nameRequired: "Укажите имя источника.",
    directoryRequired: "Выберите каталог на сервере.",
    modeRequired:
      "Явно выберите режим обработки: «На месте» или «Через рабочую копию».",
    sourceTableHelp:
      "Анализ не изменяет исходные файлы. Изменение режима применяется к будущей обработке.",
    addSource: "Добавить источник",
    cancel: "Отмена",
  },
} as const;

const modeChoice =
  "sources-inline group inline-flex min-h-8 cursor-pointer items-center gap-[7px] rounded-md border border-[var(--line)] px-3 py-[10px] text-xs data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-offset-2 data-[focus-visible]:outline-[var(--focus)]";
const modeHelp = "m-0 ml-[25px] text-[11px] text-[var(--muted)]";
const radioIndicator =
  "size-4 flex-none rounded-full border border-[var(--control)] bg-[var(--surface)] group-data-[selected]:border-[5px] group-data-[selected]:border-[var(--accent)]";

type RegistrationError =
  | ReturnType<typeof message>
  | "nameRequired"
  | "directoryRequired"
  | "modeRequired";

function SourceListScreen() {
  const { locale } = useI18n();
  const copy = registrationCopy[locale];
  const scans = useSourceScans();
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const [sources, setSources] = useState<SourceRootResponse[]>();
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [processingMode, setProcessingMode] = useState<
    "" | "in_place" | "staged"
  >("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ReturnType<typeof message> | null>(null);
  const [formError, setFormError] = useState<RegistrationError | null>(null);
  const [saving, setSaving] = useState(false);
  const registering = useRef(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const formAlert = useRef<HTMLParagraphElement>(null);
  const loadRequest = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    loadRequest.current?.abort();
    const request = new AbortController();
    loadRequest.current = request;
    setBusy(true);
    setError(null);
    try {
      const response = await listSources({
        cache: "no-store",
        signal: request.signal,
      });
      if (request.signal.aborted) return;
      if (response.status !== 200) throw sourceFailure("list", response);
      setSources(response.data.sources || []);
    } catch (reason) {
      if (!request.signal.aborted) setError(message(reason));
    } finally {
      if (!request.signal.aborted) setBusy(false);
    }
  }, []);

  useEffect(() => {
    heading.current?.focus();
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: A scan revision explicitly requests an authoritative reread.
  useEffect(() => {
    void load();
    return () => loadRequest.current?.abort();
  }, [load, scans.revision]);
  useEffect(() => {
    if (formError) formAlert.current?.focus();
  }, [formError]);

  async function register() {
    if (registering.current) return;
    if (!name.trim()) {
      setFormError("nameRequired");
      return;
    }
    if (!path.trim()) {
      setFormError("directoryRequired");
      return;
    }
    if (!processingMode) {
      setFormError("modeRequired");
      return;
    }
    registering.current = true;
    setSaving(true);
    setFormError(null);
    try {
      const response = await createSource({
        display_name: name.trim(),
        configured_path: path.trim(),
        processing_mode: processingMode,
      });
      if (response.status !== 200) throw sourceFailure("create", response);
      const registered = response.data;
      setCreating(false);
      setName("");
      setPath("");
      setProcessingMode("");
      // Registration only opens the new source. It never starts a scan.
      window.location.hash = `/sources/${encodeURIComponent(registered.id)}`;
    } catch (reason) {
      setFormError(message(reason));
    } finally {
      registering.current = false;
      setSaving(false);
    }
  }

  function cancelCreate() {
    setCreating(false);
    setFormError(null);
    setName("");
    setPath("");
    setProcessingMode("");
  }

  function startScan(rootId: string) {
    // The scan provider records a rejection per root; the alert below the
    // approved list renders it in the current locale.
    void scans.start(rootId);
  }

  const rows: SourceRootListRow[] = (sources ?? []).map((root) => {
    const notes = [
      inventoryAvailabilityNote(root, locale),
      inventoryScopeNote(root, locale),
      inventoryEmptyNote(root, locale),
    ].filter((note) => note.length > 0);
    return {
      id: root.id,
      name: root.display_name,
      path: root.configured_path,
      mode: root.processing_mode,
      availability: root.status,
      lastSuccessfulScan: root.last_successful_scan_at
        ? {
            label: lastScanLabel(root, locale),
            dateTime: root.last_successful_scan_at,
          }
        : undefined,
      inventory: {
        label: inventoryLabel(root, locale),
        notes: notes.length > 0 ? notes : undefined,
      },
      scanning: scans.scanning(root.id),
      href: `#/sources/${encodeURIComponent(root.id)}`,
    };
  });

  const scanErrors = (sources ?? []).flatMap((root) => {
    const scanError = scans.error(root.id);
    return scanError ? [{ root, scanError }] : [];
  });

  // Discovery must finish before a scan can be accepted. Keep the approved
  // list rendering unchanged, but disable its controls meanwhile instead of
  // showing an active-looking button whose press is ignored.
  const scanUnavailable = scans.checking || scans.discoveryError !== null;

  const listState =
    sources === undefined
      ? error
        ? {
            kind: "error" as const,
            message: renderSourceError(error, locale),
            onRetry: () => void load(),
          }
        : { kind: "loading" as const }
      : undefined;

  return (
    <section
      aria-labelledby="sources-title"
      aria-busy={scans.checking || undefined}
      className="sources-screen mx-auto max-w-5xl py-3"
    >
      <header className="mb-4 flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] pb-3">
        <div className="min-w-0">
          <h1 ref={heading} tabIndex={-1} id="sources-title">
            {t("title")}
          </h1>
          <p className="m-0 max-w-[75ch] text-xs text-[var(--muted)]">
            {t("description")}
          </p>
        </div>
        {!creating && (
          <AppButton
            onPress={() => {
              setFormError(null);
              setCreating(true);
            }}
          >
            {copy.addSource}
          </AppButton>
        )}
      </header>
      {creating && (
        <form
          aria-labelledby="source-create-title"
          className="mb-4 grid min-w-0 gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3"
          onSubmit={(event) => {
            event.preventDefault();
            void register();
          }}
        >
          <h2 id="source-create-title" className="m-0 text-sm font-semibold">
            {copy.newSource}
          </h2>
          <label className="grid min-w-0 gap-1 text-xs font-semibold">
            {copy.name}
            <input
              value={name}
              placeholder={copy.namePlaceholder}
              disabled={saving}
              onChange={(event) => setName(event.target.value)}
              className="min-h-8 min-w-0 w-full rounded border border-[var(--control)] bg-[var(--surface)] px-2 py-[5px] text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]"
            />
          </label>
          <DirectoryPathField
            id="source-create-path"
            label={copy.serverDirectory}
            value={path}
            onSelect={setPath}
            disabled={saving}
            description={copy.onlyDirectory}
          />
          <RadioGroup
            className="grid gap-1"
            aria-label={copy.processingMode}
            value={processingMode}
            isDisabled={saving}
            onChange={(value) =>
              setProcessingMode(value as typeof processingMode)
            }
          >
            <Radio className={modeChoice} value="in_place">
              <span aria-hidden="true" className={radioIndicator} />
              {copy.inPlace}
            </Radio>
            <p className={modeHelp}>{copy.inPlaceHelp}</p>
            <Radio className={modeChoice} value="staged">
              <span aria-hidden="true" className={radioIndicator} />
              {copy.staged}
            </Radio>
            <p className={modeHelp}>{copy.stagedHelp}</p>
          </RadioGroup>
          <p className="m-0 text-xs text-[var(--muted)]">{copy.sourcePolicy}</p>
          {formError && (
            <p ref={formAlert} tabIndex={-1} role="alert" className="m-0">
              {typeof formError === "string"
                ? copy[formError]
                : renderSourceError(formError, locale)}
            </p>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <AppButton type="submit" isDisabled={saving}>
              {copy.addSource}
            </AppButton>
            <AppButton onPress={cancelCreate} isDisabled={saving}>
              {copy.cancel}
            </AppButton>
          </div>
        </form>
      )}
      {busy && sources && (
        <p role="status" className="m-0 text-xs text-[var(--muted)]">
          {t("requestRunning")}
        </p>
      )}
      {error && sources && (
        <div role="alert" className="flex flex-wrap items-center gap-2">
          <p className="m-0">{renderSourceError(error, locale)}</p>
          <AppButton onPress={() => void load()}>{t("retryLoad")}</AppButton>
        </div>
      )}
      {scanErrors.length > 0 && (
        <div role="alert" className="grid gap-1">
          {scanErrors.map(({ root, scanError }) => (
            <p key={root.id} className="m-0">
              {root.display_name}: {renderSourceError(scanError, locale)}
            </p>
          ))}
        </div>
      )}
      {scans.discoveryError && (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert" className="m-0">
            {renderSourceError(scans.discoveryError, locale)}
          </p>
          <AppButton onPress={() => void scans.discover()}>
            {t("retryLoad")}
          </AppButton>
        </div>
      )}
      {sources && (
        <p className="my-2 text-xs text-[var(--muted)]">
          {copy.sourceTableHelp}
        </p>
      )}
      <fieldset
        disabled={scanUnavailable && listState === undefined}
        className="m-0 min-w-0 border-0 p-0"
      >
        <SourceRootList
          rows={rows}
          locale={locale}
          state={listState}
          onScan={startScan}
        />
      </fieldset>
      {sources && <SourceArtifactCleanupPanel />}
    </section>
  );
}
