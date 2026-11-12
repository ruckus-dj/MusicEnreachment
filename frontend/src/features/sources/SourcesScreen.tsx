import { useCallback, useEffect, useRef, useState } from "react";
import {
  createSource,
  listSources,
  startSourceScan,
} from "../../api/generated/client";
import type { SourceRootResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathField } from "../../components/DirectoryPathField";
import { useI18n } from "../../i18n/I18nProvider";
import { useHashLocation } from "../../navigation/hashLocation";
import { SourceArtifactCleanupPanel } from "./SourceArtifactCleanupPanel";
import { SourceDetailScreen } from "./SourceDetailScreen";
import { SourceInspectorScreen } from "./SourceInspectorScreen";
import { type SourceMessage, sourceMessage } from "./sourceMessages";
import {
  inventoryAvailabilityNote,
  inventoryEmptyNote,
  inventoryLabel,
  inventoryScopeNote,
  lastScanLabel,
  message,
  renderSourceError,
  sourceError,
  sourceFailure,
  statusLabel,
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
  return route ? <SourcesView route={route} /> : <StandaloneSourcesScreen />;
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

function InventoryCell({
  root,
  locale,
}: {
  root: SourceRootResponse;
  locale: "en" | "ru";
}) {
  const notes = [
    inventoryAvailabilityNote(root, locale),
    inventoryScopeNote(root, locale),
    inventoryEmptyNote(root, locale),
  ].filter((note) => note.length > 0);
  return (
    <>
      {inventoryLabel(root, locale)}
      {notes.map((note) => (
        <span key={note} className="sources-note">
          {note}
        </span>
      ))}
    </>
  );
}

function SourceListScreen() {
  const { locale } = useI18n();
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
  const [formError, setFormError] = useState<ReturnType<typeof message> | null>(
    null,
  );
  const [notice, setNotice] = useState<SourceMessage | null>(null);
  const [partialSuccess, setPartialSuccess] = useState<SourceMessage | null>(
    null,
  );
  const [partialSourceId, setPartialSourceId] = useState("");
  const registering = useRef(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const listHeading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const formAlert = useRef<HTMLParagraphElement>(null);

  const load = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const response = await listSources({ cache: "no-store" });
      if (response.status !== 200) throw sourceFailure("list", response);
      setSources(response.data.sources || []);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    heading.current?.focus();
    void load();
  }, [load]);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  useEffect(() => {
    if (formError) formAlert.current?.focus();
  }, [formError]);

  async function register() {
    if (registering.current) return;
    if (!name.trim() || !path.trim() || !processingMode) {
      setFormError(sourceError("invalidCreate"));
      return;
    }
    registering.current = true;
    setBusy(true);
    setFormError(null);
    setNotice(null);
    setPartialSuccess(null);
    setPartialSourceId("");
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
      try {
        // Registration is an explicit operator action that admits exactly one
        // initial scan. The detail screen adopts this operation by discovery.
        const scan = await startSourceScan(registered.id);
        if (scan.status !== 200) {
          setPartialSuccess({
            key: "manualScanSuccess",
            values: { name: registered.display_name },
          });
          setPartialSourceId(registered.id);
          await load();
          listHeading.current?.focus();
          return;
        }
        window.location.hash = `/sources/${encodeURIComponent(registered.id)}`;
      } catch {
        setPartialSuccess({
          key: "manualScanFailure",
          values: { name: registered.display_name },
        });
        setPartialSourceId(registered.id);
        await load();
        listHeading.current?.focus();
      }
    } catch (reason) {
      setFormError(message(reason));
    } finally {
      registering.current = false;
      setBusy(false);
    }
  }

  function cancelCreate() {
    setCreating(false);
    setFormError(null);
    setPartialSuccess(null);
    setPartialSourceId("");
    setName("");
    setPath("");
    setProcessingMode("");
  }

  return (
    <section
      aria-labelledby="sources-title"
      className="sources-screen mx-auto max-w-5xl py-3"
    >
      <h1 ref={heading} tabIndex={-1} id="sources-title">
        {t("title")}
      </h1>
      <p className="sources-help">{t("description")}</p>
      {busy && sources && <p role="status">{t("requestRunning")}</p>}
      {notice && (
        <p role="status">{sourceMessage(locale, notice.key, notice.values)}</p>
      )}
      {partialSuccess && (
        <div className="sources-partial-success" role="alert">
          <p>
            {sourceMessage(locale, partialSuccess.key, partialSuccess.values)}
          </p>
          <AppButton
            onPress={() => {
              window.location.hash = `/sources/${encodeURIComponent(partialSourceId)}`;
            }}
          >
            {t("openManualScan")}
          </AppButton>
        </div>
      )}
      {error && (
        <>
          <p ref={alert} tabIndex={-1} role="alert">
            {renderSourceError(error, locale)}
          </p>
          <AppButton onPress={() => void load()}>{t("retryLoad")}</AppButton>
        </>
      )}
      {!sources && !error && <p role="status">{t("loading")}</p>}
      {sources && (
        <>
          {creating ? (
            <form
              aria-labelledby="source-create-title"
              className="sources-panel"
              onSubmit={(event) => {
                event.preventDefault();
                void register();
              }}
            >
              <h2 id="source-create-title">{t("createTitle")}</h2>
              <label>
                {t("name")}
                <input
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
              <DirectoryPathField
                id="source-create-path"
                label={t("serverPath")}
                value={path}
                onSelect={setPath}
                disabled={busy}
              />
              <label>
                {t("processingMode")}
                <select
                  className="sources-select"
                  required
                  value={processingMode}
                  onChange={(event) =>
                    setProcessingMode(
                      event.target.value as typeof processingMode,
                    )
                  }
                >
                  <option value="">{t("chooseMode")}</option>
                  <option value="in_place">{t("inPlace")}</option>
                  <option value="staged">{t("staged")}</option>
                </select>
              </label>
              <p className="sources-help">{t("sourcePolicy")}</p>
              {formError && (
                <p ref={formAlert} tabIndex={-1} role="alert">
                  {renderSourceError(formError, locale)}
                </p>
              )}
              <AppButton type="submit" isDisabled={busy}>
                {t("register")}
              </AppButton>
              <AppButton onPress={cancelCreate}>{t("cancel")}</AppButton>
            </form>
          ) : (
            <AppButton
              onPress={() => {
                setFormError(null);
                setNotice(null);
                setCreating(true);
              }}
            >
              {t("addSource")}
            </AppButton>
          )}
          <section
            aria-labelledby="sources-list-title"
            className="sources-panel"
          >
            <h2 ref={listHeading} tabIndex={-1} id="sources-list-title">
              {t("connectedSources")}
            </h2>
            {sources.length === 0 ? (
              <p role="status">{t("noneRegistered")}</p>
            ) : (
              <div className="sources-table-wrap">
                <table className="sources-table">
                  <caption className="sources-visually-hidden">
                    {t("sourceCaption")}
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">{t("nameColumn")}</th>
                      <th scope="col">{t("pathColumn")}</th>
                      <th scope="col">{t("availability")}</th>
                      <th scope="col">{t("enabled")}</th>
                      <th scope="col">{t("lastScan")}</th>
                      <th scope="col">{t("inventory")}</th>
                      <th scope="col">{t("actions")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {sources.map((root) => (
                      <tr key={root.id}>
                        <th scope="row" data-label={t("nameColumn")}>
                          {root.display_name}
                        </th>
                        <td data-label={t("pathColumn")}>
                          <code>{root.configured_path}</code>
                        </td>
                        <td data-label={t("availability")}>
                          <span
                            className="sources-chip"
                            data-status={root.status}
                          >
                            {statusLabel(root.status, locale)}
                          </span>
                          {root.status === "unavailable" && root.safe_error && (
                            <span className="sources-note">
                              {root.safe_error}
                            </span>
                          )}
                        </td>
                        <td data-label={t("enabled")}>
                          {root.enabled ? t("enabledYes") : t("enabledNo")}
                        </td>
                        <td data-label={t("lastScan")}>
                          {lastScanLabel(root, locale)}
                        </td>
                        <td data-label={t("inventory")}>
                          <InventoryCell root={root} locale={locale} />
                        </td>
                        <td data-label={t("actions")}>
                          <AppButton
                            onPress={() => {
                              window.location.hash = `/sources/${encodeURIComponent(root.id)}`;
                            }}
                          >
                            {t("openSource", { name: root.display_name })}
                          </AppButton>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <p className="sources-note">{t("unavailableInventory")}</p>
          </section>
          <SourceArtifactCleanupPanel />
        </>
      )}
    </section>
  );
}
