import { useCallback, useEffect, useRef, useState } from "react";
import { createSource, listSources } from "../../api/generated/client";
import type { SourceRootResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { browseServerDirectory } from "../../components/DirectoryPicker";
import { useI18n } from "../../i18n/I18nProvider";
import { useHashLocation } from "../../navigation/hashLocation";
import { SourceArtifactCleanupPanel } from "./SourceArtifactCleanupPanel";
import { SourceDetailScreen } from "./SourceDetailScreen";
import { SourceInspectorScreen } from "./SourceInspectorScreen";
import {
  type SourceRegistrationErrorKey,
  SourceRegistrationView,
  sourceRegistrationMessage,
  sourceRegistrationMessages,
  useSourceRegistrationFocus,
} from "./SourceRegistrationView";
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
  sourceSettingsMutationKey,
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

// Registration requires an explicit mode. The path belongs to the server and
// is chosen through the shared directory field; registration never probes,
// scans, or invents a scan result.
type RegistrationError =
  | SourceRegistrationErrorKey
  | ReturnType<typeof message>;

function SourceListScreen() {
  const { locale } = useI18n();
  const scans = useSourceScans();
  const pendingSettings = useIsMutating({
    mutationKey: sourceSettingsMutationKey,
  });
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const [sources, setSources] = useState<SourceRootResponse[]>();
  const [creating, setCreating] = useState(false);
  const registrationFocus = useSourceRegistrationFocus(creating);
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
    // Returning from the detail screen during a PATCH must not leave this
    // independently rendered list showing the pre-write source settings.
    if (pendingSettings) return;
    void load();
    return () => loadRequest.current?.abort();
  }, [load, scans.revision, pendingSettings]);

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
        configured_path: path,
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
    registrationFocus.restoreOnClose();
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

  const registrationError = formError
    ? typeof formError === "string"
      ? sourceRegistrationMessage(locale, formError)
      : renderSourceError(formError, locale)
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
            id={registrationFocus.triggerId}
            onPress={() => {
              setFormError(null);
              setCreating(true);
            }}
          >
            {sourceRegistrationMessages[locale].addSource}
          </AppButton>
        )}
      </header>
      {creating && (
        <SourceRegistrationView
          locale={locale}
          name={name}
          path={path}
          mode={processingMode}
          saving={saving}
          error={registrationError}
          browse={browseServerDirectory}
          onNameChange={setName}
          onPathChange={setPath}
          onModeChange={setProcessingMode}
          onSubmit={() => void register()}
          onCancel={cancelCreate}
        />
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
          {sourceRegistrationMessages[locale].sourceTableHelp}
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

import { useIsMutating } from "@tanstack/react-query";
