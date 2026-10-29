import { useCallback, useEffect, useId, useRef, useState } from "react";
import { browseServerDirectories } from "../api/generated/client";
import type { BrowseDirectoriesBody } from "../api/generated/client.schemas";
import "./DirectoryPicker.css";

const browseError = "Не удалось прочитать каталог сервера.";

function isWindowsPath(path: string) {
  return (
    /^([A-Za-z]:)[\\/]/.test(path) || /^\\\\[^\\]+\\[^\\]+(?:\\|$)/.test(path)
  );
}

function pathParts(path: string) {
  const unc = /^(\\\\[^\\]+\\[^\\]+)(?:\\|$)/.exec(path);
  const drive = /^([A-Za-z]:)[\\/]/.exec(path);
  const unix = path.startsWith("/");
  const windows = Boolean(unc || drive);
  const root = unc ? `${unc[1]}\\` : drive ? `${drive[1]}\\` : unix ? "/" : "";
  const separator = windows ? "\\" : "/";
  const rest = path
    .slice(root.length)
    .split(windows ? /\\+/ : /\/+/)
    .filter(Boolean);
  const crumbs = root ? [{ label: root, path: root }] : [];
  let current = root;
  for (const segment of rest) {
    current = current
      ? current.endsWith(separator)
        ? `${current}${segment}`
        : `${current}${separator}${segment}`
      : segment;
    crumbs.push({ label: segment, path: current });
  }
  return crumbs;
}

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
  const [editingPath, setEditingPath] = useState(false);
  const [listing, setListing] = useState<BrowseDirectoriesBody>();
  const [directories, setDirectories] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const revision = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const dialog = useRef<HTMLDialogElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const titleId = useId();
  const editPathRef = useRef<HTMLInputElement>(null);

  const abortRequest = useCallback(() => {
    revision.current++;
    controller.current?.abort();
    controller.current = undefined;
  }, []);

  const readPath = useCallback(
    async (target: string, startOffset = 0, append = false) => {
      controller.current?.abort();
      const id = ++revision.current;
      const requestController = new AbortController();
      controller.current = requestController;
      setLoading(true);
      setError("");
      if (!append) {
        setListing(undefined);
        setDirectories([]);
      }
      let offset = startOffset;
      let responsePath = append ? target : undefined;
      try {
        while (true) {
          const result = await browseServerDirectories(
            { path: target || undefined, offset },
            { signal: requestController.signal },
          );
          if (revision.current !== id) return;
          if (result.status !== 200) throw new Error(browseError);
          const page = result.data;
          if (page.offset !== offset)
            throw new Error("Unexpected directory offset");
          if (responsePath && page.current_path !== responsePath) {
            throw new Error("Directory path changed while paging");
          }
          responsePath = page.current_path;
          if (!append && offset === startOffset) {
            setListing(page);
            setPath(page.current_path);
            setEditingPath(false);
            setDirectories(page.directories ?? []);
          } else {
            setListing(page);
            setDirectories((existing) => [
              ...existing,
              ...(page.directories ?? []),
            ]);
          }
          const nextOffset = page.next_offset;
          if (nextOffset === undefined) break;
          if (!Number.isSafeInteger(nextOffset) || nextOffset <= offset) {
            throw new Error("Invalid directory page cursor");
          }
          offset = nextOffset;
        }
      } catch {
        if (revision.current === id) setError(browseError);
      } finally {
        if (revision.current === id) {
          controller.current = undefined;
          setLoading(false);
        }
      }
    },
    [],
  );

  useEffect(() => {
    if (!open) return;
    previousFocus.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (!dialog.current?.open) dialog.current?.showModal();
    setPath(initialPath);
    setEditingPath(false);
    setListing(undefined);
    setDirectories([]);
    void readPath(initialPath);
    return () => {
      abortRequest();
      dialog.current?.close();
      previousFocus.current?.focus();
    };
  }, [open, initialPath, abortRequest, readPath]);

  useEffect(() => {
    if (editingPath) editPathRef.current?.focus();
  }, [editingPath]);

  function close() {
    abortRequest();
    dialog.current?.close();
    onClose();
    previousFocus.current?.focus();
  }

  const crumbs = pathParts(listing?.current_path ?? path);
  const currentPath = listing?.current_path ?? path;
  const windowsPath = isWindowsPath(currentPath);
  const pathSeparator = windowsPath ? /\\+/ : /\/+/;
  const otherRoots =
    listing && listing.current_path === listing.parent_path
      ? (listing.roots ?? []).filter((root) => root !== listing.current_path)
      : [];

  return (
    <dialog
      ref={dialog}
      className="directory-picker"
      aria-labelledby={titleId}
      onClose={(event) => event.stopPropagation()}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        close();
      }}
    >
      <div className="directory-picker__header">
        <h2 id={titleId}>Папка на сервере</h2>
        <p>Выберите папку, доступную серверу MeloTrove.</p>
        <button
          className="directory-picker__close"
          type="button"
          aria-label="Закрыть"
          onClick={close}
        >
          ×
        </button>
      </div>
      <nav className="directory-picker__navigation" aria-label="Путь к папке">
        <button
          className="directory-picker__up"
          type="button"
          aria-label="На уровень выше"
          disabled={
            loading || !listing || listing.current_path === listing.parent_path
          }
          onClick={() => void readPath(listing?.parent_path ?? "")}
        >
          ↑
        </button>
        <ol className="directory-picker__breadcrumbs">
          {crumbs.map((crumb, index) => (
            <li key={crumb.path}>
              {index === crumbs.length - 1 ? (
                <button
                  type="button"
                  className="directory-picker__current-crumb"
                  aria-label={`Изменить путь: ${listing?.current_path ?? path}`}
                  onClick={() => {
                    setPath(currentPath);
                    setEditingPath(true);
                  }}
                >
                  {crumb.label}
                </button>
              ) : (
                <button
                  type="button"
                  disabled={loading}
                  onClick={() => void readPath(crumb.path)}
                >
                  {crumb.label}
                </button>
              )}
            </li>
          ))}
          {!crumbs.length && (
            <li>
              <button
                type="button"
                aria-label={path ? `Изменить путь: ${path}` : "Ввести путь"}
                onClick={() => {
                  setPath(currentPath);
                  setEditingPath(true);
                }}
              >
                {path ? "Изменить путь" : "Ввести путь"}
              </button>
            </li>
          )}
        </ol>
      </nav>
      {editingPath && (
        <div className="directory-picker__path-editor">
          <label htmlFor={`${titleId}-path`}>Путь к папке</label>
          <input
            ref={editPathRef}
            id={`${titleId}-path`}
            value={path}
            onChange={(event) => setPath(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                event.stopPropagation();
                void readPath(path);
              } else if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                setPath(listing?.current_path ?? initialPath);
                setEditingPath(false);
              }
            }}
          />
        </div>
      )}
      <section
        className="directory-picker__listing"
        aria-label="Папки"
        onScroll={(event) => {
          const element = event.currentTarget;
          if (
            !loading &&
            !error &&
            listing?.next_offset !== undefined &&
            element.scrollHeight - element.scrollTop - element.clientHeight < 40
          ) {
            void readPath(listing.current_path, listing.next_offset, true);
          }
        }}
      >
        {otherRoots.length > 0 && (
          <>
            <h3>Корни сервера</h3>
            <ul className="directory-picker__directories">
              {otherRoots.map((root) => (
                <li key={root}>
                  <button
                    type="button"
                    disabled={loading}
                    onClick={() => void readPath(root)}
                  >
                    <span aria-hidden="true">📁</span>
                    {root}
                  </button>
                </li>
              ))}
            </ul>
          </>
        )}
        {directories.length > 0 ? (
          <ul className="directory-picker__directories">
            {directories.map((directory) => (
              <li key={directory}>
                <button
                  type="button"
                  disabled={loading}
                  onClick={() => void readPath(directory)}
                >
                  <span aria-hidden="true">📁</span>
                  {directory.split(pathSeparator).filter(Boolean).at(-1) ||
                    directory}
                </button>
              </li>
            ))}
          </ul>
        ) : listing && !loading && !error ? (
          <p className="directory-picker__empty">В этой папке нет подпапок.</p>
        ) : null}
        {loading && (
          <p className="directory-picker__status" role="status">
            Загрузка папок…
          </p>
        )}
        {error && (
          <div className="directory-picker__error">
            <p role="alert">{error}</p>
            <button
              type="button"
              onClick={() =>
                void readPath(
                  listing?.current_path ?? path,
                  listing?.next_offset ?? 0,
                  Boolean(listing),
                )
              }
            >
              Повторить
            </button>
          </div>
        )}
      </section>
      <footer className="directory-picker__actions">
        <button type="button" onClick={close}>
          Отмена
        </button>
        <button
          type="button"
          disabled={
            loading ||
            Boolean(error) ||
            !listing ||
            path !== listing.current_path
          }
          onClick={() => {
            if (!listing || error || loading || path !== listing.current_path)
              return;
            onSelect(listing.current_path);
            close();
          }}
        >
          Выбрать папку
        </button>
      </footer>
    </dialog>
  );
}
