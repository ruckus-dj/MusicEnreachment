import { useCallback, useEffect, useId, useRef, useState } from "react";
import { browseServerDirectories } from "../api/generated/client";
import type { BrowseDirectoriesBody } from "../api/generated/client.schemas";
import "./DirectoryPicker.css";

export function DirectoryPicker({
  open,
  initialPath,
  onSelect,
  onClose,
}: {
  open: boolean;
  initialPath: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState(initialPath);
  const [listing, setListing] = useState<BrowseDirectoriesBody>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const revision = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const dialog = useRef<HTMLDialogElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const titleId = useId();

  const abortRequest = useCallback(() => {
    revision.current++;
    controller.current?.abort();
    controller.current = undefined;
  }, []);

  useEffect(() => {
    if (!open) return;
    previousFocus.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (!dialog.current?.open) dialog.current?.showModal();
    const id = ++revision.current;
    const requestController = new AbortController();
    controller.current = requestController;
    setPath(initialPath);
    setListing(undefined);
    setError("");
    setLoading(true);
    void browseServerDirectories(
      { path: initialPath || undefined },
      { signal: requestController.signal },
    )
      .then((result) => {
        if (revision.current !== id) return;
        if (result.status !== 200)
          throw new Error("Не удалось прочитать каталог сервера.");
        setListing(result.data);
        setPath(result.data.current_path);
      })
      .catch(() => {
        if (revision.current === id)
          setError("Не удалось прочитать каталог сервера.");
      })
      .finally(() => {
        if (revision.current === id) setLoading(false);
      });
    return () => {
      abortRequest();
      dialog.current?.close();
      previousFocus.current?.focus();
    };
  }, [open, initialPath, abortRequest]);

  async function navigate(target: string, offset = 0) {
    controller.current?.abort();
    const id = ++revision.current;
    const requestController = new AbortController();
    controller.current = requestController;
    setLoading(true);
    setError("");
    try {
      const result = await browseServerDirectories(
        { path: target, offset },
        { signal: requestController.signal },
      );
      if (revision.current !== id) return;
      if (result.status !== 200) throw new Error();
      setListing(result.data);
      setPath(result.data.current_path);
    } catch {
      if (revision.current === id)
        setError("Не удалось прочитать каталог сервера.");
    } finally {
      if (revision.current === id) {
        controller.current = undefined;
        setLoading(false);
      }
    }
  }

  function close() {
    abortRequest();
    dialog.current?.close();
    onClose();
    previousFocus.current?.focus();
  }

  return (
    <dialog
      ref={dialog}
      className="directory-picker"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        close();
      }}
    >
      <h2 id={titleId}>Выбор каталога сервера</h2>
      <label className="directory-picker__path">
        Путь
        <input value={path} onChange={(event) => setPath(event.target.value)} />
      </label>
      <div className="directory-picker__navigation">
        <button
          type="button"
          disabled={loading}
          onClick={() => void navigate(path)}
        >
          Перейти
        </button>
        <button
          type="button"
          disabled={loading}
          onClick={() => void navigate("")}
        >
          Корень сервера
        </button>
      </div>
      {listing && (
        <>
          <p aria-live="polite">Текущий каталог: {listing.current_path}</p>
          <fieldset
            className="directory-picker__roots"
            aria-label="Корневые каталоги"
          >
            {listing.roots?.map((root) => (
              <button
                key={root}
                type="button"
                disabled={loading}
                onClick={() => void navigate(root)}
              >
                {root}
              </button>
            ))}
          </fieldset>
          {listing.current_path !== listing.parent_path && (
            <button
              type="button"
              disabled={loading}
              onClick={() => void navigate(listing.parent_path)}
            >
              На уровень выше
            </button>
          )}
          <ul
            className="directory-picker__directories"
            aria-label="Подкаталоги"
          >
            {listing.directories?.map((directory) => (
              <li key={directory}>
                <button
                  type="button"
                  disabled={loading}
                  onClick={() => void navigate(directory)}
                >
                  {directory.split(/[\\/]/).filter(Boolean).at(-1) || directory}
                </button>
              </li>
            ))}
          </ul>
          {listing.next_offset !== undefined && (
            <button
              type="button"
              disabled={loading}
              onClick={() =>
                void navigate(listing.current_path, listing.next_offset)
              }
            >
              Следующая страница каталогов
            </button>
          )}
        </>
      )}
      {loading && <p role="status">Загрузка…</p>}
      {error && <p role="alert">{error}</p>}
      <div className="directory-picker__actions">
        <button
          type="button"
          disabled={
            loading ||
            Boolean(error) ||
            !listing ||
            path !== listing.current_path
          }
          onClick={() => {
            if (!listing || error) return;
            onSelect(listing.current_path);
            close();
          }}
        >
          Выбрать текущий каталог
        </button>
        <button type="button" onClick={close}>
          Отмена
        </button>
      </div>
    </dialog>
  );
}
