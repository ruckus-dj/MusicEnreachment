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
import { SourceLocations } from "./SourceLocations";
import { SourceScanControl } from "./SourceScanControl";
import {
  fileCount,
  inventoryLabel,
  lastScanLabel,
  message,
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
  const [error, setError] = useState("");
  const [formError, setFormError] = useState("");
  const [deleteError, setDeleteError] = useState("");
  const [notice, setNotice] = useState("");
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
    setError("");
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
    setFormError("");
    setNotice("");
    setEditing(true);
  }

  async function save() {
    if (!form.displayName.trim() || !form.path.trim()) {
      setFormError("Укажите имя каталога и абсолютный путь на сервере.");
      return;
    }
    setBusy(true);
    setFormError("");
    setNotice("");
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
          ? `Каталог сохранён. Инвентарь относится к прежнему пути ${response.data.inventory_path} и заменится после успешного сканирования нового пути.`
          : "Каталог сохранён.",
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
    setDeleteError("");
    if (dialog.current?.open) dialog.current.close();
    else {
      setDeleting(false);
      restoreTrigger();
    }
  }

  async function confirmDelete() {
    if (!root) return;
    setBusy(true);
    setDeleteError("");
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
        {root ? root.display_name : "Каталог источника"}
      </h1>
      <AppButton onPress={backToList}>К списку каталогов</AppButton>
      {busy && root && <p role="status">Выполняется запрос…</p>}
      {!root && !error && <p role="status">Загрузка каталога…</p>}
      {error && (
        <>
          <p ref={alert} tabIndex={-1} role="alert">
            {error}
          </p>
          <AppButton onPress={() => void load()}>Повторить загрузку</AppButton>
        </>
      )}
      {notice && <p role="status">{notice}</p>}
      {root && (
        <>
          <section
            aria-labelledby="source-summary-title"
            className="sources-panel"
          >
            <h2 id="source-summary-title">Каталог</h2>
            <dl className="sources-summary">
              <div>
                <dt>Серверный путь</dt>
                <dd>
                  <code>{root.configured_path}</code>
                </dd>
              </div>
              <div>
                <dt>Доступность</dt>
                <dd>
                  <span className="sources-chip" data-status={root.status}>
                    {statusLabel(root.status)}
                  </span>
                  {root.status === "unavailable" && root.safe_error && (
                    <span className="sources-note">{root.safe_error}</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>Включён</dt>
                <dd>
                  {root.enabled
                    ? "Да: новые сканирования разрешены."
                    : "Нет: новые сканирования запрещены."}
                </dd>
              </div>
              <div>
                <dt>Последнее успешное сканирование</dt>
                <dd>{lastScanLabel(root)}</dd>
              </div>
              <div>
                <dt>Инвентарь</dt>
                <dd>{inventoryLabel(root)}</dd>
              </div>
              {root.inventory_path && (
                <div>
                  <dt>Путь инвентаря</dt>
                  <dd>
                    <code>{root.inventory_path}</code>
                    {root.stale && (
                      <span className="sources-note">
                        Это прежний путь: инвентарь заменится после успешного
                        сканирования текущего пути.
                      </span>
                    )}
                  </dd>
                </div>
              )}
            </dl>
            <p className="sources-help">
              Каталог принадлежит серверу: MeloTrove читает его только для
              чтения и ничего в него не записывает.
            </p>
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
            <h2 id="source-edit-title">Имя, путь и режим</h2>
            {editing ? (
              <form
                aria-labelledby="source-edit-title"
                onSubmit={(event) => {
                  event.preventDefault();
                  void save();
                }}
              >
                <label>
                  Имя каталога
                  <input
                    value={form.displayName}
                    onChange={(event) =>
                      setForm({ ...form, displayName: event.target.value })
                    }
                  />
                </label>
                <label>
                  Путь на сервере
                  <input
                    value={form.path}
                    onChange={(event) =>
                      setForm({ ...form, path: event.target.value })
                    }
                  />
                </label>
                <label>
                  Режим обработки
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
                    <option value="in_place">in_place</option>
                    <option value="staged">staged</option>
                  </select>
                </label>
                <p className="sources-help">
                  Смена режима влияет только на будущую обработку и не запускает
                  повторный анализ текущего инвентаря.
                </p>
                <p className="sources-help">
                  Путь принадлежит серверу, а не этому компьютеру. Смена пути не
                  удаляет инвентарь: прежние записи остаются видимыми как
                  относящиеся к прежнему пути, пока новое сканирование не
                  завершится успешно.
                </p>
                {formError && (
                  <p ref={formAlert} tabIndex={-1} role="alert">
                    {formError}
                  </p>
                )}
                <AppButton type="submit" isDisabled={busy}>
                  Сохранить каталог
                </AppButton>
                <AppButton onPress={() => setEditing(false)}>Отмена</AppButton>
              </form>
            ) : (
              <AppButton onPress={openEdit}>Изменить каталог</AppButton>
            )}
          </section>
          <section
            aria-labelledby="source-delete-title"
            className="sources-panel sources-danger"
          >
            <h2 id="source-delete-title">Удаление каталога</h2>
            <p>
              Удаляются только записи инвентаря этого каталога в базе MeloTrove.
              Исходные файлы в каталоге источника и медиатека в output останутся
              на месте. Во время активной операции с каталогом сервер отклоняет
              удаление.
            </p>
            <AppButton
              onPress={(event) => {
                dialogTrigger.current = event.target as HTMLElement;
                setDeleteError("");
                setDeleting(true);
              }}
            >
              Удалить каталог
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
            Удалить инвентарь каталога «{root.display_name}»?
          </h2>
          <p>
            Будут удалены записи инвентаря: {fileCount(root.location_count)} по
            серверному пути <code>{root.configured_path}</code>.
          </p>
          <p>
            Исходные файлы в каталоге источника и медиатека в output останутся
            на месте: MeloTrove удаляет только свои записи.
          </p>
          {deleteError && (
            <p ref={deleteAlert} tabIndex={-1} role="alert">
              {deleteError}
            </p>
          )}
          <AppButton onPress={closeDelete}>Отмена</AppButton>
          <AppButton isDisabled={busy} onPress={() => void confirmDelete()}>
            Подтвердить удаление
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
