import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import {
  GenrePicker,
  type GenrePickerCatalog,
  type GenreReference,
} from "./GenrePicker";
import { genreMessages } from "./genreMessages";
import {
  ProviderGenreBaselineRow,
  type ProviderGenreBaselineValue,
} from "./ProviderGenreBaselineRow";

export type GenreMetadataOwner = "artist" | "release" | "recording";
export type GenreMetadataState = "absent" | "explicit_empty" | "value";

export type GenreMetadataEditorViewProps = {
  owner: GenreMetadataOwner;
  locale: Locale;
  editing: boolean;
  draftDirty: boolean;
  draft: GenreReference[];
  selected: GenreReference[];
  draftState: GenreMetadataState;
  savedState: GenreMetadataState;
  loadingSaved: boolean;
  saving: boolean;
  readError: string;
  saveError: string;
  refreshConflict: boolean;
  providerGenreBaseline?: ProviderGenreBaselineValue | null;
  picker: GenrePickerCatalog;
  onChange: (next: GenreReference[]) => void;
  onEditOrSave: () => void;
  onCancel: () => void;
  onClear: () => void;
  onInherit: () => void;
};

/**
 * Copy-ready genre editor card composition.
 *
 * The provider owns saved metadata, draft state, revision authority, catalog
 * requests and save/cancel. This module owns the complete card markup,
 * including focused Escape handling, actions, provider baseline and notices.
 */
export function GenreMetadataEditorView({
  owner,
  locale,
  editing,
  draftDirty,
  draft,
  selected,
  draftState,
  savedState,
  loadingSaved,
  saving,
  readError,
  saveError,
  refreshConflict,
  providerGenreBaseline,
  picker,
  onChange,
  onEditOrSave,
  onCancel,
  onClear,
  onInherit,
}: GenreMetadataEditorViewProps) {
  const copy = genreMessages[locale];
  const label = copy.label[owner];
  const displayedValue = editing ? draft : selected;
  const emptyState = editing ? draftState : savedState;
  const emptyText =
    emptyState === "explicit_empty"
      ? copy.explicitlyCleared
      : emptyState === "absent"
        ? copy.noOverride
        : copy.empty;

  return (
    <section
      className="grid gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3 text-[var(--ink)]"
      aria-label={label}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing) return;
        event.preventDefault();
        onCancel();
      }}
    >
      <div className="grid min-w-0 gap-1.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
        <div className="min-w-0">
          <h3 className="mb-1 text-sm font-semibold">{label}</h3>
          <GenrePicker
            value={displayedValue}
            onChange={onChange}
            editing={editing}
            label={label}
            copy={copy}
            {...picker}
            disabled={saving}
          />
          <p
            className="m-0 mt-1 min-h-5 text-sm text-[var(--muted)]"
            aria-hidden={Boolean(displayedValue.length)}
          >
            {displayedValue.length ? "\u00a0" : emptyText}
          </p>
        </div>
        <div className="grid grid-cols-[7.5rem_7.5rem] gap-2">
          <AppButton
            type="button"
            isDisabled={
              loadingSaved ||
              !!readError ||
              saving ||
              refreshConflict ||
              (editing &&
                (!draftDirty || (draftState === "value" && !draft.length)))
            }
            onPress={onEditOrSave}
            className="min-w-0 justify-center"
          >
            {editing ? copy.save : copy.edit}
          </AppButton>
          <AppButton
            type="button"
            isDisabled={!editing || saving}
            aria-hidden={!editing}
            excludeFromTabOrder={!editing}
            onPress={onCancel}
            className={`min-w-0 justify-center ${editing ? "" : "invisible"}`}
          >
            {copy.cancel}
          </AppButton>
        </div>
      </div>
      <ProviderGenreBaselineRow
        baseline={providerGenreBaseline}
        locale={locale}
      />
      {loadingSaved ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {copy.loading}
        </p>
      ) : null}
      {readError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {copy.readError} {readError}
        </p>
      ) : null}
      {editing ? (
        <div className="flex flex-wrap gap-2">
          <AppButton type="button" isDisabled={saving} onPress={onClear}>
            {copy.clear}
          </AppButton>
          <AppButton type="button" isDisabled={saving} onPress={onInherit}>
            {copy.inherit}
          </AppButton>
        </div>
      ) : null}
      {saving ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {copy.saving}
        </p>
      ) : null}
      {saveError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {copy.saveError} {saveError} {copy.draftRetained}
        </p>
      ) : null}
      {refreshConflict && draftDirty ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {copy.refreshConflict}
        </p>
      ) : null}
    </section>
  );
}
