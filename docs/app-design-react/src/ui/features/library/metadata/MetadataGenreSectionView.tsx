import type { ReactNode } from "react";
import { AppButton } from "../../../components/AppButton";
import type { Locale } from "../../../i18n/types";
import type { GenreMetadataOwner } from "../GenreMetadataEditorView";
import type { GenreReference } from "../GenrePicker";
import { genreMessages } from "../genreMessages";

export type MetadataGenreState = "absent" | "value" | "explicit_empty";

export type MetadataGenreSectionViewProps = {
  locale: Locale;
  owner: GenreMetadataOwner;
  value: readonly GenreReference[];
  state: MetadataGenreState;
  editing: boolean;
  disabled: boolean;
  /** Prepared picker field; rendered only while editing. */
  genreControl?: ReactNode;
  onClear: () => void;
  onInherit: () => void;
};

/**
 * Genre block embedded in a larger metadata draft. It shares the parent
 * Save/Cancel boundary, so it only mutates the draft state.
 */
export function MetadataGenreSectionView({
  locale,
  owner,
  value,
  state,
  editing,
  disabled,
  genreControl,
  onClear,
  onInherit,
}: MetadataGenreSectionViewProps) {
  const copy = genreMessages[locale];
  const label = copy.label[owner];
  return (
    <section
      aria-label={label}
      className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-2"
    >
      <h4 className="m-0 text-sm font-semibold">{label}</h4>
      {editing
        ? genreControl
        : value.length > 0 && (
            <ul className="m-0 flex list-none flex-wrap gap-2 p-0 text-sm">
              {value.map((genre) => (
                <li
                  className="rounded-full border border-[var(--line)] px-2 py-0.5"
                  key={genre.id}
                >
                  {genre.name}
                </li>
              ))}
            </ul>
          )}
      {!value.length && (
        <p className="m-0 text-sm text-[var(--muted)]">
          {state === "explicit_empty"
            ? copy.explicitlyCleared
            : copy.noOverride}
        </p>
      )}
      {editing && (
        <div className="flex flex-wrap gap-2">
          <AppButton type="button" isDisabled={disabled} onPress={onClear}>
            {copy.clear}
          </AppButton>
          <AppButton type="button" isDisabled={disabled} onPress={onInherit}>
            {copy.inherit}
          </AppButton>
        </div>
      )}
    </section>
  );
}
