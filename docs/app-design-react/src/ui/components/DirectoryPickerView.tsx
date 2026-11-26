import { useCallback, useEffect, useId, useRef, useState } from "react";
import {
  Breadcrumb,
  Breadcrumbs,
  Dialog,
  Heading,
  Input,
  Label,
  Modal,
  ModalOverlay,
  TextField,
} from "react-aria-components";
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

const breadcrumbButtonClass =
  "min-h-9 shrink-0 justify-start border-transparent! bg-transparent! px-1.5! hover:border-[var(--line)]! hover:bg-[var(--surface)]!";

const directoryClass =
  "flex! min-h-9 w-full justify-start! gap-2 rounded-none! border-0! border-b! border-[var(--line)]! bg-transparent! px-2.5! py-1.5! text-left text-sm [overflow-wrap:anywhere] hover:bg-[var(--raised)]!";

const pathInputClass =
  "min-h-9 w-full min-w-0 flex-1 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2.5 py-1.5 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 aria-[invalid=true]:border-[var(--error)]";

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
  const controller = useRef<AbortController | undefined>(undefined);
  const dialog = useRef<HTMLElement>(null);
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

  const cancelEditing = useCallback(() => {
    setEditingPath(false);
    const lastPath = listing?.currentPath ?? lastConfirmedPath.current;
    setPath(lastPath);
    if (!listing || loading) void readPath(lastPath);
    requestAnimationFrame(() => {
      dialog.current
        ?.querySelector<HTMLElement>("[data-directory-picker-edit]")
        ?.focus();
    });
  }, [listing, loading, readPath]);

  useEffect(() => {
    if (!open) return;
    lastConfirmedPath.current = initialPath;
    setPath(initialPath);
    setEditingPath(false);
    setListing(undefined);
    setDirectories([]);
    void readPath(initialPath);
    return () => {
      abortRequest();
    };
  }, [open, initialPath, abortRequest, readPath]);

  useEffect(() => {
    if (editingPath) editPathRef.current?.focus();
  }, [editingPath]);

  function close() {
    abortRequest();
    onClose();
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
  const crumbItems = crumbs.length
    ? crumbs
    : [
        {
          label: path ? copy.editPath : copy.enterPath,
          path: "",
        },
      ];
  const currentCrumbLabel = currentPath
    ? `${copy.editPath}: ${currentPath}`
    : copy.enterPath;

  function startEditing() {
    setPath(currentPath);
    setEditingPath(true);
  }

  useEffect(() => {
    if (!open || !editingPath) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      if (!dialog.current?.contains(event.target as Node)) return;
      event.preventDefault();
      event.stopPropagation();
      cancelEditing();
    };
    document.addEventListener("keydown", handleKeyDown, true);
    return () => document.removeEventListener("keydown", handleKeyDown, true);
  }, [open, editingPath, cancelEditing]);

  return (
    <ModalOverlay
      isOpen={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) close();
      }}
      isDismissable
      isKeyboardDismissDisabled={editingPath}
      className="fixed inset-0 z-[100] grid place-items-center overflow-y-auto overscroll-contain bg-black/50 p-3 max-[600px]:p-1.5"
    >
      <Modal className="max-h-[min(86dvh,720px)] w-[min(94vw,640px)] overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] text-sm text-[var(--ink)] shadow-[0_16px_48px_#0003] max-[600px]:max-h-[calc(100dvh-12px)] max-[600px]:w-[calc(100vw-12px)]">
        <Dialog
          ref={dialog}
          aria-labelledby={titleId}
          aria-describedby={`${titleId}-help`}
          className={`grid max-h-[inherit] min-h-0 gap-[14px] overflow-y-auto overscroll-contain p-[18px] outline-none max-[600px]:p-3.5 ${
            editingPath
              ? "grid-rows-[auto_auto_auto_minmax(0,1fr)_auto]"
              : "grid-rows-[auto_auto_minmax(0,1fr)_auto]"
          }`}
        >
          <div className="relative grid gap-[3px] border-b border-[var(--line)] pr-12 pb-3">
            <Heading
              slot="title"
              id={titleId}
              className="m-0 text-base font-semibold"
            >
              {copy.title}
            </Heading>
            <p id={`${titleId}-help`} className="m-0 text-[var(--muted)]">
              {copy.help}
            </p>
            <AppButton
              className="absolute top-0 right-0 h-9 w-9 min-h-9 px-0!"
              type="button"
              aria-label={copy.close}
              onPress={close}
            >
              <svg
                aria-hidden="true"
                viewBox="0 0 16 16"
                width="16"
                height="16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              >
                <path d="M4 4l8 8M12 4l-8 8" />
              </svg>
            </AppButton>
          </div>
          <nav
            className="flex min-h-12 min-w-0 items-center gap-1.5 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--raised)] p-1.5"
            aria-label={copy.navigation}
          >
            <AppButton
              className="h-9 w-9 min-h-9 flex-none px-0!"
              type="button"
              aria-label={copy.up}
              isDisabled={
                loading ||
                !listing ||
                listing.currentPath === listing.parentPath
              }
              onPress={() => void readPath(listing?.parentPath ?? "")}
            >
              <svg
                aria-hidden="true"
                viewBox="0 0 16 16"
                width="16"
                height="16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M8 12V4M4.5 7.5L8 4l3.5 3.5" />
              </svg>
            </AppButton>
            <Breadcrumbs className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto whitespace-nowrap [scrollbar-width:thin]">
              {crumbItems.map((crumb, index) => {
                const isCurrent = index === crumbItems.length - 1;
                return (
                  <Breadcrumb
                    key={crumb.path || "__current"}
                    className="flex shrink-0 items-center gap-0.5"
                  >
                    {index > 0 && (
                      <span aria-hidden="true" className="text-[var(--muted)]">
                        /
                      </span>
                    )}
                    <AppButton
                      type="button"
                      className={`${breadcrumbButtonClass}${
                        isCurrent ? " font-semibold" : ""
                      }`}
                      isDisabled={loading && !isCurrent}
                      aria-current={isCurrent ? "location" : undefined}
                      aria-label={isCurrent ? currentCrumbLabel : undefined}
                      data-directory-picker-edit={isCurrent ? "" : undefined}
                      onPress={
                        isCurrent
                          ? startEditing
                          : () => void readPath(crumb.path)
                      }
                    >
                      {crumb.label}
                    </AppButton>
                  </Breadcrumb>
                );
              })}
            </Breadcrumbs>
          </nav>
          {editingPath && (
            <TextField
              className="grid gap-1.5"
              value={path}
              onChange={setPath}
              isInvalid={editorInvalid}
            >
              <Label className="text-xs font-semibold text-[var(--ink)]">
                {copy.pathLabel}
              </Label>
              <div className="flex min-w-0 items-center gap-2">
                <Input
                  ref={editPathRef}
                  className={pathInputClass}
                  autoComplete="off"
                  spellCheck={false}
                  aria-describedby={editorInvalid ? errorId : undefined}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      event.stopPropagation();
                      void readPath(path);
                    } else if (event.key === "Escape") {
                      event.preventDefault();
                      event.stopPropagation();
                      cancelEditing();
                    }
                  }}
                />
                <AppButton
                  className="min-h-9 shrink-0"
                  type="button"
                  onPress={() => void readPath(path)}
                >
                  {copy.go}
                </AppButton>
              </div>
            </TextField>
          )}
          <section
            className="min-h-0 overflow-auto overscroll-contain rounded-[var(--control-radius)] border border-[var(--line)] p-0"
            aria-label={copy.folders}
            onScroll={(event) => {
              const element = event.currentTarget;
              if (
                !loading &&
                !error &&
                listing?.nextOffset !== undefined &&
                element.scrollHeight -
                  element.scrollTop -
                  element.clientHeight <
                  40
              ) {
                void readPath(listing.currentPath, listing.nextOffset, true);
              }
            }}
          >
            {otherRoots.length > 0 && (
              <>
                <h3 className="m-0 px-2.5 pt-2 pb-1 text-sm font-semibold">
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
              <p className="m-0 px-2.5 py-2 text-[var(--muted)]">
                {copy.empty}
              </p>
            ) : null}
            {loading && (
              <p className="m-0 px-2.5 py-2 text-[var(--muted)]" role="status">
                {copy.loading}
              </p>
            )}
            {error && (
              <div className="flex items-center justify-between gap-2 px-2.5 py-2 text-[var(--error)]">
                <p id={errorId} className="m-0" role="alert">
                  {copy[error]}
                </p>
                <AppButton
                  className="min-h-9 shrink-0"
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
          <footer className="flex justify-end gap-2 border-t border-[var(--line)] pt-2.5 max-[600px]:[&>*]:flex-1">
            <AppButton className="min-h-9" type="button" onPress={close}>
              {copy.cancel}
            </AppButton>
            <AppButton
              type="button"
              className="min-h-9 border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:border-[var(--button-hover)]! hover:bg-[var(--button-hover)]! active:bg-[var(--button-hover)]! disabled:border-[var(--line)]! disabled:bg-[var(--raised)]! disabled:text-[var(--muted)]! aria-disabled:border-[var(--line)]! aria-disabled:bg-[var(--raised)]! aria-disabled:text-[var(--muted)]!"
              isDisabled={
                loading ||
                Boolean(error) ||
                !listing ||
                path !== listing.currentPath
              }
              onPress={() => {
                if (
                  !listing ||
                  error ||
                  loading ||
                  path !== listing.currentPath
                )
                  return;
                onSelect(listing.currentPath);
                close();
              }}
            >
              {copy.choose}
            </AppButton>
          </footer>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
