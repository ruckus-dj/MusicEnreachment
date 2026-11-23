import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  deleteSource,
  getSource,
  updateSource,
} from "../../api/generated/client";
import type { SourceRootResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathField } from "../../components/DirectoryPathField";
import { useI18n } from "../../i18n/I18nProvider";
import { incomingGroupCountQueryKey } from "../incoming/incomingGroupsQueryKeys";
import { SourceLocations } from "./SourceLocations";
import { SourceRootHeadingView } from "./SourceRootHeadingView";
import { SourceRootRemovalDialog } from "./SourceRootRemovalDialog";
import { SourceScanControl } from "./SourceScanControl";
import { type SourceMessage, sourceMessage } from "./sourceMessages";
import { useSourceScans } from "./sourceScans";
import {
  inventoryLabel,
  lastScanLabel,
  message,
  renderSourceError,
  sourceError,
  sourceFailure,
  statusLabel,
} from "./sourcesApi";
import "./sources.css";

type EditForm = {
  displayName: string;
  path: string;
  processingMode: "in_place" | "staged";
};

export function SourceDetailScreen({ sourceId }: { sourceId: string }) {
  const { locale } = useI18n();
  const queryClient = useQueryClient();
  const scanRevision = useSourceScans().rootRevision(sourceId);
  const rootRead = useRef<AbortController | null>(null);
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const [root, setRoot] = useState<SourceRootResponse>();
  // Every authoritative root update also re-reads the published inventory: a
  // successful scan publishes a new generation, and a path edit changes what
  // the listed paths are relative to. This token is that re-read trigger.
  const [refresh, setRefresh] = useState(0);
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<EditForm>({
    displayName: "",
    path: "",
    processingMode: "in_place",
  });
  const [removal, setRemoval] = useState<{
    sourceId: string;
    path: string;
    locationCount: number;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ReturnType<typeof message> | null>(null);
  const [formError, setFormError] = useState<ReturnType<typeof message> | null>(
    null,
  );
  const [deleteError, setDeleteError] = useState<ReturnType<
    typeof message
  > | null>(null);
  const [notice, setNotice] = useState<SourceMessage | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const formAlert = useRef<HTMLParagraphElement>(null);

  const applyRoot = useCallback((next: SourceRootResponse) => {
    setRoot(next);
    setRefresh((value) => value + 1);
  }, []);

  // The shell mirrors the visible group count outside the Incoming screens. A
  // root edit or removal can leave that number stale, so refresh only
  // the exact count entry instead of the review screen's group-list request.
  const refreshIncomingGroupCount = useCallback(() => {
    void queryClient.invalidateQueries({
      queryKey: incomingGroupCountQueryKey(),
      exact: true,
    });
  }, [queryClient]);

  const load = useCallback(async () => {
    rootRead.current?.abort();
    const request = new AbortController();
    rootRead.current = request;
    setBusy(true);
    setError(null);
    try {
      const response = await getSource(sourceId, {
        cache: "no-store",
        signal: request.signal,
      });
      if (request.signal.aborted) return;
      if (response.status !== 200) throw sourceFailure("read", response);
      applyRoot(response.data);
    } catch (reason) {
      if (!request.signal.aborted) setError(message(reason));
    } finally {
      if (!request.signal.aborted) {
        rootRead.current = null;
        setBusy(false);
      }
    }
  }, [sourceId, applyRoot]);

  useEffect(() => {
    heading.current?.focus();
    return () => rootRead.current?.abort();
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: A scan revision explicitly requests an authoritative reread.
  useEffect(() => {
    void load();
  }, [load, scanRevision]);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  useEffect(() => {
    if (formError) formAlert.current?.focus();
  }, [formError]);
  function backToList() {
    window.location.hash = "/sources";
  }

  function openEdit() {
    if (!root) return;
    setForm({
      displayName: root.display_name,
      path: root.configured_path,
      processingMode: root.processing_mode,
    });
    setFormError(null);
    setNotice(null);
    setEditing(true);
  }

  async function save() {
    if (!form.displayName.trim() || !form.path.trim()) {
      setFormError(sourceError("invalidEdit"));
      return;
    }
    setBusy(true);
    setFormError(null);
    setNotice(null);
    try {
      const response = await updateSource(sourceId, {
        display_name: form.displayName.trim(),
        configured_path: form.path.trim(),
        processing_mode: form.processingMode,
      });
      if (response.status !== 200) throw sourceFailure("edit", response);
      applyRoot(response.data);
      refreshIncomingGroupCount();
      setEditing(false);
      setNotice(
        response.data.stale && response.data.inventory_path
          ? {
              key: "sourceSavedStale",
              values: { path: response.data.inventory_path },
            }
          : { key: "sourceSaved" },
      );
    } catch (reason) {
      setFormError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  function closeDelete() {
    setDeleteError(null);
    setRemoval(null);
  }

  async function confirmDelete() {
    if (!removal || busy) return;
    setBusy(true);
    setDeleteError(null);
    try {
      const response = await deleteSource(removal.sourceId, {
        confirmed_path: removal.path,
        confirmed_location_count: removal.locationCount,
      });
      if (response.status !== 204) throw sourceFailure("delete", response);
      refreshIncomingGroupCount();
      setRemoval(null);
      backToList();
    } catch (reason) {
      setDeleteError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section
      aria-labelledby="source-title"
      className="sources-screen mx-auto max-w-5xl py-3"
    >
      <SourceRootHeadingView
        title={root ? root.display_name : t("sourceTitle")}
        headingRef={heading}
        headingId="source-title"
        back={{ label: t("backToSources"), onPress: backToList }}
      />
      {busy && root && <p role="status">{t("requestRunning")}</p>}
      {!root && !error && <p role="status">{t("loadingLocations")}</p>}
      {error && (
        <>
          <p ref={alert} tabIndex={-1} role="alert">
            {renderSourceError(error, locale)}
          </p>
          <AppButton onPress={() => void load()}>{t("retryLoad")}</AppButton>
        </>
      )}
      {notice && (
        <p role="status">{sourceMessage(locale, notice.key, notice.values)}</p>
      )}
      {root && (
        <>
          <section
            aria-labelledby="source-summary-title"
            className="sources-panel"
          >
            <h2 id="source-summary-title">{t("sourceSummary")}</h2>
            <dl className="sources-summary">
              <div>
                <dt>{t("serverPath")}</dt>
                <dd>
                  <code>{root.configured_path}</code>
                </dd>
              </div>
              <div>
                <dt>{t("availabilityLabel")}</dt>
                <dd>
                  <span className="sources-chip" data-status={root.status}>
                    {statusLabel(root.status, locale)}
                  </span>
                  {root.status === "unavailable" && root.safe_error && (
                    <span className="sources-note">{root.safe_error}</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>{t("enabled")}</dt>
                <dd>{root.enabled ? t("yesNewScans") : t("noNewScans")}</dd>
              </div>
              <div>
                <dt>{t("lastSuccessfulScan")}</dt>
                <dd>{lastScanLabel(root, locale)}</dd>
              </div>
              <div>
                <dt>{t("inventory")}</dt>
                <dd>{inventoryLabel(root, locale)}</dd>
              </div>
              {root.inventory_path && (
                <div>
                  <dt>{t("inventoryPath")}</dt>
                  <dd>
                    <code>{root.inventory_path}</code>
                    {root.stale && (
                      <span className="sources-note">{t("previousPath")}</span>
                    )}
                  </dd>
                </div>
              )}
            </dl>
            <p className="sources-help">{t("sourceReadOnly")}</p>
          </section>
          <SourceScanControl root={root} />
          <SourceLocations
            sourceId={sourceId}
            root={root}
            refreshToken={refresh}
          />
          <section
            aria-labelledby="source-edit-title"
            className="sources-panel"
          >
            <h2 id="source-edit-title">{t("editTitle")}</h2>
            {editing ? (
              <form
                aria-labelledby="source-edit-title"
                onSubmit={(event) => {
                  event.preventDefault();
                  void save();
                }}
              >
                <label>
                  {t("name")}
                  <input
                    value={form.displayName}
                    onChange={(event) =>
                      setForm({ ...form, displayName: event.target.value })
                    }
                  />
                </label>
                <DirectoryPathField
                  id="source-edit-path"
                  label={t("serverPath")}
                  value={form.path}
                  onSelect={(path) => setForm({ ...form, path })}
                  disabled={busy}
                />
                <label>
                  {t("processingMode")}
                  <select
                    className="sources-select"
                    required
                    value={form.processingMode}
                    onChange={(event) =>
                      setForm({
                        ...form,
                        processingMode: event.target
                          .value as EditForm["processingMode"],
                      })
                    }
                  >
                    <option value="in_place">{t("inPlace")}</option>
                    <option value="staged">{t("staged")}</option>
                  </select>
                </label>
                <p className="sources-help">{t("modeChange")}</p>
                <p className="sources-help">{t("pathChange")}</p>
                {formError && (
                  <p ref={formAlert} tabIndex={-1} role="alert">
                    {renderSourceError(formError, locale)}
                  </p>
                )}
                <AppButton type="submit" isDisabled={busy}>
                  {t("saveSource")}
                </AppButton>
                <AppButton onPress={() => setEditing(false)}>
                  {t("cancel")}
                </AppButton>
              </form>
            ) : (
              <AppButton onPress={openEdit}>{t("editSource")}</AppButton>
            )}
          </section>
          <section
            aria-labelledby="source-delete-title"
            className="sources-panel sources-danger"
          >
            <h2 id="source-delete-title">{t("deleteTitle")}</h2>
            <p>{t("deleteExplanation")}</p>
            <AppButton
              onPress={() => {
                setDeleteError(null);
                setRemoval({
                  sourceId,
                  path: root.configured_path,
                  locationCount: root.location_count,
                });
              }}
            >
              {t("deleteSource")}
            </AppButton>
          </section>
        </>
      )}
      {removal && (
        <SourceRootRemovalDialog
          locale={locale}
          path={removal.path}
          count={removal.locationCount}
          busy={busy}
          error={
            deleteError ? renderSourceError(deleteError, locale) : undefined
          }
          onCancel={closeDelete}
          onConfirm={() => void confirmDelete()}
        />
      )}
    </section>
  );
}
