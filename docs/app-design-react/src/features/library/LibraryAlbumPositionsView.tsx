import type { ReactNode } from "react";
import { Checkbox } from "react-aria-components";

/**
 * Plain selection state supplied by a host. The view owns the checkbox markup
 * and never receives a rendered control, so application and prototype hosts can
 * project their own selection models without sharing JSX.
 */
export interface LibraryPositionSelectionControl {
  isSelected: boolean;
  isIndeterminate: boolean;
  isDisabled: boolean;
  onChange: (selected: boolean) => void;
}

export interface LibraryPublishedAlbumPosition {
  state: "published";
  container: string;
  codec: string;
  matchingConfidence?: number | null;
  /** Optional published title shown under the separate tags link. */
  title?: string | null;
}

/** An absent publication is unknown and is displayed as an em dash. */
export type LibraryAlbumPositionPublication =
  | LibraryPublishedAlbumPosition
  | { state: "not-published" }
  | { state: "unknown" };

export interface LibraryAlbumPosition {
  id: string;
  /** Medium and position number already formatted by the host, e.g. "1.03". */
  numberLabel: string;
  title: string;
  /** Position/recording detail route. */
  recordingHref: string;
  /** Track tags route, deliberately separate from the recording link. */
  tagsHref: string;
  artist?: string | null;
  recordingTitle?: string | null;
  durationMs?: number | null;
  /** Number of source files attached to this position. Zero is a known value. */
  sourceCount: number;
  /** Absent when the host has no explicit audio hint; unknown stays an em dash. */
  hasAudio?: boolean;
  /** Optional codec or format summary shown above the source count. */
  sourceLabel?: string | null;
  sourceSelection?: "unselected" | "missing";
  /** Source paths from inventory; an empty list is displayed as an em dash. */
  paths?: readonly string[];
  /** Optional audio properties; absent values keep the em dash. */
  properties?: string | null;
  publication?: LibraryAlbumPositionPublication;
  isNewRecording?: boolean;
  /** Presentation of a position relative to the active filter. */
  filterState?: "match" | "muted";
  selection: LibraryPositionSelectionControl;
}

/**
 * Details-only position view. The album row summary is rendered by the parent,
 * so this component carries no release heading, facts or provider totals.
 */
export interface LibraryAlbumPositionsViewProps {
  locale: "en" | "ru";
  /** Release title used for the positions table/cards accessible label. */
  releaseTitle: string;
  positions: readonly LibraryAlbumPosition[];
  layout: "desktop" | "mobile";
  /** Optional desktop-only "select all release positions" control. */
  releaseSelection?: LibraryPositionSelectionControl;
}

type Locale = LibraryAlbumPositionsViewProps["locale"];

const copy = {
  en: {
    positionsHeading: "Album positions",
    select: "Select",
    selectReleasePositions: "Select all positions in this release",
    number: "No.",
    recording: "Recording",
    duration: "Duration",
    source: "Source",
    properties: "Properties",
    filePath: "File path",
    publication: "Publication",
    tags: "Tags",
    empty: "No positions.",
    notPublished: "Not published",
    matchingConfidence: "Match",
    hasAudio: "Has audio",
    noAudio: "No audio",
    noSourceSelected: "No source selected",
    noFile: "No file",
    sourceCount: "Sources: {count}",
    newRecording: "New recording",
    untitled: "Untitled",
  },
  ru: {
    positionsHeading: "Позиции альбома",
    select: "Выбрать",
    selectReleasePositions: "Выбрать все позиции этого издания",
    number: "№",
    recording: "Запись",
    duration: "Длительность",
    source: "Источник",
    properties: "Свойства",
    filePath: "Путь к файлу",
    publication: "Публикация",
    tags: "Теги",
    empty: "Нет позиций.",
    notPublished: "Не опубликовано",
    matchingConfidence: "Совпадение",
    hasAudio: "Есть аудио",
    noAudio: "Нет аудио",
    noSourceSelected: "Источник не выбран",
    noFile: "Нет файла",
    sourceCount: "Источников: {count}",
    newRecording: "Новая запись",
    untitled: "Без названия",
  },
} as const;

type Text = (typeof copy)[Locale];

const styles = {
  root: "min-w-0 text-xs [font-variant-numeric:tabular-nums]",
  sub: "mt-[3px] block text-[11px] font-normal text-[var(--muted)] [overflow-wrap:anywhere]",
  mobile: "grid min-w-0 gap-2",
  mobileCard:
    "grid min-w-0 gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2 text-xs wrap-anywhere",
  mobileHeading: "flex min-w-0 items-start gap-2",
  mobileFields: "grid min-w-0 gap-1",
  mobileField:
    "m-0 grid min-w-0 grid-cols-[minmax(76px,.7fr)_minmax(0,1.5fr)] gap-2 border-b border-[var(--line)] pb-1.5 last:border-b-0 last:pb-0 [&>dt]:text-[11px] [&>dt]:text-[var(--muted)] [&>dd]:m-0 [&>dd]:[overflow-wrap:anywhere]",
  desktop: "min-w-0",
  tableWrap:
    "min-w-0 overflow-auto rounded border border-[var(--line)] bg-[var(--raised)]",
  table:
    "w-full table-fixed border-collapse text-xs wrap-anywhere [font-variant-numeric:tabular-nums] [&_th]:bg-[var(--surface)] [&_th]:px-2 [&_th]:py-2 [&_th]:text-left [&_th]:font-semibold [&_th]:text-[var(--muted)] [&_td]:border-b [&_td]:border-[var(--line)] [&_th]:border-b [&_th]:border-[var(--line)] [&_td]:px-2 [&_td]:py-1.5 [&_td]:align-top [&_td:nth-child(2)]:align-middle",
  selectCell:
    "box-border w-[34px] min-w-[34px] max-w-[34px] px-0! text-center! align-middle!",
  checkbox:
    "group grid size-8 shrink-0 cursor-pointer place-items-center data-[disabled]:cursor-default data-[disabled]:opacity-45 data-[focus-visible]:outline-2 data-[focus-visible]:outline-offset-2 data-[focus-visible]:outline-[var(--focus)]",
  checkboxBox:
    "grid size-[18px] place-items-center rounded-[3px] border border-[var(--control)] bg-[var(--surface)] text-sm leading-none text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)]",
  number: "whitespace-nowrap [font-variant-numeric:tabular-nums]",
  link: "font-semibold text-[var(--accent)] underline-offset-2 hover:underline",
  path: "block font-mono text-[11px] [overflow-wrap:anywhere]",
  empty: "m-0 p-3 text-center text-[var(--muted)]",
  filterMatch:
    "bg-[linear-gradient(to_right,color-mix(in_srgb,var(--accent)_4%,transparent),transparent)] bg-left-top bg-no-repeat [background-size:9%_100%]",
  filterMutedRow: "[&>td]:opacity-[.55] [&:focus-within>td]:opacity-100",
  filterMutedCard: "[&_dl]:opacity-[.55] [&:focus-within_dl]:opacity-100",
} as const;

const columnWidths = [
  styles.selectCell,
  "w-[8%]",
  "",
  "w-[7%]",
  "w-[13%]",
  "w-[9%]",
  "w-[24%]",
  "w-[11%]",
  "w-[9%]",
] as const;

function fill(message: string, values: Record<string, string | number>) {
  return message.replace(/\{([^}]+)\}/g, (_, name: string) =>
    String(values[name] ?? `{${name}}`),
  );
}

function formatDuration(durationMs: number | null | undefined) {
  if (durationMs == null || !Number.isFinite(durationMs) || durationMs < 0)
    return "—";
  const totalSeconds = Math.floor(durationMs / 1000);
  return `${Math.floor(totalSeconds / 60)}:${String(totalSeconds % 60).padStart(2, "0")}`;
}

function selectionLabel(position: LibraryAlbumPosition, text: Text) {
  return `${text.select}: ${position.title || text.untitled}`;
}

function filterClass(
  filterState: LibraryAlbumPosition["filterState"],
  muted: string,
) {
  if (filterState === "match") return styles.filterMatch;
  if (filterState === "muted") return muted;
  return "";
}

function SelectionCheckbox({
  control,
  label,
}: {
  control: LibraryPositionSelectionControl;
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

function publicationLabel(
  publication: LibraryAlbumPositionPublication | undefined,
  text: Text,
) {
  if (!publication || publication.state === "unknown") return "—";
  if (publication.state === "not-published") return text.notPublished;
  const format = `${publication.container} · ${publication.codec}`;
  return publication.matchingConfidence == null
    ? format
    : `${format} · ${text.matchingConfidence}: ${Math.round(publication.matchingConfidence * 100)}%`;
}

function publishedTitle(position: LibraryAlbumPosition) {
  const publication = position.publication;
  return publication?.state === "published" ? publication.title : null;
}

function SourcePrimary({
  position,
  text,
}: {
  position: LibraryAlbumPosition;
  text: Text;
}) {
  const label =
    position.sourceLabel ||
    (position.sourceSelection === "unselected"
      ? text.noSourceSelected
      : position.sourceSelection === "missing"
        ? text.noFile
        : undefined);
  const count = fill(text.sourceCount, { count: position.sourceCount });
  const summary =
    position.hasAudio === undefined
      ? count
      : `${count} · ${position.hasAudio ? text.hasAudio : text.noAudio}`;
  return (
    <>
      {label ? (
        <>
          {label}
          <span className={styles.sub}>{summary}</span>
        </>
      ) : (
        summary
      )}
    </>
  );
}

function PositionTitle({
  position,
  text,
}: {
  position: LibraryAlbumPosition;
  text: Text;
}) {
  return (
    <>
      <a className={styles.link} href={position.recordingHref}>
        {position.title || text.untitled}
      </a>
      <span className={styles.sub}>
        {position.artist || "—"}
        {position.recordingTitle ? ` · ${position.recordingTitle}` : ""}
      </span>
      {position.isNewRecording && (
        <span className={styles.sub}>{text.newRecording}</span>
      )}
    </>
  );
}

function PositionPaths({ position }: { position: LibraryAlbumPosition }) {
  const paths = [...new Set((position.paths ?? []).filter(Boolean))];
  return paths.length
    ? paths.map((path) => (
        <span className={styles.path} key={path}>
          {path}
        </span>
      ))
    : "—";
}

function PositionTags({
  position,
  text,
}: {
  position: LibraryAlbumPosition;
  text: Text;
}) {
  const title = publishedTitle(position);
  return (
    <>
      <a className={styles.link} href={position.tagsHref}>
        {text.tags}
      </a>
      {title && <span className={styles.sub}>{title}</span>}
    </>
  );
}

function MobileCards({
  positions,
  text,
}: {
  positions: readonly LibraryAlbumPosition[];
  text: Text;
}) {
  return (
    <div className={styles.mobile}>
      {positions.length ? (
        positions.map((position) => (
          <article
            className={[
              styles.mobileCard,
              filterClass(position.filterState, styles.filterMutedCard),
            ].join(" ")}
            key={position.id}
          >
            <div className={styles.mobileHeading}>
              <SelectionCheckbox
                control={position.selection}
                label={selectionLabel(position, text)}
              />
              <span className={styles.number}>{position.numberLabel}</span>
              <div className="min-w-0">
                <PositionTitle position={position} text={text} />
              </div>
            </div>
            <div className={styles.mobileFields}>
              <MobileField label={text.duration}>
                {formatDuration(position.durationMs)}
              </MobileField>
              <MobileField label={text.source}>
                <SourcePrimary position={position} text={text} />
              </MobileField>
              <MobileField label={text.properties}>
                {position.properties || "—"}
              </MobileField>
              <MobileField label={text.filePath}>
                <PositionPaths position={position} />
              </MobileField>
              <MobileField label={text.publication}>
                {publicationLabel(position.publication, text)}
              </MobileField>
              <MobileField label={text.tags}>
                <PositionTags position={position} text={text} />
              </MobileField>
            </div>
          </article>
        ))
      ) : (
        <p className={styles.empty}>{text.empty}</p>
      )}
    </div>
  );
}

function DesktopTable({
  positions,
  releaseSelection,
  text,
  label,
}: {
  positions: readonly LibraryAlbumPosition[];
  releaseSelection?: LibraryPositionSelectionControl;
  text: Text;
  label: string;
}) {
  const headings = [
    text.select,
    text.number,
    text.recording,
    text.duration,
    text.source,
    text.properties,
    text.filePath,
    text.publication,
    text.tags,
  ];
  return (
    <div className={styles.desktop}>
      <div className={styles.tableWrap}>
        <table className={styles.table} aria-label={label}>
          <thead>
            <tr>
              {headings.map((heading, index) => (
                <th className={columnWidths[index]} key={heading} scope="col">
                  {index === 0 ? (
                    releaseSelection ? (
                      <SelectionCheckbox
                        control={releaseSelection}
                        label={text.selectReleasePositions}
                      />
                    ) : (
                      <span className="sr-only">{heading}</span>
                    )
                  ) : (
                    heading
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {positions.length ? (
              positions.map((position) => (
                <tr
                  className={
                    filterClass(position.filterState, styles.filterMutedRow) ||
                    undefined
                  }
                  key={position.id}
                >
                  <td className={styles.selectCell}>
                    <SelectionCheckbox
                      control={position.selection}
                      label={selectionLabel(position, text)}
                    />
                  </td>
                  <td>{position.numberLabel}</td>
                  <td>
                    <PositionTitle position={position} text={text} />
                  </td>
                  <td>{formatDuration(position.durationMs)}</td>
                  <td>
                    <SourcePrimary position={position} text={text} />
                  </td>
                  <td>{position.properties || "—"}</td>
                  <td>
                    <PositionPaths position={position} />
                  </td>
                  <td>{publicationLabel(position.publication, text)}</td>
                  <td>
                    <PositionTags position={position} text={text} />
                  </td>
                </tr>
              ))
            ) : (
              <tr>
                <td className={styles.empty} colSpan={headings.length}>
                  {text.empty}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function LibraryAlbumPositionsView({
  locale,
  releaseTitle,
  positions,
  layout,
  releaseSelection,
}: LibraryAlbumPositionsViewProps) {
  const text = copy[locale];
  const title = releaseTitle.trim() || text.untitled;
  const label = `${text.positionsHeading} · ${title}`;

  if (layout === "mobile") {
    return (
      <section className={styles.root} aria-label={label}>
        <MobileCards positions={positions} text={text} />
      </section>
    );
  }

  return (
    <section className={styles.root}>
      <DesktopTable
        positions={positions}
        releaseSelection={releaseSelection}
        text={text}
        label={label}
      />
    </section>
  );
}
