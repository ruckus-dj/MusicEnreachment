import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import {
  deleteSource,
  getSource,
  updateSource,
} from "../../api/generated/client";
import type { SourceRootResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathField } from "../../components/DirectoryPathField";
import { useI18n } from "../../i18n/I18nProvider";
import { SourceLocations } from "./SourceLocations";
import { SourceScanControl } from "./SourceScanControl";
import { type SourceMessage, sourceMessage } from "./sourceMessages";
import {
  fileCount,
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
  const [deleting, setDeleting] = useState(false);
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
  const deleteAlert = useRef<HTMLParagraphElement>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const dialogTrigger = useRef<HTMLElement | null>(null);

  const applyRoot = useCallback((next: SourceRootResponse) => {
    setRoot(next);
    setRefresh((value) => value + 1);
  }, []);

  const load = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const response = await getSource(sourceId, { cache: "no-store" });
      if (response.status !== 200) throw sourceFailure("read", response);
      applyRoot(response.data);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }, [sourceId, applyRoot]);

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
  useEffect(() => {
    if (deleteError) deleteAlert.current?.focus();
  }, [deleteError]);
  useLayoutEffect(() => {
    if (deleting) showDialog(dialog.current);
  }, [deleting]);

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

  function restoreTrigger() {
    const trigger = dialogTrigger.current;
    dialogTrigger.current = null;
    trigger?.focus();
  }

  function closeDelete() {
    setDeleteError(null);
    if (dialog.current?.open) dialog.current.close();
    else {
      setDeleting(false);
      restoreTrigger();
    }
  }

  async function confirmDelete() {
    if (!root) return;
    setBusy(true);
    setDeleteError(null);
    try {
      const response = await deleteSource(sourceId, {
        confirmed_path: root.configured_path,
        confirmed_location_count: root.location_count,
      });
      if (response.status !== 204) throw sourceFailure("delete", response);
      setDeleting(false);
      dialogTrigger.current = null;
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
      <h1 ref={heading} tabIndex={-1} id="source-title">
        {root ? root.display_name : t("sourceTitle")}
      </h1>
      <AppButton onPress={backToList}>{t("backToSources")}</AppButton>
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
          <SourceScanControl root={root} onScanCompleted={load} />
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
              onPress={(event) => {
                dialogTrigger.current = event.target as HTMLElement;
                setDeleteError(null);
                setDeleting(true);
              }}
            >
              {t("deleteSource")}
            </AppButton>
          </section>
        </>
      )}
      {deleting && root && (
        <dialog
          ref={dialog}
          aria-modal="true"
          aria-labelledby="source-delete-dialog-title"
          className="sources-dialog"
          onClose={() => {
            setDeleting(false);
            restoreTrigger();
          }}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              closeDelete();
            }
          }}
          onCancel={(event) => {
            event.preventDefault();
            closeDelete();
          }}
        >
          <h2 id="source-delete-dialog-title">
            {t("deleteConfirmTitle", { name: root.display_name })}
          </h2>
          <p>
            {t("deleteConfirmBody", {
              count: fileCount(root.location_count, locale),
              path: root.configured_path,
            })}
          </p>
          <p>{t("sourceFilesRemain")}</p>
          {deleteError && (
            <p ref={deleteAlert} tabIndex={-1} role="alert">
              {renderSourceError(deleteError, locale)}
            </p>
          )}
          <AppButton onPress={closeDelete}>{t("cancel")}</AppButton>
          <AppButton isDisabled={busy} onPress={() => void confirmDelete()}>
            {t("confirmDelete")}
          </AppButton>
        </dialog>
      )}
    </section>
  );
}

function showDialog(element: HTMLDialogElement | null) {
  if (!element || element.open) return;
  element.showModal();
  element
    .querySelector<HTMLElement>("button:not(:disabled), input:not(:disabled)")
    ?.focus();
}
