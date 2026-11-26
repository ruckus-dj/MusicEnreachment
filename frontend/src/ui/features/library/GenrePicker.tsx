import {
  type KeyboardEvent as ReactKeyboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { Key, Selection } from "react-aria-components";
import {
  Autocomplete,
  Button,
  DialogTrigger,
  Input,
  ListBox,
  ListBoxItem,
  Popover,
  SearchField,
  Tag,
  TagGroup,
  TagList,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import type { GenreMessages } from "./genreMessages";

export type GenreReference = {
  id: string;
  source_name: string;
  name: string;
};

export type GenrePickerCatalog = {
  options: readonly GenreReference[];
  query: string;
  onQueryChange: (query: string) => void;
  loading: boolean;
  catalogError: string;
  onClearCatalogError: () => void;
  onRetryCatalog: () => void;
  catalogStatus?: {
    updated_at?: string | null;
    count: number;
    last_error?: string | null;
  };
  catalogStatusLoading: boolean;
  catalogStatusError: string;
  bundledCatalogReady?: boolean;
  onLoadBundledCatalog?: () => void;
  settingsHref?: string;
  onOpenCatalog: () => void;
  onRetryStatus: () => void;
};

const triggerClass =
  "size-8 min-h-8 min-w-8 shrink-0 rounded-md px-0 text-[var(--muted)] hover:text-[var(--ink)]";

const tagClass =
  "inline-flex h-7 max-w-full min-w-0 items-center gap-1 rounded-full border border-[var(--line)] bg-[var(--raised)] py-0 pl-2.5 pr-1 text-xs text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

const readOnlyTagClass =
  "inline-flex h-7 max-w-full min-w-0 items-center rounded-full border border-[var(--line)] bg-[var(--raised)] px-2.5 text-xs text-[var(--ink)]";

const removeButtonClass =
  "grid size-5 shrink-0 cursor-pointer place-items-center rounded-full text-[var(--muted)] hover:bg-[var(--surface)] hover:text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2 disabled:cursor-not-allowed disabled:opacity-50";

const popoverClass =
  "z-50 max-h-[min(24rem,calc(100dvh-1rem))] w-[min(22rem,calc(100vw-1rem))] overflow-y-auto rounded-xl border border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--ink)] shadow-[0_12px_32px_rgb(0_0_0/0.14)]";

const searchClass =
  "h-9 w-full min-w-0 rounded-lg border border-[var(--control)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] placeholder:text-[var(--muted)] hover:border-[var(--ink)] focus-visible:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0";

const listBoxClass =
  "max-h-56 overflow-y-auto overscroll-contain rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-1 outline-none";

const optionClass =
  "cursor-pointer rounded-[var(--control-radius)] px-2.5 py-2 text-sm text-[var(--ink)] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent-strong)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:-outline-offset-2";

const noticeClass = "m-0 text-xs text-[var(--muted)]";
const errorNoticeClass = "m-0 text-xs text-[var(--error)]";

type PendingFocus =
  | { target: "trigger"; removed?: string[] }
  | { target: "tag"; id: string; removed: string[] };

/**
 * Multi-select genre picker. The selected value is always visible as removable
 * tags; a compact, anchored popover holds search, catalog status and the
 * provider-supplied result page. Search state and results belong to the host,
 * so the popover never filters or paginates them a second time.
 */
export function GenrePicker({
  value,
  onChange,
  editing,
  label,
  copy,
  options,
  query,
  onQueryChange,
  loading,
  catalogError,
  onClearCatalogError,
  onRetryCatalog,
  catalogStatus,
  catalogStatusLoading,
  catalogStatusError,
  bundledCatalogReady,
  onLoadBundledCatalog,
  settingsHref = "#/settings",
  onOpenCatalog,
  onRetryStatus,
  disabled,
  showLabel,
}: {
  value: GenreReference[];
  onChange: (next: GenreReference[]) => void;
  editing: boolean;
  label: string;
  copy: GenreMessages;
  disabled: boolean;
  showLabel?: boolean;
} & GenrePickerCatalog) {
  const [open, setOpen] = useState(false);
  const [visibleCount, setVisibleCount] = useState(60);
  const fieldRef = useRef<HTMLDivElement>(null);
  const tagGroupRef = useRef<HTMLDivElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const pendingFocus = useRef<PendingFocus | null>(null);
  const pendingExpandFocus = useRef(false);
  const focusFrame = useRef<number | null>(null);
  const [, setFocusRequest] = useState(0);
  const visibleOptions = useMemo(
    () => options.slice(0, visibleCount),
    [options, visibleCount],
  );
  const visibleOptionIds = useMemo(
    () => new Set(visibleOptions.map((item) => item.id)),
    [visibleOptions],
  );
  const selectedKeys = useMemo(
    () =>
      new Set(
        value.map((item) => item.id).filter((id) => visibleOptionIds.has(id)),
      ),
    [value, visibleOptionIds],
  );
  const catalogAvailable = Boolean(
    bundledCatalogReady ||
      (catalogStatus?.updated_at && catalogStatus.count > 0),
  );
  const searchDisabled = disabled || !catalogAvailable || catalogStatusLoading;
  const loadAvailable = Boolean(
    onLoadBundledCatalog &&
      !catalogStatusLoading &&
      !catalogStatusError &&
      !catalogStatus &&
      !bundledCatalogReady,
  );

  function requestFocus(next: PendingFocus) {
    pendingFocus.current = next;
    setFocusRequest((request) => request + 1);
  }

  useEffect(() => {
    if (!open) return;
    if (!searchDisabled) {
      searchInputRef.current?.focus();
      return;
    }
    const action = loadAvailable ? "load" : "done";
    popoverRef.current
      ?.querySelector<HTMLButtonElement>(
        `[data-genre-popover-action="${action}"]`,
      )
      ?.focus();
  }, [loadAvailable, open, searchDisabled]);

  useEffect(() => {
    if (!pendingExpandFocus.current || visibleOptions.length < options.length)
      return;
    pendingExpandFocus.current = false;
    if (!searchDisabled) {
      searchInputRef.current?.focus();
      return;
    }
    popoverRef.current
      ?.querySelector<HTMLButtonElement>('[data-genre-popover-action="done"]')
      ?.focus();
  }, [searchDisabled, options.length, visibleOptions.length]);

  useEffect(() => {
    const pending = pendingFocus.current;
    if (!pending) return;
    if (pending.removed?.some((id) => value.some((item) => item.id === id)))
      return;
    pendingFocus.current = null;
    const trigger =
      fieldRef.current?.querySelector<HTMLButtonElement>("[data-genre-add]");
    let target = trigger;
    if (pending.target === "tag") {
      const buttons = tagGroupRef.current?.querySelectorAll<HTMLButtonElement>(
        "[data-genre-remove-id]",
      );
      target = buttons
        ? (Array.from(buttons).find(
            (button) => button.dataset.genreRemoveId === pending.id,
          ) ?? trigger)
        : trigger;
    }
    if (!target) return;
    if (focusFrame.current != null) cancelAnimationFrame(focusFrame.current);
    focusFrame.current = requestAnimationFrame(() => {
      focusFrame.current = null;
      target?.focus();
    });
  });

  useEffect(
    () => () => {
      if (focusFrame.current != null) cancelAnimationFrame(focusFrame.current);
    },
    [],
  );

  function close() {
    setOpen(false);
    setVisibleCount(60);
    pendingExpandFocus.current = false;
    onQueryChange("");
    onClearCatalogError();
  }

  function changeOpen(nextOpen: boolean) {
    if (nextOpen) {
      setOpen(true);
      onOpenCatalog();
      return;
    }
    close();
  }

  function changeQuery(next: string) {
    onQueryChange(next);
    setVisibleCount(60);
    onClearCatalogError();
  }

  function changeSelection(selection: Selection) {
    const selected =
      selection === "all"
        ? visibleOptions.map((item) => item.id)
        : Array.from(selection, String);
    const retained = value.filter((item) => !visibleOptionIds.has(item.id));
    const added = visibleOptions.filter((item) => selected.includes(item.id));
    onChange([...retained, ...added]);
  }

  function removeSelection(keys: Iterable<Key>) {
    const removed = new Set(Array.from(keys, String));
    if (!removed.size) return;
    const firstRemoved = value.findIndex((item) => removed.has(item.id));
    const remaining = value.filter((item) => !removed.has(item.id));
    const next =
      remaining[Math.min(Math.max(firstRemoved, 0), remaining.length - 1)];
    requestFocus(
      next
        ? { target: "tag", id: next.id, removed: [...removed] }
        : { target: "trigger", removed: [...removed] },
    );
    onChange(remaining);
  }

  function showMore() {
    const next = visibleCount + 60;
    if (next >= options.length) pendingExpandFocus.current = true;
    setVisibleCount(next);
  }

  function handlePickerKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (!open || event.key !== "Escape") return;
    event.preventDefault();
    event.stopPropagation();
    requestFocus({ target: "trigger" });
    close();
  }

  return (
    <div className="grid min-w-0 gap-1 text-sm">
      {showLabel && (
        <span className="text-xs text-[var(--muted)]">{label}</span>
      )}
      {editing ? (
        <div
          ref={fieldRef}
          onKeyDownCapture={handlePickerKeyDown}
          className="flex min-h-9 min-w-0 flex-wrap items-center gap-1.5 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-1 py-1 focus-within:border-[var(--accent)] focus-within:outline-2 focus-within:outline-[var(--accent)] focus-within:outline-offset-0 pointer-coarse:min-h-10"
        >
          {value.length ? (
            <TagGroup
              ref={tagGroupRef}
              aria-label={copy.selected}
              onRemove={removeSelection}
              disabledKeys={disabled ? value.map((item) => item.id) : undefined}
              className="min-w-0 flex-1"
            >
              <TagList
                items={value}
                className="flex min-w-0 flex-wrap items-center gap-1.5"
              >
                {(genre) => (
                  <Tag
                    id={genre.id}
                    textValue={genre.name}
                    className={tagClass}
                  >
                    <span
                      className="min-w-0 max-w-[16rem] truncate"
                      title={
                        genre.source_name !== genre.name
                          ? genre.source_name
                          : undefined
                      }
                    >
                      {genre.name}
                    </span>
                    <Button
                      slot="remove"
                      aria-label={copy.remove}
                      data-genre-remove-id={genre.id}
                      className={removeButtonClass}
                    >
                      <AppIcon name="close" className="size-3" />
                    </Button>
                  </Tag>
                )}
              </TagList>
            </TagGroup>
          ) : (
            <span className="min-w-0 flex-1 px-1 text-[var(--muted)]">
              {copy.choose}
            </span>
          )}
          <DialogTrigger isOpen={open} onOpenChange={changeOpen}>
            <AppButton
              type="button"
              aria-label={label}
              data-genre-add
              variant="ghost"
              isDisabled={disabled}
              className={triggerClass}
            >
              <AppIcon name="plus" />
            </AppButton>
            <Popover
              ref={popoverRef}
              isNonModal
              placement="bottom start"
              offset={4}
              className={popoverClass}
            >
              <Autocomplete
                inputValue={query}
                onInputChange={changeQuery}
                /*
                 * The host already searched the catalog. Keeping every supplied
                 * row prevents a second, client-side filter of server results.
                 */
                filter={() => true}
              >
                <div className="grid gap-2 p-2">
                  <SearchField
                    aria-label={copy.search}
                    isDisabled={
                      disabled || !catalogAvailable || catalogStatusLoading
                    }
                    className="grid gap-1"
                  >
                    <Input
                      ref={searchInputRef}
                      placeholder={copy.searchPlaceholder}
                      className={searchClass}
                    />
                  </SearchField>
                  {catalogStatusLoading ? (
                    <p role="status" className={noticeClass}>
                      {copy.catalogLoading}
                    </p>
                  ) : null}
                  {catalogStatusError ? (
                    <p role="alert" className={errorNoticeClass}>
                      {copy.catalogError} {catalogStatusError}{" "}
                      <button
                        type="button"
                        className="underline underline-offset-2"
                        onClick={onRetryStatus}
                      >
                        {copy.retry}
                      </button>
                      {" · "}
                      <a
                        className="underline underline-offset-2"
                        href={settingsHref}
                      >
                        {copy.openSettings}
                      </a>
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  !catalogStatusError &&
                  !catalogStatus &&
                  !bundledCatalogReady ? (
                    <p role="status" className={noticeClass}>
                      {copy.catalogUnloaded}{" "}
                      {onLoadBundledCatalog ? (
                        <AppButton
                          type="button"
                          size="sm"
                          data-genre-popover-action="load"
                          onPress={onLoadBundledCatalog}
                        >
                          {copy.loadBundledCatalog}
                        </AppButton>
                      ) : (
                        <a
                          className="underline underline-offset-2"
                          href={settingsHref}
                        >
                          {copy.openSettings}
                        </a>
                      )}
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  catalogStatus &&
                  !catalogStatus.updated_at ? (
                    <p
                      role={catalogStatus.last_error ? "alert" : "status"}
                      className={noticeClass}
                    >
                      {catalogStatus.last_error
                        ? `${copy.catalogError} ${catalogStatus.last_error}`
                        : copy.catalogUnloaded}{" "}
                      <a
                        className="underline underline-offset-2"
                        href={settingsHref}
                      >
                        {copy.openSettings}
                      </a>
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  catalogStatus?.updated_at &&
                  catalogStatus.count === 0 ? (
                    <p role="status" className={noticeClass}>
                      {copy.catalogEmpty}
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  catalogStatus?.updated_at &&
                  catalogStatus.last_error ? (
                    <p role="status" className={noticeClass}>
                      {copy.catalogLastRefreshError} {catalogStatus.last_error}
                    </p>
                  ) : null}
                  {loading ? (
                    <p role="status" className={noticeClass}>
                      {copy.loading}
                    </p>
                  ) : null}
                  {catalogError ? (
                    <p role="alert" className={errorNoticeClass}>
                      {copy.catalogError} {catalogError}
                      <button
                        type="button"
                        className="ml-2 underline underline-offset-2"
                        onClick={onRetryCatalog}
                      >
                        {copy.retry}
                      </button>
                    </p>
                  ) : null}
                  {catalogAvailable &&
                  !query.trim() &&
                  !options.length &&
                  !loading ? (
                    <p role="status" className={noticeClass}>
                      {copy.searchPrompt}
                    </p>
                  ) : null}
                  {catalogAvailable &&
                  query.trim() &&
                  !loading &&
                  !catalogError &&
                  !options.length ? (
                    <p role="status" className={noticeClass}>
                      {copy.noMatches}
                    </p>
                  ) : null}
                  {catalogAvailable && options.length > 0 && !catalogError ? (
                    <ListBox
                      aria-label={copy.available}
                      selectionMode="multiple"
                      disabledKeys={
                        disabled ? options.map((item) => item.id) : []
                      }
                      escapeKeyBehavior="none"
                      selectedKeys={selectedKeys}
                      onSelectionChange={changeSelection}
                      className={listBoxClass}
                    >
                      {visibleOptions.map((item) => (
                        <ListBoxItem
                          key={item.id}
                          id={item.id}
                          aria-label={item.name}
                          textValue={item.name}
                          className={optionClass}
                        >
                          <span>{item.name}</span>
                          {item.source_name !== item.name ? (
                            <span className="ml-2 text-xs text-[var(--muted)]">
                              {item.source_name}
                            </span>
                          ) : null}
                        </ListBoxItem>
                      ))}
                    </ListBox>
                  ) : null}
                  {catalogAvailable &&
                  visibleOptions.length < options.length ? (
                    <AppButton
                      type="button"
                      size="sm"
                      className="w-full"
                      onPress={showMore}
                    >
                      {copy.more} · {options.length - visibleOptions.length}
                    </AppButton>
                  ) : null}
                  <div className="flex justify-end">
                    <AppButton
                      type="button"
                      size="sm"
                      data-genre-popover-action="done"
                      onPress={close}
                    >
                      {copy.done}
                    </AppButton>
                  </div>
                </div>
              </Autocomplete>
            </Popover>
          </DialogTrigger>
        </div>
      ) : value.length ? (
        <ul
          aria-label={copy.selected}
          className="m-0 flex min-w-0 list-none flex-wrap items-center gap-1.5 p-0"
        >
          {value.map((genre) => (
            <li key={genre.id} className={readOnlyTagClass}>
              <span
                className="min-w-0 truncate"
                title={
                  genre.source_name !== genre.name
                    ? genre.source_name
                    : undefined
                }
              >
                {genre.name}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <div
          className="min-h-9 truncate border-b border-transparent px-1 py-2 text-[var(--ink)] max-sm:min-h-10"
          title={copy.emptyDisplay}
        >
          {copy.emptyDisplay}
        </div>
      )}
    </div>
  );
}
