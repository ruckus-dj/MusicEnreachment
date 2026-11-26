import {
  Checkbox,
  GridList,
  GridListItem,
  type Key,
  Link,
  type Selection,
  Text,
} from "react-aria-components";
import { libraryArtistCardStyles as catalogueStyles } from "./libraryArtistCardStyles";

const copyByLocale = {
  en: {
    artistsLibrary: "Library artists",
    performer: "Performer",
    audio: "Audio",
    published: "Published",
    selectArtist: "Select positions by",
    selectArtistHint:
      "Selects every position by this artist, not the artist record.",
    selectPage: "Select on this page",
    positionsOnlyHelp: "Selection applies to positions, not artist records.",
    noArtists: "No artists found.",
    partialProviderScope: "Provider counts cover the known cache only.",
  },
  ru: {
    artistsLibrary: "Артисты медиатеки",
    performer: "Исполнитель",
    audio: "Аудио",
    published: "Опубликовано",
    selectArtist: "Выбрать позиции артиста",
    selectArtistHint:
      "Выбирает все позиции этого артиста, а не запись артиста.",
    selectPage: "Выбрать на этой странице",
    positionsOnlyHelp: "Выбор относится к позициям, а не к записям артистов.",
    noArtists: "Артисты не найдены.",
    partialProviderScope: "Данные провайдера ограничены известным кэшем.",
  },
} as const;

const selectionGlyph = (
  <svg
    aria-hidden="true"
    viewBox="0 0 16 16"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.8"
    strokeLinecap="round"
    strokeLinejoin="round"
    className="size-3"
  >
    <path d="m3 8 3 3 7-7" />
  </svg>
);

/** Display data for one artist card; counts are resolved by the host. */
export interface LibraryArtistCard {
  id: string;
  name: string;
  href: string;
  monogram: string;
  palette: string;
  genres: string[];
  releaseCount: number;
  /** Undefined when the provider does not expose missing-edition data; never inferred as 0. */
  missingEditionCount?: number;
  positionCount: number;
  availableCount: number;
  publishedCount: number;
  partialProviderScope?: boolean;
  /** Plain per-card selection state and action, derived by the host. */
  selection: ArtistCardSelectionControl;
}

/** Raw page-scoped selection supplied by hosts that allow selecting card positions. */
export interface ArtistSelectionState {
  selectedIds: ReadonlySet<string>;
  onChange: (ids: string[], selected: boolean) => void;
}

/** Per-card or page-wide selection state and action; the card view owns the markup. */
export interface ArtistCardSelectionControl {
  isSelected: boolean;
  isIndeterminate: boolean;
  isDisabled: boolean;
  onChange: (selected: boolean) => void;
}

interface Props {
  locale: "en" | "ru";
  artists: LibraryArtistCard[];
  /** Optional page-wide select; hosts without one render cards only. */
  pageSelectionControl?: ArtistCardSelectionControl;
}

const pageSelectionRow = "flex items-center gap-2 text-sm text-[var(--muted)]";
const pageSelectionHelpId = "library-artist-page-selection-help";

export function LibraryArtistCards({
  locale,
  artists,
  pageSelectionControl,
}: Props) {
  const copy = copyByLocale[locale];
  const selectedKeys = new Set<Key>(
    artists
      .filter((artist) => artist.selection.isSelected)
      .map((artist) => artist.id),
  );
  const disabledKeys = artists
    .filter((artist) => artist.selection.isDisabled)
    .map((artist) => artist.id);

  function changeSelection(keys: Selection) {
    for (const artist of artists) {
      if (artist.selection.isDisabled) continue;
      const next =
        keys === "all" ? true : keys instanceof Set && keys.has(artist.id);
      if (next !== artist.selection.isSelected) artist.selection.onChange(next);
    }
  }

  return (
    <>
      {pageSelectionControl ? (
        <div className="mb-3 grid gap-1">
          <div className={pageSelectionRow}>
            <Checkbox
              slot={null}
              aria-label={copy.selectPage}
              aria-describedby={pageSelectionHelpId}
              isSelected={pageSelectionControl.isSelected}
              isIndeterminate={pageSelectionControl.isIndeterminate}
              isDisabled={pageSelectionControl.isDisabled}
              onChange={pageSelectionControl.onChange}
              className="group grid size-8 cursor-pointer place-items-center data-[disabled]:cursor-default data-[disabled]:opacity-45 data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2"
            >
              <span
                aria-hidden="true"
                className="grid size-4 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--surface)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)]"
              >
                {pageSelectionControl.isIndeterminate
                  ? "−"
                  : pageSelectionControl.isSelected
                    ? selectionGlyph
                    : null}
              </span>
            </Checkbox>
            <span>{copy.selectPage}</span>
          </div>
          <p
            id={pageSelectionHelpId}
            className="m-0 text-xs text-[var(--muted)]"
          >
            {copy.positionsOnlyHelp}
          </p>
        </div>
      ) : null}
      <div
        className="contents"
        onKeyDownCapture={(event) => {
          if (
            (event.ctrlKey || event.metaKey) &&
            event.key.toLowerCase() === "a"
          ) {
            event.preventDefault();
            changeSelection("all");
          }
        }}
      >
        <GridList
          aria-label={copy.artistsLibrary}
          layout="grid"
          selectionMode="multiple"
          selectionBehavior="toggle"
          disabledBehavior="selection"
          selectedKeys={selectedKeys}
          disabledKeys={disabledKeys}
          onSelectionChange={changeSelection}
          items={artists}
          className={catalogueStyles.artistGrid}
          renderEmptyState={() => (
            <div className={catalogueStyles.empty}>{copy.noArtists}</div>
          )}
        >
          {(artist) => {
            const selectionHelpId = `library-artist-position-help-${artist.id}`;
            return (
              <GridListItem
                id={artist.id}
                textValue={artist.name}
                className={catalogueStyles.artistCard}
              >
                <span
                  title={copy.selectArtistHint}
                  className="absolute right-3 top-3 z-[2]"
                >
                  <Checkbox
                    slot="selection"
                    aria-label={copy.selectArtist}
                    aria-describedby={selectionHelpId}
                    isIndeterminate={artist.selection.isIndeterminate}
                    className="grid size-5 cursor-pointer place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--surface)] data-[indeterminate]:border-[var(--accent)] data-[indeterminate]:bg-[var(--accent)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-2"
                  >
                    <span
                      aria-hidden="true"
                      className={
                        artist.selection.isIndeterminate
                          ? "h-0.5 w-2 rounded bg-current"
                          : artist.selection.isSelected
                            ? "block"
                            : "hidden"
                      }
                    >
                      {artist.selection.isIndeterminate ? null : selectionGlyph}
                    </span>
                  </Checkbox>
                </span>
                <span id={selectionHelpId} className="sr-only">
                  {copy.selectArtistHint}
                </span>
                <div className="flex min-w-0 items-center gap-3 pr-7">
                  <span
                    className={catalogueStyles.artistArt}
                    data-palette={artist.palette}
                    aria-hidden="true"
                  >
                    {artist.monogram}
                  </span>
                  <span className={catalogueStyles.artistCopy}>
                    <Link
                      href={artist.href}
                      className="block rounded-sm text-sm font-semibold text-[var(--ink)] underline-offset-2 wrap-anywhere hover:underline focus-visible:outline-2 focus-visible:outline-[var(--accent)]"
                    >
                      {artist.name}
                    </Link>
                    <Text
                      slot="description"
                      className="block text-xs text-[var(--muted)] wrap-anywhere"
                    >
                      {artist.genres.join(" · ") || copy.performer}
                    </Text>
                  </span>
                </div>
                <dl className={catalogueStyles.artistStats}>
                  <div>
                    <dt>{copy.audio}</dt>
                    <dd>
                      <strong>
                        {artist.availableCount} / {artist.positionCount}
                      </strong>
                    </dd>
                  </div>
                  <div>
                    <dt>{copy.published}</dt>
                    <dd>
                      <strong>
                        {artist.publishedCount} / {artist.positionCount}
                      </strong>
                    </dd>
                  </div>
                </dl>
                {artist.partialProviderScope ? (
                  <p className="m-0 text-xs text-[var(--warning)]">
                    {copy.partialProviderScope}
                  </p>
                ) : null}
              </GridListItem>
            );
          }}
        </GridList>
      </div>
    </>
  );
}
