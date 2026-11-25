import { useRef, useState } from "react";
import { AppButton } from "../../components/AppButton";
import {
  GenrePicker,
  type GenrePickerCatalog,
  type GenreReference,
} from "./GenrePicker";
import { genreMessages } from "./genreMessages";
import { MetadataCardActions } from "./MetadataCardActions";

const copy = {
  en: {
    title: "Artist metadata",
    name: "Name",
    sortName: "Sort name",
    clear: "Clear",
    clearSortName: "Clear sort name",
    disambiguation: "Disambiguation",
    clearDisambiguation: "Clear disambiguation",
    tags: "Tags",
    nameRequired: "Enter the artist’s name.",
    saveError: "Could not save artist metadata.",
    loading: "Loading artist metadata…",
    readError: "Could not load artist metadata.",
    retry: "Retry",
    conflict:
      "Artist metadata changed. Your draft is preserved. Cancel and reopen the editor to use the current revision.",
  },
  ru: {
    title: "Метаданные артиста",
    name: "Имя",
    sortName: "Имя для сортировки",
    clear: "Очистить",
    clearSortName: "Очистить имя для сортировки",
    disambiguation: "Уточнение",
    clearDisambiguation: "Очистить уточнение",
    tags: "Теги",
    nameRequired: "Укажите имя артиста.",
    saveError: "Не удалось сохранить метаданные артиста.",
    loading: "Загрузка метаданных артиста…",
    readError: "Не удалось загрузить метаданные артиста.",
    retry: "Повторить",
    conflict:
      "Метаданные артиста изменились. Черновик сохранён. Отмените правки и откройте редактор заново, чтобы использовать актуальную версию.",
  },
} as const;

export const artistMetadataCardMessages = copy;

export type ArtistMetadataValues = {
  name: string;
  disambiguation: string | null;
  sortName: string | null;
  genre: {
    state: "absent" | "explicit_empty" | "value";
    value: GenreReference[];
  };
  tags?: string;
};

type ArtistMetadataCardProps = {
  locale: "en" | "ru";
  values: ArtistMetadataValues;
  revision: string | number;
  readOnly?: readonly { id: string; label: string; value: string }[];
  genreCaption?: string;
  genreCatalog: GenrePickerCatalog;
  providerGenres?: GenreReference[] | null;
  onSave: (
    changes: Partial<ArtistMetadataValues>,
    expectedRevision: string | number,
  ) => Promise<void>;
  disabled?: boolean;
};

type DraftValues = {
  name: string;
  disambiguation: string | null;
  sortName: string | null;
  genre: ArtistMetadataValues["genre"];
  tags: string;
};

function toDraft(values: ArtistMetadataValues): DraftValues {
  return {
    name: values.name,
    disambiguation: values.disambiguation,
    sortName: values.sortName,
    genre: {
      state: values.genre.state,
      value: values.genre.value.map((genre) => ({ ...genre })),
    },
    tags: values.tags ?? "",
  };
}

function sameGenre(
  left: ArtistMetadataValues["genre"],
  right: ArtistMetadataValues["genre"],
) {
  return (
    left.state === right.state &&
    left.value.length === right.value.length &&
    left.value.every((genre, index) => {
      const other = right.value[index];
      return (
        genre.id === other?.id &&
        genre.source_name === other.source_name &&
        genre.name === other.name
      );
    })
  );
}

function sameDraft(left: DraftValues, right: DraftValues) {
  return (
    left.name === right.name &&
    left.disambiguation === right.disambiguation &&
    left.sortName === right.sortName &&
    left.tags === right.tags &&
    sameGenre(left.genre, right.genre)
  );
}

export function ArtistMetadataCard({
  locale,
  values,
  revision,
  readOnly,
  genreCaption,
  genreCatalog,
  providerGenres,
  onSave,
  disabled,
}: ArtistMetadataCardProps) {
  const text = copy[locale];
  const genreCopy = genreMessages[locale];
  const [draft, setDraft] = useState<DraftValues>(() => toDraft(values));
  const [baseline, setBaseline] = useState<{
    values: DraftValues;
    revision: string | number;
  }>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const pending = useRef(false);
  const actions = useRef<HTMLDivElement>(null);
  const editing = baseline !== undefined;
  const displayed = editing ? draft : values;
  const busy = Boolean(disabled) || saving;
  const hasChanges = baseline && !sameDraft(draft, baseline.values);

  function beginEdit() {
    if (busy) return;
    const next = toDraft(values);
    setBaseline({ values: next, revision });
    setDraft(next);
    setSaveError("");
  }

  function cancelEdit() {
    if (pending.current) return;
    setBaseline(undefined);
    setSaveError("");
    requestAnimationFrame(() =>
      actions.current?.querySelector("button")?.focus(),
    );
  }

  async function saveDraft() {
    if (!baseline || busy || pending.current) return;
    if (!draft.name.trim()) {
      setSaveError(text.nameRequired);
      return;
    }
    const before = baseline.values;
    const changes: Partial<ArtistMetadataValues> = {};
    if (draft.name !== before.name) changes.name = draft.name;
    if (draft.disambiguation !== before.disambiguation)
      changes.disambiguation = draft.disambiguation;
    if (draft.sortName !== before.sortName) changes.sortName = draft.sortName;
    if (!sameGenre(draft.genre, before.genre)) changes.genre = draft.genre;
    if (draft.tags !== before.tags) changes.tags = draft.tags;
    if (!Object.keys(changes).length) return;
    pending.current = true;
    setSaving(true);
    setSaveError("");
    try {
      await onSave(changes, baseline.revision);
      setBaseline(undefined);
    } catch (reason) {
      // A failed write keeps the shared draft open for correction and retry.
      setSaveError(
        reason instanceof Error && reason.message
          ? reason.message
          : text.saveError,
      );
    } finally {
      pending.current = false;
      setSaving(false);
    }
  }

  function updateText(
    key: "name" | "disambiguation" | "sortName" | "tags",
    value: string,
  ) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  function renderTextField(
    key: "name" | "disambiguation" | "sortName" | "tags",
    label: string,
  ) {
    return (
      <div
        key={key}
        className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2"
      >
        <dt className="text-xs text-[var(--muted)]">{label}</dt>
        <dd className="m-0 flex min-h-9 min-w-0 items-center gap-1.5 text-sm">
          {editing ? (
            <input
              aria-label={label}
              className="min-h-9 min-w-0 flex-1 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
              value={draft[key] ?? ""}
              disabled={busy}
              onChange={(event) => updateText(key, event.currentTarget.value)}
            />
          ) : (
            <span className="block whitespace-pre-wrap break-words">
              {displayed[key] ??
                (key === "sortName" ? genreCopy.noOverride : "")}
            </span>
          )}
          {editing && (key === "sortName" || key === "disambiguation") && (
            <AppButton
              type="button"
              aria-label={
                key === "sortName"
                  ? text.clearSortName
                  : text.clearDisambiguation
              }
              isDisabled={
                busy ||
                (key === "sortName"
                  ? draft.sortName === null
                  : draft.disambiguation === "")
              }
              onPress={(event) => {
                event.target.parentElement?.querySelector("input")?.focus();
                setDraft((current) => ({
                  ...current,
                  [key]: key === "sortName" ? null : "",
                }));
              }}
              className="shrink-0"
            >
              {text.clear}
            </AppButton>
          )}
        </dd>
      </div>
    );
  }

  return (
    <section
      aria-label={text.title}
      className="grid min-w-0 gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3 text-[var(--ink)]"
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing || saving) return;
        event.preventDefault();
        event.stopPropagation();
        cancelEdit();
      }}
    >
      <MetadataCardActions
        locale={locale}
        title={text.title}
        editing={editing}
        saving={saving}
        editOrSaveDisabled={busy || (editing && !hasChanges)}
        actionsRef={actions}
        onEditOrSave={() => (editing ? void saveDraft() : beginEdit())}
        onCancel={cancelEdit}
      />
      <div className="grid min-w-0 gap-2">
        <dl className="m-0 grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {(
            [
              ["name", text.name],
              ["sortName", text.sortName],
              ["disambiguation", text.disambiguation],
              ["tags", text.tags],
            ] as const
          )
            .filter(([key]) => key !== "tags" || values.tags !== undefined)
            .map(([key, label]) => renderTextField(key, label))}
        </dl>
        <section
          aria-label={genreCopy.label.artist}
          className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2"
        >
          <h3 className="text-sm font-semibold">{genreCopy.label.artist}</h3>
          <GenrePicker
            {...genreCatalog}
            value={displayed.genre.value}
            onChange={(next) =>
              setDraft((current) => ({
                ...current,
                genre: {
                  state: next.length ? "value" : "explicit_empty",
                  value: next,
                },
              }))
            }
            editing={editing}
            label={genreCopy.label.artist}
            copy={genreCopy}
            disabled={busy}
          />
          {!displayed.genre.value.length && (
            <p className="m-0 text-sm text-[var(--muted)]">
              {displayed.genre.state === "explicit_empty"
                ? genreCopy.explicitlyCleared
                : genreCaption || genreCopy.noOverride}
            </p>
          )}
          {editing && (
            <div className="flex flex-wrap gap-2">
              <AppButton
                type="button"
                isDisabled={busy}
                onPress={() =>
                  setDraft((current) => ({
                    ...current,
                    genre: { state: "explicit_empty", value: [] },
                  }))
                }
              >
                {genreCopy.clear}
              </AppButton>
              <AppButton
                type="button"
                isDisabled={busy}
                onPress={() =>
                  setDraft((current) => ({
                    ...current,
                    genre: { state: "absent", value: [] },
                  }))
                }
              >
                {genreCopy.inherit}
              </AppButton>
            </div>
          )}
        </section>
        {providerGenres !== undefined ? (
          <section
            className="grid gap-1 border-t border-[var(--line)] pt-2 text-sm"
            aria-label={genreCopy.providerGenres}
          >
            <h3 className="m-0 font-medium">{genreCopy.providerGenres}</h3>
            {providerGenres ? (
              providerGenres.length ? (
                <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
                  {providerGenres.map((genre) => (
                    <li
                      className="rounded-full border border-[var(--line)] px-2 py-0.5"
                      key={genre.id}
                    >
                      {genre.name}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="m-0 text-[var(--muted)]">
                  {genreCopy.providerGenresEmpty}
                </p>
              )
            ) : (
              <p className="m-0 text-[var(--muted)]">
                {genreCopy.providerGenresUnavailable}
              </p>
            )}
          </section>
        ) : null}
        {readOnly?.length ? (
          <dl className="m-0 grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {readOnly.map((item) => (
              <div
                key={item.id}
                className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2"
              >
                <dt className="text-xs text-[var(--muted)]">{item.label}</dt>
                <dd className="m-0 min-w-0 text-sm">
                  <span className="block whitespace-pre-wrap break-words">
                    {item.value}
                  </span>
                </dd>
              </div>
            ))}
          </dl>
        ) : null}
      </div>
      {saveError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {saveError}
        </p>
      ) : null}
    </section>
  );
}
