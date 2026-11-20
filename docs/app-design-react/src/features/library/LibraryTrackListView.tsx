import type { ReactNode } from "react";
import {
  Cell,
  Checkbox,
  Column,
  Row,
  Table,
  TableBody,
  TableHeader,
} from "react-aria-components";

/**
 * Boolean selection state and action supplied by the host. The view owns the
 * checkbox markup and never receives a rendered control, so the application
 * and the design prototype can project their own selection models.
 */
export interface LibraryTrackSelectionControl {
  isSelected: boolean;
  isIndeterminate: boolean;
  isDisabled: boolean;
  onChange: (selected: boolean) => void;
}

/** One ordered artist credit; join phrases are kept verbatim. */
export interface LibraryTrackCredit {
  name: string;
  href?: string | null;
  joinPhrase?: string | null;
}

/**
 * One ordered track-position row. There is no grouping and no provider-only
 * record here: the host passes positions in display order.
 *
 * Technical details are optional. Omit a field when the provider has no
 * corresponding fact instead of inventing a value.
 */
export interface LibraryTrackRow {
  /** Track-position identity, also the React key. */
  id: string;
  title: string;
  durationMs?: number | null;
  /** Track-position detail route. */
  href: string;
  /** Release context; href is optional because a host may not have a route. */
  releaseTitle?: string | null;
  releaseHref?: string | null;
  releaseSubtitle?: string;
  recordingTitle?: string;
  /** Credits in display order. */
  credits?: readonly LibraryTrackCredit[] | null;
  /** Medium and number already formatted by the host, for example "1 · 03". */
  positionLabel: string;
  /** Independent audio hint; never derived from sources or publication. */
  hasAudio: boolean;
  /** Source variant count, independent of `hasAudio`. */
  variantCount: number;
  /** Independent publication state; details below are only used when true. */
  published: boolean;
  /** Actual selected-source codec, when the host knows it. */
  sourceCodec?: string | null;
  /** Host-known source state used only when no codec is available. */
  sourceSelection?: "unselected" | "missing" | null;
  /** Recorded audio properties, already formatted by the host. */
  properties?: string | null;
  /** Known source paths; an empty list is displayed as an em dash. */
  paths?: readonly string[] | null;
  /** Published container and codec, displayed only when `published` is true. */
  publicationContainer?: string | null;
  publicationCodec?: string | null;
  /** Track tags route and optional published title. */
  tagsHref?: string | null;
  tagsTitle?: string | null;
  filterState?: "match" | "muted";
  /** Per-row controlled selection state and action. */
  selection: LibraryTrackSelectionControl;
}

export interface LibraryTrackListViewProps {
  locale: "en" | "ru";
  /** Positions in display order. */
  rows: readonly LibraryTrackRow[];
  /** Page-wide select; omit when the host does not offer bulk selection. */
  pageSelection?: LibraryTrackSelectionControl | null;
  /** Accessible name for both layouts; defaults to the localized "Tracks". */
  label?: string | null;
}

type Locale = LibraryTrackListViewProps["locale"];

const copy = {
  en: {
    selection: "Selection",
    track: "Track",
    release: "Release",
    artist: "Artist",
    position: "Position",
    audio: "Audio",
    sources: "Sources",
    publication: "Publication",
    properties: "Properties",
    filePath: "File path",
    tags: "Tags",
    tracksTable: "Library tracks",
    available: "Available",
    missing: "Missing",
    published: "Published",
    notPublished: "Unpublished",
    unnamed: "Untitled",
    durationUnknown: "Duration not specified",
    unknownArtist: "Unnamed artist",
    sourceCount: "Sources: {count}",
    noSourceSelected: "No source selected",
    noFile: "No file",
    empty: "No tracks.",
    selectPage: "Select all tracks on this page",
    selectTrack: "Select {track}",
  },
  ru: {
    selection: "Выбор",
    track: "Трек",
    release: "Релиз",
    artist: "Артист",
    position: "Позиция",
    audio: "Аудио",
    sources: "Исходники",
    publication: "Публикация",
    properties: "Параметры",
    filePath: "Путь к файлу",
    tags: "Теги",
    tracksTable: "Треки медиатеки",
    available: "В наличии",
    missing: "Отсутствует",
    published: "Опубликовано",
    notPublished: "Не опубликовано",
    unnamed: "Без названия",
    durationUnknown: "Длительность не указана",
    unknownArtist: "Артист не указан",
    sourceCount: "Исходники: {count}",
    noSourceSelected: "Источник не выбран",
    noFile: "Нет файла",
    empty: "Нет треков.",
    selectPage: "Выбрать все треки на этой странице",
    selectTrack: "Выбрать трек {track}",
  },
} as const;

type Text = (typeof copy)[Locale];

const styles = {
  root: "w-full min-w-0 text-[var(--ink)]",
  mobileList: "m-0 hidden grid-cols-1 gap-2 p-0 max-[599px]:grid",
  mobileCard:
    "grid min-w-0 gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2 text-xs [font-variant-numeric:tabular-nums]",
  mobileField:
    "m-0 grid min-w-0 grid-cols-[minmax(76px,.7fr)_minmax(0,1.5fr)] gap-2 border-b border-[var(--line)] pb-1.5 last:border-b-0 last:pb-0 [&>dt]:text-[11px] [&>dt]:text-[var(--muted)] [&>dd]:m-0 [&>dd]:[overflow-wrap:anywhere] [&_a]:font-semibold [&_a]:text-[var(--accent)] [&_a]:underline-offset-2 [&_a:hover]:underline",
  desktop: "w-full overflow-auto max-[599px]:hidden",
  table:
    "w-full table-fixed border-separate border-spacing-0 text-xs wrap-anywhere [font-variant-numeric:tabular-nums] [&_.react-aria-Column]:border-b [&_.react-aria-Column]:border-[var(--line)] [&_.react-aria-Column]:bg-[var(--raised)] [&_.react-aria-Column]:px-[9px] [&_.react-aria-Column]:py-2 [&_.react-aria-Column]:text-left [&_.react-aria-Column]:text-[12px] [&_.react-aria-Column]:font-bold [&_.react-aria-Column]:text-[var(--muted)] [&_.react-aria-Column]:align-middle [&_.react-aria-Row]:bg-[var(--surface)] [&_.react-aria-Row:hover]:bg-[var(--raised)] [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-t [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-[var(--line)] [&_.react-aria-Cell]:min-w-0 [&_.react-aria-Cell]:px-2 [&_.react-aria-Cell]:py-[6px] [&_.react-aria-Cell]:align-top [&_.react-aria-Cell:nth-child(2)]:align-middle",
  selectionCell:
    "box-border w-[34px] min-w-[34px] max-w-[34px] px-0! text-center! align-middle!",
  sub: "mt-[3px] block text-xs font-normal text-[var(--muted)] [overflow-wrap:anywhere]",
  link: "font-semibold text-[var(--accent)] underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]",
  path: "block font-mono text-[11px] [overflow-wrap:anywhere]",
  empty: "m-0 p-3 text-center text-[var(--muted)]",
  checkbox:
    "group grid size-8 shrink-0 cursor-pointer place-items-center data-[disabled]:cursor-default data-[disabled]:opacity-45 data-[focus-visible]:outline-2 data-[focus-visible]:outline-offset-2 data-[focus-visible]:outline-[var(--focus)]",
  checkboxBox:
    "grid size-[18px] place-items-center rounded-[3px] border border-[var(--control)] bg-[var(--surface)] text-sm leading-none text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)]",
  filterMatch:
    "bg-[linear-gradient(to_right,color-mix(in_srgb,var(--accent)_4%,transparent),transparent)] bg-left-top bg-no-repeat [background-size:9%_100%]",
  filterMutedRow: "[&>td]:opacity-[.55] [&:focus-within>td]:opacity-100",
  filterMutedCard: "[&_dl]:opacity-[.55] [&:focus-within_dl]:opacity-100",
} as const;

function filterClass(row: LibraryTrackRow, muted: string) {
  if (row.filterState === "match") return styles.filterMatch;
  return row.filterState === "muted" ? muted : undefined;
}

function fill(message: string, values: Record<string, string | number>) {
  return message.replace(/\{([^}]+)\}/g, (_, name: string) =>
    String(values[name] ?? `{${name}}`),
  );
}

function formatDuration(
  durationMs: number | null | undefined,
  unknown: string,
) {
  if (durationMs == null || !Number.isFinite(durationMs) || durationMs < 0) {
    return unknown;
  }
  const totalSeconds = Math.floor(durationMs / 1000);
  return `${Math.floor(totalSeconds / 60)}:${String(totalSeconds % 60).padStart(2, "0")}`;
}

function rowLabel(row: LibraryTrackRow, text: Text) {
  return fill(text.selectTrack, {
    track: row.title || text.unnamed,
  });
}

function publicationLabel(row: LibraryTrackRow, text: Text) {
  if (!row.published) return text.notPublished;
  const detail = [row.publicationContainer, row.publicationCodec]
    .map((value) => value?.trim())
    .filter((value): value is string => Boolean(value))
    .join(" · ");
  return detail || text.published;
}

function SelectionCheckbox({
  control,
  label,
}: {
  control: LibraryTrackSelectionControl;
  label: string;
}) {
  return (
    <Checkbox
      slot={null}
      className={styles.checkbox}
      aria-label={label}
      isSelected={control.isSelected}
      isIndeterminate={control.isIndeterminate}
      isDisabled={control.isDisabled}
      onChange={control.onChange}
    >
      <span className={styles.checkboxBox} aria-hidden="true">
        {(control.isIndeterminate || control.isSelected) && (
          <svg
            aria-hidden="true"
            width="12"
            height="12"
            viewBox="0 0 12 12"
            fill="none"
          >
            <path
              d={control.isIndeterminate ? "M2 6h8" : "m2 6 2.5 2.5L10 3"}
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        )}
      </span>
    </Checkbox>
  );
}

function MobileField({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <dl className={styles.mobileField}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </dl>
  );
}

function TrackTitle({ row, text }: { row: LibraryTrackRow; text: Text }) {
  return (
    <>
      <a className={styles.link} href={row.href}>
        {row.title || text.unnamed}
      </a>
      <span className={styles.sub}>
        {formatDuration(row.durationMs, text.durationUnknown)}
      </span>
      {row.recordingTitle && (
        <span className={styles.sub}>{row.recordingTitle}</span>
      )}
    </>
  );
}

function ReleaseSummary({ row, text }: { row: LibraryTrackRow; text: Text }) {
  if (row.releaseTitle == null) return <>—</>;
  const title = row.releaseTitle || text.unnamed;
  return (
    <>
      {row.releaseHref ? (
        <a className={styles.link} href={row.releaseHref}>
          {title}
        </a>
      ) : (
        title
      )}
      {row.releaseSubtitle && (
        <span className={styles.sub}>{row.releaseSubtitle}</span>
      )}
    </>
  );
}

function Credits({ row, text }: { row: LibraryTrackRow; text: Text }) {
  const credits = row.credits ?? [];
  if (!credits.length) return <>—</>;
  const occurrences = new Map<string, number>();
  const entries = credits.map((credit) => {
    const identity = JSON.stringify([
      credit.href,
      credit.name,
      credit.joinPhrase,
    ]);
    const occurrence = occurrences.get(identity) ?? 0;
    occurrences.set(identity, occurrence + 1);
    return { credit, key: `${identity}:${occurrence}` };
  });
  return (
    <span>
      {entries.map(({ credit, key }, index) => (
        <span key={key}>
          {credit.href ? (
            <a className={styles.link} href={credit.href}>
              {credit.name || text.unknownArtist}
            </a>
          ) : (
            credit.name || text.unknownArtist
          )}
          {credit.joinPhrase ?? (index < credits.length - 1 ? " " : "")}
        </span>
      ))}
    </span>
  );
}

function SourceSummary({ row, text }: { row: LibraryTrackRow; text: Text }) {
  const count = fill(text.sourceCount, { count: row.variantCount });
  const codec = row.sourceCodec?.trim();
  const primary = codec
    ? codec
    : row.sourceSelection === "unselected"
      ? text.noSourceSelected
      : row.sourceSelection === "missing"
        ? text.noFile
        : null;
  if (!primary) return <>{count}</>;
  return (
    <>
      {primary}
      <span className={styles.sub}>{count}</span>
    </>
  );
}

function PositionPaths({
  paths,
}: {
  paths: readonly string[] | null | undefined;
}) {
  const unique = [...new Set(paths ?? [])].filter(Boolean);
  return unique.length ? (
    unique.map((path) => (
      <span className={styles.path} key={path}>
        {path}
      </span>
    ))
  ) : (
    <>—</>
  );
}

function TagsSummary({ row, text }: { row: LibraryTrackRow; text: Text }) {
  const title = row.tagsTitle?.trim() ? row.tagsTitle : null;
  if (row.tagsHref) {
    return (
      <>
        <a className={styles.link} href={row.tagsHref}>
          {text.tags}
        </a>
        {title && <span className={styles.sub}>{title}</span>}
      </>
    );
  }
  if (title) return <>{title}</>;
  return <>—</>;
}

export function LibraryTrackListView({
  locale,
  rows,
  pageSelection,
  label,
}: LibraryTrackListViewProps) {
  const text = copy[locale];
  const tableLabel = label?.trim() || text.tracksTable;
  const showRelease = rows.some((row) => row.releaseTitle != null);
  const showProperties = rows.some((row) => row.properties !== undefined);
  const showPaths = rows.some((row) => row.paths !== undefined);
  const showTags = rows.some(
    (row) => row.tagsHref != null || row.tagsTitle != null,
  );
  const showDetails = showProperties || showPaths || showTags;
  const tableClass = `${styles.table} ${showDetails ? "min-w-[960px]" : "min-w-[620px]"}`;

  return (
    <div className={styles.root}>
      <ul className={styles.mobileList} aria-label={tableLabel}>
        {pageSelection ? (
          <li className={styles.mobileCard}>
            <MobileField label={text.selectPage}>
              <SelectionCheckbox
                control={pageSelection}
                label={text.selectPage}
              />
            </MobileField>
          </li>
        ) : null}
        {rows.length ? (
          rows.map((row) => (
            <li
              className={`${styles.mobileCard} ${filterClass(row, styles.filterMutedCard) ?? ""}`}
              key={row.id}
            >
              <MobileField label={text.selection}>
                <SelectionCheckbox
                  control={row.selection}
                  label={rowLabel(row, text)}
                />
              </MobileField>
              <MobileField label={text.track}>
                <TrackTitle row={row} text={text} />
              </MobileField>
              {showRelease && (
                <MobileField label={text.release}>
                  <ReleaseSummary row={row} text={text} />
                </MobileField>
              )}
              <MobileField label={text.artist}>
                <Credits row={row} text={text} />
              </MobileField>
              <MobileField label={text.position}>
                {row.positionLabel.trim() || "—"}
              </MobileField>
              <MobileField label={text.audio}>
                {row.hasAudio ? text.available : text.missing}
              </MobileField>
              <MobileField label={text.sources}>
                <SourceSummary row={row} text={text} />
              </MobileField>
              {showProperties && (
                <MobileField label={text.properties}>
                  {row.properties || "—"}
                </MobileField>
              )}
              {showPaths && (
                <MobileField label={text.filePath}>
                  <PositionPaths paths={row.paths} />
                </MobileField>
              )}
              <MobileField label={text.publication}>
                {publicationLabel(row, text)}
              </MobileField>
              {showTags && (
                <MobileField label={text.tags}>
                  <TagsSummary row={row} text={text} />
                </MobileField>
              )}
            </li>
          ))
        ) : (
          <li className={styles.empty}>{text.empty}</li>
        )}
      </ul>

      <div className={styles.desktop}>
        <Table className={tableClass} aria-label={tableLabel}>
          <TableHeader>
            <Column
              key="selection"
              className={`react-aria-Column ${styles.selectionCell}`}
            >
              {pageSelection ? (
                <SelectionCheckbox
                  control={pageSelection}
                  label={text.selectPage}
                />
              ) : (
                <span className="sr-only">{text.selection}</span>
              )}
            </Column>
            <Column key="track" isRowHeader>
              {text.track}
            </Column>
            {showRelease && (
              <Column
                key="release"
                className={`react-aria-Column ${showDetails ? "w-[13%]" : "w-[16%]"}`}
              >
                {text.release}
              </Column>
            )}
            <Column
              key="artist"
              className={`react-aria-Column ${showDetails ? "w-[10%]" : "w-[16%]"}`}
            >
              {text.artist}
            </Column>
            <Column
              key="position"
              className={`react-aria-Column ${showDetails ? "w-[7%]" : "w-[11%]"}`}
            >
              {text.position}
            </Column>
            <Column
              key="audio"
              className={`react-aria-Column ${showDetails ? "w-[7%]" : "w-[11%]"}`}
            >
              {text.audio}
            </Column>
            <Column key="sources" className="react-aria-Column w-[11%]">
              {text.sources}
            </Column>
            {showProperties && (
              <Column key="properties" className="react-aria-Column w-[9%]">
                {text.properties}
              </Column>
            )}
            {showPaths && (
              <Column key="paths" className="react-aria-Column w-[14%]">
                {text.filePath}
              </Column>
            )}
            <Column
              key="publication"
              className={`react-aria-Column ${showDetails ? "w-[8%]" : "w-[12%]"}`}
            >
              {text.publication}
            </Column>
            {showTags && (
              <Column key="tags" className="react-aria-Column w-[6%]">
                {text.tags}
              </Column>
            )}
          </TableHeader>
          <TableBody
            items={rows}
            dependencies={[
              rows,
              text,
              showRelease,
              showProperties,
              showPaths,
              showTags,
            ]}
            renderEmptyState={() => (
              <div className={styles.empty}>{text.empty}</div>
            )}
          >
            {(row) => (
              <Row
                id={row.id}
                className={`react-aria-Row ${filterClass(row, styles.filterMutedRow) ?? ""}`}
              >
                <Cell
                  key="selection"
                  className={`react-aria-Cell ${styles.selectionCell}`}
                >
                  <SelectionCheckbox
                    control={row.selection}
                    label={rowLabel(row, text)}
                  />
                </Cell>
                <Cell key="track">
                  <TrackTitle row={row} text={text} />
                </Cell>
                {showRelease && (
                  <Cell key="release">
                    <ReleaseSummary row={row} text={text} />
                  </Cell>
                )}
                <Cell key="artist">
                  <Credits row={row} text={text} />
                </Cell>
                <Cell key="position">{row.positionLabel.trim() || "—"}</Cell>
                <Cell key="audio">
                  {row.hasAudio ? text.available : text.missing}
                </Cell>
                <Cell key="sources">
                  <SourceSummary row={row} text={text} />
                </Cell>
                {showProperties && (
                  <Cell key="properties">{row.properties || "—"}</Cell>
                )}
                {showPaths && (
                  <Cell key="paths">
                    <PositionPaths paths={row.paths} />
                  </Cell>
                )}
                <Cell key="publication">{publicationLabel(row, text)}</Cell>
                {showTags && (
                  <Cell key="tags">
                    <TagsSummary row={row} text={text} />
                  </Cell>
                )}
              </Row>
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
