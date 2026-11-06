import { useMemo, useState } from "react";
import type { Selection } from "react-aria-components";
import {
  Dialog,
  Heading,
  Input,
  ListBox,
  ListBoxItem,
  Modal,
  ModalOverlay,
  SearchField,
} from "react-aria-components";
import type { GenreCatalogStatus } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import type { GenreReference } from "./GenreMetadataEditor";
import type { GenreMessages } from "./genreMessages";

export type GenreOption = GenreReference;

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
  onOpenCatalog,
  onRetryStatus,
  disabled,
}: {
  value: GenreReference[];
  onChange: (next: GenreReference[]) => void;
  editing: boolean;
  label: string;
  copy: GenreMessages;
  options: readonly GenreOption[];
  query: string;
  onQueryChange: (query: string) => void;
  loading: boolean;
  catalogError: string;
  onClearCatalogError: () => void;
  onRetryCatalog: () => void;
  catalogStatus?: GenreCatalogStatus;
  catalogStatusLoading: boolean;
  catalogStatusError: string;
  onOpenCatalog: () => void;
  onRetryStatus: () => void;
  disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [visibleCount, setVisibleCount] = useState(60);
  const visibleOptions = options.slice(0, visibleCount);
  const visibleOptionIds = useMemo(
    () => new Set(options.map((item) => item.id)),
    [options],
  );
  const selectedKeys = useMemo(
    () =>
      new Set(
        value.map((item) => item.id).filter((id) => visibleOptionIds.has(id)),
      ),
    [value, visibleOptionIds],
  );
  const catalogAvailable = Boolean(
    catalogStatus?.updated_at && catalogStatus.count > 0,
  );

  function close() {
    setOpen(false);
    setVisibleCount(60);
    onQueryChange("");
    onClearCatalogError();
  }

  function changeSelection(selection: Selection) {
    const selected =
      selection === "all"
        ? visibleOptions.map((item) => item.id)
        : Array.from(selection, String);
    const retained = value.filter((item) => !visibleOptionIds.has(item.id));
    const added = options.filter((item) => selected.includes(item.id));
    onChange([...retained, ...added]);
  }

  return (
    <div className="grid min-w-0 gap-1 text-sm">
      {editing ? (
        <>
          <AppButton
            type="button"
            aria-label={label}
            aria-haspopup="dialog"
            aria-expanded={open}
            isDisabled={disabled}
            onPress={() => {
              setOpen(true);
              onOpenCatalog();
            }}
            className="min-h-9 w-full justify-between border-x-0 border-t-0 bg-transparent px-1 text-left shadow-none hover:bg-transparent"
          >
            <span className="truncate text-[var(--ink)]">
              {value.length
                ? value.map((item) => item.name).join(", ")
                : copy.choose}
            </span>
            <svg
              aria-hidden="true"
              viewBox="0 0 16 16"
              className="size-4 shrink-0 text-[var(--muted)]"
              fill="none"
            >
              <path
                d="m4 6 4 4 4-4"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </AppButton>
          <ModalOverlay
            isOpen={open}
            onOpenChange={(nextOpen) => {
              if (nextOpen) setOpen(true);
              else close();
            }}
            isDismissable
            className="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-black/45 p-3 sm:p-6"
          >
            <Modal className="max-h-[min(80vh,560px)] w-full max-w-[560px] overflow-y-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-4 text-[var(--ink)] shadow-[0_16px_48px_#0003] sm:p-5">
              <div
                onKeyDownCapture={(event) => {
                  if (event.key === "Escape") {
                    event.stopPropagation();
                    close();
                  }
                }}
              >
                <Dialog aria-label={`${label} — ${copy.chooseDialog}`}>
                  <Heading slot="title">{label}</Heading>
                  {value.length > 0 ? (
                    <ul
                      aria-label={copy.selected}
                      className="mb-3 flex flex-wrap gap-1.5"
                    >
                      {value.map((genre) => (
                        <li
                          key={genre.id}
                          className="inline-flex items-center gap-1 rounded-full border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm text-[var(--ink)]"
                        >
                          {genre.name}
                          <AppButton
                            type="button"
                            aria-label={`${copy.remove} ${genre.name}`}
                            isDisabled={disabled}
                            onPress={() =>
                              onChange(
                                value.filter((item) => item.id !== genre.id),
                              )
                            }
                            className="min-h-6 min-w-6 px-1 py-0"
                          >
                            ×
                          </AppButton>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                  <SearchField
                    aria-label={copy.search}
                    value={query}
                    isDisabled={
                      disabled || !catalogAvailable || catalogStatusLoading
                    }
                    onChange={(next) => {
                      onQueryChange(next);
                      setVisibleCount(60);
                      onClearCatalogError();
                    }}
                    autoFocus
                    className="grid gap-1"
                  >
                    <Input
                      placeholder={copy.searchPlaceholder}
                      className="min-h-10 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-3 text-[var(--ink)] outline-none placeholder:text-[var(--muted)] focus-visible:ring-2 focus-visible:ring-[var(--focus)]"
                    />
                  </SearchField>
                  {catalogStatusLoading ? (
                    <p role="status" className="mt-2 text-[var(--muted)]">
                      {copy.catalogLoading}
                    </p>
                  ) : null}
                  {catalogStatusError ? (
                    <p role="alert" className="mt-2 text-[var(--error)]">
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
                        href="#/settings"
                      >
                        {copy.openSettings}
                      </a>
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  !catalogStatusError &&
                  !catalogStatus ? (
                    <p role="status" className="mt-2 text-[var(--muted)]">
                      {copy.catalogUnloaded}{" "}
                      <a
                        className="underline underline-offset-2"
                        href="#/settings"
                      >
                        {copy.openSettings}
                      </a>
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  catalogStatus &&
                  !catalogStatus.updated_at ? (
                    <p
                      role={catalogStatus.last_error ? "alert" : "status"}
                      className="mt-2 text-[var(--muted)]"
                    >
                      {catalogStatus.last_error
                        ? `${copy.catalogError} ${catalogStatus.last_error}`
                        : copy.catalogUnloaded}{" "}
                      <a
                        className="underline underline-offset-2"
                        href="#/settings"
                      >
                        {copy.openSettings}
                      </a>
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  catalogStatus?.updated_at &&
                  catalogStatus.count === 0 ? (
                    <p role="status" className="mt-2 text-[var(--muted)]">
                      {copy.catalogEmpty}
                    </p>
                  ) : null}
                  {!catalogStatusLoading &&
                  catalogStatus?.updated_at &&
                  catalogStatus.last_error ? (
                    <p role="status" className="mt-2 text-[var(--muted)]">
                      {copy.catalogLastRefreshError} {catalogStatus.last_error}
                    </p>
                  ) : null}
                  {loading ? (
                    <p role="status" className="mt-2 text-[var(--muted)]">
                      {copy.loading}
                    </p>
                  ) : null}
                  {catalogError ? (
                    <p role="alert" className="mt-2 text-[var(--error)]">
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
                    <p role="status" className="mt-2 text-[var(--muted)]">
                      {copy.searchPrompt}
                    </p>
                  ) : null}
                  {catalogAvailable &&
                  query.trim() &&
                  !loading &&
                  !catalogError &&
                  !options.length ? (
                    <p role="status" className="mt-2 text-[var(--muted)]">
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
                      className="mt-2 max-h-56 overflow-y-auto rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-1"
                    >
                      {visibleOptions.map((item) => (
                        <ListBoxItem
                          key={item.id}
                          id={item.id}
                          textValue={item.name}
                          className="cursor-pointer rounded px-2 py-2 text-[var(--ink)] outline-none focus:bg-[var(--raised)] focus-visible:ring-2 focus-visible:ring-[var(--focus)] data-[selected]:bg-[var(--accent-soft)]"
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
                      className="mt-2 w-full"
                      onPress={() => setVisibleCount((count) => count + 60)}
                    >
                      {copy.more} · {options.length - visibleOptions.length}
                    </AppButton>
                  ) : null}
                  <div className="mt-4 flex justify-end">
                    <AppButton type="button" onPress={close}>
                      {copy.done}
                    </AppButton>
                  </div>
                </Dialog>
              </div>
            </Modal>
          </ModalOverlay>
        </>
      ) : (
        <div
          className="min-h-9 truncate border-b border-transparent px-1 py-2 text-[var(--ink)] max-sm:min-h-10"
          title={value.map((item) => item.name).join(", ")}
        >
          {value.length
            ? value.map((item) => item.name).join(", ")
            : copy.emptyDisplay}
        </div>
      )}
    </div>
  );
}
