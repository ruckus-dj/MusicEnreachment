import { useCallback, useEffect, useId, useRef, useState } from "react";
import { AppButton } from "./AppButton";
import {
  type DirectoryPickerLocale,
  directoryPickerMessages,
} from "./DirectoryPickerMessages";

export type DirectoryPage = {
  currentPath: string;
  parentPath: string;
  directories: readonly string[];
  roots?: readonly string[];
  offset: number;
  nextOffset?: number;
};

export type BrowseDirectory = (
  path: string,
  offset: number,
  signal: AbortSignal,
) => Promise<DirectoryPage>;

export class DirectoryBrowseError extends Error {
  constructor(readonly kind: "notFound" | "unreadable") {
    super(kind);
  }
}

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

const dialogClass =
  "m-auto hidden max-h-[min(82dvh,720px)] w-[min(92vw,620px)] gap-[14px] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-[18px] text-[13px]/[1.4] text-[var(--ink)] shadow-[0_16px_48px_#0003] backdrop:bg-black/50 open:grid max-[600px]:max-h-[calc(100dvh-24px)] max-[600px]:w-[calc(100vw-20px)] max-[600px]:p-[14px]";

const directoryClass =
  "flex! min-h-[44px] w-full justify-start! gap-[10px] rounded-none! border-0! border-b! border-[var(--line)]! bg-transparent! px-[10px]! py-[10px]! text-left [overflow-wrap:anywhere] hover:bg-[var(--raised)]!";

const pathInputClass =
  "min-h-[38px] w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-[10px] py-2 text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";

export function DirectoryPickerView({
  open,
  initialPath,
  locale,
  browse,
  onSelect,
  onClose,
}: {
  open: boolean;
  initialPath: string;
  locale: DirectoryPickerLocale;
  browse: BrowseDirectory;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState(initialPath);
  const [editingPath, setEditingPath] = useState(false);
  const [listing, setListing] = useState<DirectoryPage>();
  const [directories, setDirectories] = useState<string[]>([]);
  const [error, setError] = useState<DirectoryBrowseError["kind"] | "error">();
  const [loading, setLoading] = useState(false);
  const copy = directoryPickerMessages[locale];
  const revision = useRef(0);
  const lastConfirmedPath = useRef(initialPath);
  const retryRequest = useRef<{
    path: string;
    offset: number;
    append: boolean;
  }>(undefined);
  const pressedBackdrop = useRef(false);
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
      retryRequest.current = undefined;
      setLoading(true);
      setError(undefined);
      if (!append) {
        setListing(undefined);
        setDirectories([]);
      }
      let offset = startOffset;
      let responsePath = append ? target : undefined;
      try {
        while (true) {
          const page = await browse(target, offset, requestController.signal);
          if (revision.current !== id) return;
          if (page.offset !== offset)
            throw new Error("Unexpected directory offset");
          if (responsePath && page.currentPath !== responsePath) {
            throw new Error("Directory path changed while paging");
          }
          responsePath = page.currentPath;
          if (!append && offset === startOffset) {
            lastConfirmedPath.current = page.currentPath;
            setListing(page);
            setPath(page.currentPath);
            setEditingPath(false);
            setDirectories([...page.directories]);
          } else {
            setListing(page);
            setDirectories((existing) => [...existing, ...page.directories]);
          }
          const nextOffset = page.nextOffset;
          if (nextOffset === undefined) break;
          if (!Number.isSafeInteger(nextOffset) || nextOffset <= offset) {
            throw new Error("Invalid directory page cursor");
          }
          offset = nextOffset;
        }
      } catch (reason) {
        if (revision.current === id) {
          const resume = append || offset !== startOffset;
          retryRequest.current = {
            path: resume ? (responsePath ?? target) : target,
            offset,
            append: resume,
          };
          setError(
            reason instanceof DirectoryBrowseError ? reason.kind : "error",
          );
        }
      } finally {
        if (revision.current === id) {
          controller.current = undefined;
          setLoading(false);
        }
      }
    },
    [browse],
  );

  useEffect(() => {
    if (!open) return;
    previousFocus.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (!dialog.current?.open) dialog.current?.showModal();
    lastConfirmedPath.current = initialPath;
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

  function focusEditTrigger() {
    dialog.current
      ?.querySelector<HTMLElement>("[data-directory-picker-edit]")
      ?.focus();
  }

  const crumbs = pathParts(listing?.currentPath ?? path);
  const currentPath = listing?.currentPath ?? path;
  const windowsPath = isWindowsPath(currentPath);
  const pathSeparator = windowsPath ? /\\+/ : /\/+/;
  const otherRoots =
    listing && listing.currentPath === listing.parentPath
      ? (listing.roots ?? []).filter((root) => root !== listing.currentPath)
      : [];
  const errorId = `${titleId}-error`;
  const editorInvalid = editingPath && Boolean(error);

  function startEditing() {
    setPath(currentPath);
    setEditingPath(true);
  }

  function outsideDialog(element: HTMLDialogElement, x: number, y: number) {
    const { left, right, top, bottom } = element.getBoundingClientRect();
    return x < left || x >= right || y < top || y >= bottom;
  }

  return (
    <dialog
      ref={dialog}
      className={`${dialogClass} ${
        editingPath
          ? "grid-rows-[auto_auto_auto_minmax(0,1fr)_auto]"
          : "grid-rows-[auto_auto_minmax(0,1fr)_auto]"
      }`}
      aria-labelledby={titleId}
      aria-describedby={`${titleId}-help`}
      onClose={(event) => event.stopPropagation()}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        close();
      }}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || event.defaultPrevented) return;
        event.preventDefault();
        event.stopPropagation();
        close();
      }}
      onPointerDown={(event) => {
        pressedBackdrop.current =
          event.target === event.currentTarget &&
          outsideDialog(event.currentTarget, event.clientX, event.clientY);
      }}
      onClick={(event) => {
        if (
          pressedBackdrop.current &&
          event.target === event.currentTarget &&
          outsideDialog(event.currentTarget, event.clientX, event.clientY)
        ) {
          close();
        }
        pressedBackdrop.current = false;
      }}
    >
      <div className="relative grid gap-[3px] border-b border-[var(--line)] pb-3 pr-12">
        <h2 id={titleId} className="m-0 text-base font-semibold">
          {copy.title}
        </h2>
        <p id={`${titleId}-help`} className="m-0 text-[var(--muted)]">
          {copy.help}
        </p>
        <AppButton
          className="absolute top-0 right-0 h-9 w-9 px-0! text-xl"
          type="button"
          aria-label={copy.close}
          onPress={close}
        >
          ×
        </AppButton>
      </div>
      <nav
        className="flex min-h-[56px] items-center gap-2 rounded-md border border-[var(--line)] bg-[var(--raised)] p-[9px]"
        aria-label={copy.navigation}
      >
        <AppButton
          className="w-9 flex-none px-0! text-[19px]"
          type="button"
          aria-label={copy.up}
          isDisabled={
            loading || !listing || listing.currentPath === listing.parentPath
          }
          onPress={() => void readPath(listing?.parentPath ?? "")}
        >
          ↑
        </AppButton>
        <ol className="m-0 flex min-w-0 list-none items-center gap-[5px] overflow-x-auto p-0 whitespace-nowrap [&>li+li]:before:mr-[5px] [&>li+li]:before:text-[var(--muted)] [&>li+li]:before:content-['/']">
          {crumbs.map((crumb, index) => (
            <li key={crumb.path}>
              {index === crumbs.length - 1 ? (
                <AppButton
                  type="button"
                  className="border-transparent! bg-transparent! px-1.5!"
                  data-directory-picker-edit=""
                  aria-current="location"
                  aria-label={`${copy.editPath}: ${currentPath}`}
                  onPress={startEditing}
                >
                  {crumb.label}
                </AppButton>
              ) : (
                <AppButton
                  type="button"
                  className="border-transparent! bg-transparent! px-1.5!"
                  isDisabled={loading}
                  onPress={() => void readPath(crumb.path)}
                >
                  {crumb.label}
                </AppButton>
              )}
            </li>
          ))}
          {!crumbs.length && (
            <li>
              <AppButton
                type="button"
                className="border-transparent! bg-transparent! px-1.5!"
                data-directory-picker-edit=""
                aria-label={path ? `${copy.editPath}: ${path}` : copy.enterPath}
                onPress={startEditing}
              >
                {path ? copy.editPath : copy.enterPath}
              </AppButton>
            </li>
          )}
        </ol>
      </nav>
      {editingPath && (
        <div className="grid gap-[5px]">
          <label
            className="text-xs font-semibold text-[var(--ink)]"
            htmlFor={`${titleId}-path`}
          >
            {copy.pathLabel}
          </label>
          <div className="flex flex-wrap items-center gap-2 [&>input]:flex-1">
            <input
              ref={editPathRef}
              id={`${titleId}-path`}
              className={pathInputClass}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={editorInvalid || undefined}
              aria-describedby={editorInvalid ? errorId : undefined}
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
                  setEditingPath(false);
                  const lastPath =
                    listing?.currentPath ?? lastConfirmedPath.current;
                  setPath(lastPath);
                  if (!listing || loading) void readPath(lastPath);
                  requestAnimationFrame(focusEditTrigger);
                }
              }}
            />
            <AppButton type="button" onPress={() => void readPath(path)}>
              {copy.go}
            </AppButton>
          </div>
        </div>
      )}
      <section
        className="min-h-0 overflow-auto overscroll-contain rounded-md border border-[var(--line)] p-0"
        aria-label={copy.folders}
        onScroll={(event) => {
          const element = event.currentTarget;
          if (
            !loading &&
            !error &&
            listing?.nextOffset !== undefined &&
            element.scrollHeight - element.scrollTop - element.clientHeight < 40
          ) {
            void readPath(listing.currentPath, listing.nextOffset, true);
          }
        }}
      >
        {otherRoots.length > 0 && (
          <>
            <h3 className="m-0 px-[10px] pt-2 pb-1 text-sm font-semibold">
              {copy.serverRoots}
            </h3>
            <ul className="m-0 list-none p-0">
              {otherRoots.map((root) => (
                <li key={root}>
                  <AppButton
                    type="button"
                    className={directoryClass}
                    isDisabled={loading}
                    onPress={() => void readPath(root)}
                  >
                    {root}
                  </AppButton>
                </li>
              ))}
            </ul>
          </>
        )}
        {directories.length > 0 ? (
          <ul className="m-0 list-none p-0">
            {directories.map((directory) => (
              <li key={directory}>
                <AppButton
                  type="button"
                  className={directoryClass}
                  isDisabled={loading}
                  onPress={() => void readPath(directory)}
                >
                  {directory.split(pathSeparator).filter(Boolean).at(-1) ||
                    directory}
                </AppButton>
              </li>
            ))}
          </ul>
        ) : listing && !loading && !error ? (
          <p className="m-0 px-[10px] py-2 text-[var(--muted)]">{copy.empty}</p>
        ) : null}
        {loading && (
          <p className="m-0 px-[10px] py-2 text-[var(--muted)]" role="status">
            {copy.loading}
          </p>
        )}
        {error && (
          <div className="flex items-center justify-between gap-2 px-[10px] py-2 text-[var(--error)]">
            <p id={errorId} className="m-0" role="alert">
              {copy[error]}
            </p>
            <AppButton
              type="button"
              onPress={() => {
                const failed = retryRequest.current;
                if (failed)
                  void readPath(failed.path, failed.offset, failed.append);
              }}
            >
              {copy.retry}
            </AppButton>
          </div>
        )}
      </section>
      <footer className="flex justify-end gap-2 border-t border-[var(--line)] pt-[10px] max-[600px]:[&>*]:flex-1">
        <AppButton type="button" onPress={close}>
          {copy.cancel}
        </AppButton>
        <AppButton
          type="button"
          className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-[.92]"
          isDisabled={
            loading ||
            Boolean(error) ||
            !listing ||
            path !== listing.currentPath
          }
          onPress={() => {
            if (!listing || error || loading || path !== listing.currentPath)
              return;
            onSelect(listing.currentPath);
            close();
          }}
        >
          {copy.choose}
        </AppButton>
      </footer>
    </dialog>
  );
}
