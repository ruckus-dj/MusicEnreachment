import {
  Button,
  Cell,
  Checkbox,
  Column,
  Row,
  Table,
  TableBody,
  TableHeader,
} from "react-aria-components";
import {
  LibraryAlbumPositionsView,
  type LibraryAlbumPositionsViewProps,
} from "./LibraryAlbumPositionsView";
import { LibraryEditionArtwork } from "./LibraryEditionArtwork";
import {
  LibraryMissingEditionView,
  type LibraryMissingEditionViewProps,
} from "./LibraryMissingEditionView";

/**
 * Plain selection state supplied by a host. The view owns the checkbox markup
 * and never receives a rendered control, so application and prototype hosts can
 * project their own selection models without sharing JSX.
 */
export interface LibraryAlbumSelectionControl {
  isSelected: boolean;
  isIndeterminate: boolean;
  isDisabled: boolean;
  onChange: (selected: boolean) => void;
}

/**
 * The scalar summary of one album row. Identity, counts and labels are resolved
 * by the host; the view never reads generated API models or invents a value.
 */
export interface LibraryAlbumRow {
  /** Row identity and the value used for controlled expansion. */
  id: string;
  /** Raw title; an empty value is shown as the localized untitled label. */
  title?: string | null;
  /** Local release detail route; missing provider groups omit it. */
  href?: string | null;
  /** True for a provider group that has no local release. */
  missing?: boolean;
  /** Formatted display credits; an empty value is shown as an em dash. */
  artist?: string | null;
  /**
   * Ordered navigable credits. When present, this replaces the scalar
   * `artist` fallback so a host that already has artist routes can preserve
   * links without passing rendered markup.
   */
  artistCredits?: readonly LibraryAlbumCredit[] | null;
  /** Release or original release date, already formatted by the host. */
  date?: string | null;
  /** Secondary edition facts shown below the date. */
  editionNote?: string | null;
  /** `null`/absent is an explicit em dash, not an inferred zero. */
  positionCount?: number | null;
  /** Known source count; `null`/absent means the host has no explicit hint. */
  sourceCount?: number | null;
  /** `null`/absent is an explicit em dash, not an inferred zero. */
  availableCount?: number | null;
  /** `null` means provider completeness is unknown. */
  providerPositionCount?: number | null;
  /** `null`/absent is an explicit em dash, not an inferred zero. */
  publishedCount?: number | null;
  /** Local coverage may have a different denominator than cached audio totals. */
  publicationCoverage?: { total: number; complete: boolean };
  provider: "musicbrainz" | "local";
  /**
   * Cover thumbnail URL supplied by the host. The shared artwork tile
   * shows the title initial when this is absent or the image fails to load.
   */
  coverSrc?: string | null;
  /** Per-row selection; `null`/absent rows have no checkbox. */
  selection?: LibraryAlbumSelectionControl | null;
}

/**
 * Expanded local-release model. `locale` and `layout` are supplied by this
 * view for each responsive surface, so the host keeps only the data and the
 * host-owned selection actions.
 */
export interface LibraryAlbumPositionsDetail
  extends Omit<LibraryAlbumPositionsViewProps, "locale" | "layout"> {
  kind: "positions";
}

/**
 * Ordered display credit for an expanded missing group. Join phrases are kept
 * verbatim; a host that knows an artist route supplies `href`.
 */
export interface LibraryAlbumCredit {
  name: string;
  href?: string | null;
  joinPhrase?: string | null;
}

/**
 * Expanded missing-group model. The date and credits belong to the album row,
 * while the remaining fields are the shared edition chooser contract.
 */
export interface LibraryAlbumMissingEditionDetail
  extends Omit<LibraryMissingEditionViewProps, "locale"> {
  kind: "missing-edition";
  originalReleaseDate?: string | null;
  credits?: readonly LibraryAlbumCredit[] | null;
}

/** Transport state and ready data for the single expanded row. */
export type LibraryAlbumDetail =
  | LibraryAlbumPositionsDetail
  | LibraryAlbumMissingEditionDetail
  | { kind: "loading" }
  | { kind: "error"; message?: string | null; onRetry?: () => void };

export interface LibraryAlbumListViewProps {
  locale: "en" | "ru";
  /** Rows in display order; the host owns filtering, search and pagination. */
  albums: readonly LibraryAlbumRow[];
  /** Identity of the single expanded album, or `null` when all rows close. */
  expandedId: string | null;
  /** Receives a row identity; the host owns the single-expansion state. */
  onToggleExpanded: (id: string) => void;
  /** Optional page-wide selection; absent hosts render rows only. */
  pageSelection?: LibraryAlbumSelectionControl | null;
  /** Detail for `expandedId`; ignored when no row is expanded. */
  detail?: LibraryAlbumDetail | null;
}

const selectColumn =
  "box-border w-12 min-w-12 max-w-12 px-0! text-center! align-middle!";
const widths = [
  selectColumn,
  "",
  "w-[15%]",
  "w-[12%]",
  "w-[8%]",
  "w-[8%]",
  "w-[8%]",
  "w-[11%]",
  "w-[11%]",
];
const tableClass =
  "w-full table-fixed border-separate border-spacing-0 text-sm wrap-anywhere [font-variant-numeric:tabular-nums] [&_.react-aria-Column]:border-b [&_.react-aria-Column]:border-[var(--line)] [&_.react-aria-Column]:bg-[var(--raised)] [&_.react-aria-Column]:px-4 [&_.react-aria-Column]:py-2.5 [&_.react-aria-Column]:text-left [&_.react-aria-Column]:text-xs [&_.react-aria-Column]:font-medium [&_.react-aria-Column]:text-[var(--muted)] [&_.react-aria-Row]:bg-[var(--surface)] [&_.react-aria-Row:hover]:bg-[var(--raised)] [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-t [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-[var(--line)] [&_.react-aria-Cell]:px-4 [&_.react-aria-Cell]:py-3 [&_.react-aria-Cell]:align-middle [&_.react-aria-Cell:first-child]:px-0";
const detailClass =
  "min-w-0 rounded-lg border border-[var(--line)] bg-[var(--raised)]";
const cardClass =
  "grid min-w-0 gap-2.5 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-4 text-sm wrap-anywhere";
const subClass = "block text-xs text-[var(--muted)]";
const linkClass = "font-medium text-[var(--accent-strong)] hover:underline";
const expanderClass =
  "max-w-full text-left font-semibold text-[var(--accent)] wrap-anywhere hover:underline data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]";
const checkboxClass =
  "group inline-flex size-6 shrink-0 cursor-pointer items-center justify-center data-[disabled]:cursor-default data-[disabled]:opacity-45";
const checkboxBoxClass =
  "grid size-4 place-items-center rounded-sm border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-offset-2 group-data-[focus-visible]:outline-[var(--focus)] group-data-[indeterminate]:bg-[var(--button)] group-data-[selected]:bg-[var(--button)]";

const copy = {
  en: {
    table: "Library albums",
    album: "Album",
    artist: "Artist",
    edition: "Year / edition",
    positions: "Positions",
    sources: "Sources",
    audio: "Audio",
    publication: "Publication",
    provider: "Provider",
    page: "Select all positions on this page",
    select: "Select positions",
    open: "Open release",
    artistNotSpecified: "Artist not specified",
    local: "Local collection",
    missing: "Missing from library",
    untitled: "Untitled",
    empty: "No results. Change the query or filter.",
    unknown: "Completeness unknown",
    known: "Completeness known",
    track: "Recording",
    loading: "Loading tracks…",
    error: "Could not load tracks.",
    retry: "Retry",
    notPublished: "Not published",
    dateUnknown: "Date not specified",
    unnamedArtist: "Unnamed artist",
    cacheNotice:
      "Known MusicBrainz cache entries only; this may not be a complete discography.",
  },
  ru: {
    table: "Альбомы медиатеки",
    album: "Альбом",
    artist: "Исполнитель",
    edition: "Год / издание",
    positions: "Позиции",
    sources: "Источники",
    audio: "Аудио",
    publication: "Публикация",
    provider: "Провайдер",
    page: "Выбрать все позиции на этой странице",
    select: "Выбрать позиции",
    open: "Открыть релиз",
    artistNotSpecified: "Артист не указан",
    local: "Локальная коллекция",
    missing: "Нет в медиатеке",
    untitled: "Без названия",
    empty: "Нет результатов. Измените запрос или фильтр.",
    unknown: "Полнота неизвестна",
    known: "Полнота известна",
    track: "Запись",
    loading: "Загрузка треков…",
    error: "Не удалось загрузить треки.",
    retry: "Повторить",
    notPublished: "Не опубликовано",
    dateUnknown: "Дата не указана",
    unnamedArtist: "Без имени",
    cacheNotice:
      "Показаны только известные записи кэша MusicBrainz; это может быть не полная дискография.",
  },
} as const;

type Text = (typeof copy)[keyof typeof copy];

function albumTitle(album: LibraryAlbumRow, text: Text) {
  return album.title || text.untitled;
}

function AlbumArtwork({ album, text }: { album: LibraryAlbumRow; text: Text }) {
  return (
    <LibraryEditionArtwork
      title={albumTitle(album, text)}
      imageSrc={album.coverSrc ?? undefined}
    />
  );
}

function CreditNames({
  credits,
  text,
}: {
  credits: readonly LibraryAlbumCredit[];
  text: Text;
}) {
  const occurrences = new Map<string, number>();
  return (
    <>
      {credits.map((credit, index) => {
        const identity = `${credit.name}\u0000${credit.href ?? ""}\u0000${credit.joinPhrase ?? ""}`;
        const occurrence = occurrences.get(identity) ?? 0;
        occurrences.set(identity, occurrence + 1);
        return (
          <span key={`${identity}-${occurrence}`}>
            {credit.href ? (
              <a className={linkClass} href={credit.href}>
                {credit.name || text.unnamedArtist}
              </a>
            ) : (
              credit.name || text.unnamedArtist
            )}
            {credit.joinPhrase ?? (index < credits.length - 1 ? " " : "")}
          </span>
        );
      })}
    </>
  );
}

function albumArtist(album: LibraryAlbumRow, text: Text) {
  if (album.artistCredits?.length) {
    return <CreditNames credits={album.artistCredits} text={text} />;
  }
  return album.artist || "—";
}

function editionField(album: LibraryAlbumRow) {
  return (
    <>
      {album.date || "—"}
      {album.editionNote && (
        <span className={subClass}>{album.editionNote}</span>
      )}
    </>
  );
}

function audioField(album: LibraryAlbumRow, text: Text) {
  if (album.missing) return text.missing;
  if (album.availableCount == null) return "—";
  if (album.providerPositionCount == null) {
    return (
      <>
        {album.availableCount}
        <span className={subClass}>{text.unknown}</span>
      </>
    );
  }
  return `${album.availableCount} / ${album.providerPositionCount}`;
}

function publicationField(album: LibraryAlbumRow, text: Text) {
  if (album.missing || album.publishedCount == null) return "—";
  if (album.publicationCoverage) {
    return (
      <>
        {album.publishedCount} / {album.publicationCoverage.total}
        <span className={subClass}>
          {album.publicationCoverage.complete ? text.known : text.unknown}
        </span>
      </>
    );
  }
  if (!album.publishedCount) return text.notPublished;
  return (
    <>
      {album.publishedCount} /{" "}
      {album.providerPositionCount ?? album.positionCount ?? "—"}
      {album.providerPositionCount == null && (
        <span className={subClass}>{text.unknown}</span>
      )}
    </>
  );
}

function fields(album: LibraryAlbumRow, text: Text) {
  return [
    albumArtist(album, text),
    editionField(album),
    album.positionCount ?? "—",
    album.sourceCount ?? "—",
    audioField(album, text),
    publicationField(album, text),
    album.provider === "musicbrainz" ? (
      <span key="provider" className="whitespace-nowrap">
        MusicBrainz
      </span>
    ) : (
      <span key="provider" className="whitespace-nowrap">
        {text.local}
      </span>
    ),
  ];
}

function SelectionCheckbox({
  control,
  label,
}: {
  control: LibraryAlbumSelectionControl;
  label: string;
}) {
  return (
    <Checkbox
      slot={null}
      aria-label={label}
      isSelected={control.isSelected}
      isIndeterminate={control.isIndeterminate}
      isDisabled={control.isDisabled}
      onChange={control.onChange}
      className={checkboxClass}
    >
      <span aria-hidden="true" className={checkboxBoxClass}>
        <svg
          aria-hidden="true"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className="size-3.5"
        >
          <path
            d="M3 8l3 3 7-7"
            className="hidden group-data-[selected]:block"
          />
          <path
            d="M3 8h10"
            className="hidden group-data-[indeterminate]:block"
          />
        </svg>
      </span>
    </Checkbox>
  );
}

function AlbumDetail({
  detail,
  text,
  locale,
  mobile,
}: {
  detail: LibraryAlbumDetail;
  text: Text;
  locale: LibraryAlbumListViewProps["locale"];
  mobile: boolean;
}) {
  if (detail.kind === "loading") {
    return (
      <p role="status" className="p-2">
        {text.loading}
      </p>
    );
  }
  if (detail.kind === "error") {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-2 p-2">
        {detail.message || text.error}
        {detail.onRetry && (
          <Button className={linkClass} onPress={detail.onRetry}>
            {text.retry}
          </Button>
        )}
      </div>
    );
  }
  if (detail.kind === "positions") {
    return (
      <LibraryAlbumPositionsView
        {...detail}
        locale={locale}
        layout={mobile ? "mobile" : "desktop"}
      />
    );
  }
  return (
    <div className={`${detailClass} p-2`}>
      <p>
        {detail.originalReleaseDate || text.dateUnknown}
        {detail.credits?.length ? (
          <>
            {" · "}
            <CreditNames credits={detail.credits} text={text} />
          </>
        ) : null}
      </p>
      <p className={subClass}>{text.cacheNotice}</p>
      <LibraryMissingEditionView locale={locale} {...detail} />
    </div>
  );
}

/**
 * Shared album surface with the exact desktop table widths, the mobile
 * breakpoint and the full-width inline detail row used by every host.
 */
export function LibraryAlbumListView({
  locale,
  albums,
  expandedId,
  onToggleExpanded,
  pageSelection,
  detail,
}: LibraryAlbumListViewProps) {
  const text = copy[locale];
  const headings = [
    "",
    text.album,
    text.artist,
    text.edition,
    text.positions,
    text.sources,
    text.audio,
    text.publication,
    text.provider,
  ];
  const expanded = albums.find((album) => album.id === expandedId) ?? null;
  const expandedDetail = expanded ? detail : null;
  const rows = albums.flatMap((album) =>
    expandedId === album.id
      ? [
          { id: album.id, album, detail: false },
          { id: `${album.id}:details`, album, detail: true },
        ]
      : [{ id: album.id, album, detail: false }],
  );
  function checkbox(album: LibraryAlbumRow) {
    if (!album.selection) return null;
    return (
      <SelectionCheckbox
        control={album.selection}
        label={`${text.select}: ${albumTitle(album, text)}`}
      />
    );
  }
  function heading(album: LibraryAlbumRow) {
    const title = albumTitle(album, text);
    return (
      <>
        <Button
          className={expanderClass}
          aria-expanded={expandedId === album.id}
          onPress={() => onToggleExpanded(album.id)}
        >
          {title}
        </Button>
        {album.href ? (
          <a
            aria-label={`${text.open}: ${title}`}
            href={album.href}
            className={`${subClass} ${linkClass}`}
          >
            {text.open}
          </a>
        ) : (
          <span className={subClass}>
            {album.missing ? text.missing : text.artistNotSpecified}
          </span>
        )}
      </>
    );
  }
  if (!albums.length) return <p className={cardClass}>{text.empty}</p>;
  return (
    <div className="w-full min-w-0">
      <div className="hidden gap-2 max-[900px]:grid">
        {pageSelection && (
          <div className={cardClass}>
            <div className="flex items-center gap-2">
              <SelectionCheckbox control={pageSelection} label={text.page} />
              {text.page}
            </div>
          </div>
        )}
        {albums.map((album) => (
          <article className={cardClass} key={album.id}>
            <div className="flex min-w-0 items-start gap-2">
              {checkbox(album)}
              <AlbumArtwork album={album} text={text} />
              <div className="min-w-0">{heading(album)}</div>
            </div>
            <dl className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-x-3 gap-y-1">
              {fields(album, text).map((field, index) => (
                <div className="contents" key={headings[index + 2]}>
                  <dt className="text-[var(--muted)]">{headings[index + 2]}</dt>
                  <dd>{field}</dd>
                </div>
              ))}
            </dl>
            {expandedId === album.id && expandedDetail && (
              <AlbumDetail
                detail={expandedDetail}
                text={text}
                locale={locale}
                mobile
              />
            )}
          </article>
        ))}
      </div>
      <div className="min-w-0 max-[900px]:hidden">
        <Table aria-label={text.table} className={tableClass}>
          <TableHeader>
            {headings.map((label, index) => (
              <Column
                key={label}
                className={`react-aria-Column ${widths[index]} whitespace-normal! wrap-anywhere`}
                isRowHeader={index === 1}
              >
                {index === 0
                  ? pageSelection && (
                      <SelectionCheckbox
                        control={pageSelection}
                        label={text.page}
                      />
                    )
                  : label}
              </Column>
            ))}
          </TableHeader>
          <TableBody
            items={rows}
            dependencies={[
              albums,
              pageSelection,
              expandedId,
              expandedDetail,
              locale,
            ]}
          >
            {({ id, album, detail: isDetail }) =>
              isDetail ? (
                <Row
                  id={id}
                  aria-label={`${text.track} · ${albumTitle(album, text)}`}
                >
                  <Cell
                    colSpan={headings.length}
                    className="w-auto! min-w-0! max-w-none! px-2! text-left!"
                  >
                    {expandedDetail && (
                      <AlbumDetail
                        detail={expandedDetail}
                        text={text}
                        locale={locale}
                        mobile={false}
                      />
                    )}
                  </Cell>
                </Row>
              ) : (
                <Row
                  id={id}
                  className={album.missing ? "bg-[var(--raised)]" : undefined}
                >
                  <Cell className={selectColumn}>{checkbox(album)}</Cell>
                  <Cell>
                    <div className="flex min-w-0 items-center gap-3">
                      <AlbumArtwork album={album} text={text} />
                      <div className="grid min-w-0 gap-0.5">
                        {heading(album)}
                      </div>
                    </div>
                  </Cell>
                  {fields(album, text).map((field, index) => (
                    <Cell key={headings[index + 2]}>{field}</Cell>
                  ))}
                </Row>
              )
            }
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
