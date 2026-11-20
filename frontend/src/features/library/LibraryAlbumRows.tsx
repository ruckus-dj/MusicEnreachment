import { type ReactNode, useState } from "react";
import {
  Button,
  Cell,
  Column,
  Row,
  Table,
  TableBody,
  TableHeader,
} from "react-aria-components";
import { useGetLibraryRelease } from "../../api/generated/client";
import type {
  LibraryArtistCreditResponse,
  LibraryReleaseItem,
  LibraryReleaseSummary,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import {
  LibraryAlbumPositionsView,
  type LibraryPositionSelectionControl,
} from "./LibraryAlbumPositionsView";
import { MissingReleaseGroupRow } from "./MissingReleaseGroupRow";
import type { LibrarySelectionScope } from "./useLibrarySelection";

const selectColumn =
  "box-border w-[34px] min-w-[34px] max-w-[34px] px-0! text-center! align-middle!";
const widths = [
  selectColumn,
  "",
  "w-[15%]",
  "w-[12%]",
  "w-[8%]",
  "w-[8%]",
  "w-[9%]",
  "w-[12%]",
  "w-[9%]",
];
const tableClass =
  "w-full table-fixed border-collapse text-xs wrap-anywhere [font-variant-numeric:tabular-nums] [&_th]:bg-[var(--surface)] [&_th]:text-left [&_th]:text-[var(--muted)] [&_th]:font-semibold [&_th]:px-2 [&_th]:py-2 [&_td]:px-2 [&_td]:py-1.5 [&_td]:align-top [&_th]:border-b [&_td]:border-b [&_th]:border-[var(--line)] [&_td]:border-[var(--line)]";
const detailClass =
  "min-w-0 rounded border border-[var(--line)] bg-[var(--raised)]";
const cardClass =
  "grid min-w-0 gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2 text-xs wrap-anywhere";
const subClass = "block text-[11px] text-[var(--muted)]";
const linkClass = "text-[var(--accent)] hover:underline";
const expanderClass =
  "max-w-full text-left font-semibold text-[var(--accent)] wrap-anywhere hover:underline data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]";

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
    local: "Local collection",
    missing: "Missing from library",
    untitled: "Untitled",
    unknown: "Completeness unknown",
    track: "Recording",
    loading: "Loading tracks…",
    error: "Could not load tracks.",
    retry: "Retry",
    notPublished: "Not published",
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
    local: "Локальная коллекция",
    missing: "Нет в медиатеке",
    untitled: "Без названия",
    unknown: "Полнота неизвестна",
    track: "Запись",
    loading: "Загрузка треков…",
    error: "Не удалось загрузить треки.",
    retry: "Повторить",
    notPublished: "Не опубликовано",
  },
};

type Text = (typeof copy)[keyof typeof copy];
type RenderCheckbox = (
  scope: LibrarySelectionScope,
  label: string,
) => ReactNode;
type SelectionControl = (
  scope: LibrarySelectionScope,
) => LibraryPositionSelectionControl;

function releaseHref(id: string) {
  return `#/library/releases/${encodeURIComponent(id)}`;
}

function creditsText(credits: LibraryArtistCreditResponse[]) {
  return (
    credits
      .map(
        (credit, index) =>
          `${credit.credit_name || credit.artist_name || ""}${index < credits.length - 1 ? (credit.join_phrase ?? " ") : ""}`,
      )
      .join("") || "—"
  );
}

function fields(item: LibraryReleaseItem, text: Text) {
  const local = item.library_release;
  const missing = item.missing_release_group;
  const provider =
    local?.provider_links.some(
      (link) => link.provider_code === "musicbrainz",
    ) || missing
      ? "MusicBrainz"
      : text.local;
  if (!local) {
    return [
      creditsText(missing?.artists ?? []),
      missing?.original_release_date ?? "—",
      "—",
      "—",
      text.missing,
      "—",
      provider,
    ];
  }
  const { edition, counts } = local;
  const editionName = [
    edition.country,
    ...edition.labels,
    ...edition.catalog_numbers,
  ]
    .filter(Boolean)
    .join(" · ");
  return [
    creditsText(local.artists),
    <>
      {edition.release_date ?? edition.original_release_date ?? "—"}
      {editionName && <span className={subClass}>{editionName}</span>}
    </>,
    counts.position_count,
    "—",
    counts.provider_position_count == null ? (
      <>
        {counts.available_position_count}
        <span className={subClass}>{text.unknown}</span>
      </>
    ) : (
      `${counts.available_position_count} / ${counts.provider_position_count}`
    ),
    counts.published_position_count ? (
      <>
        {counts.published_position_count} /{" "}
        {counts.provider_position_count ?? counts.position_count}
        {counts.provider_position_count == null && (
          <span className={subClass}>{text.unknown}</span>
        )}
      </>
    ) : (
      text.notPublished
    ),
    provider,
  ];
}

function AlbumTracks({
  release,
  text,
  locale,
  selectionControl,
  mobile,
}: {
  release: LibraryReleaseSummary;
  text: Text;
  locale: "en" | "ru";
  selectionControl: SelectionControl;
  mobile: boolean;
}) {
  const query = useGetLibraryRelease(release.id);
  if (query.isLoading)
    return (
      <p role="status" className="p-2">
        {text.loading}
      </p>
    );
  if (query.isError || query.data?.status !== 200)
    return (
      <div role="alert" className="flex flex-wrap items-center gap-2 p-2">
        {text.error}
        <Button className={linkClass} onPress={() => void query.refetch()}>
          {text.retry}
        </Button>
      </div>
    );
  const tracks = [...(query.data.data.tracks ?? [])].sort(
    (a, b) => a.medium - b.medium || a.position - b.position,
  );
  const positions = tracks.map((track) => {
    const sources = track.sources ?? [];
    const paths = [
      ...new Set(
        sources.flatMap(
          (source) =>
            source.locations?.map((location) => location.relative_path) ?? [],
        ),
      ),
    ];
    const href = `${releaseHref(release.id)}/positions/${encodeURIComponent(track.id)}`;
    return {
      id: track.id,
      numberLabel: `${track.medium}.${track.displayed_number || track.position}`,
      title: track.title,
      recordingHref: href,
      tagsHref: href,
      artist: creditsText(track.artists ?? []),
      durationMs: track.duration_ms ?? undefined,
      sourceCount: sources.length,
      paths,
      publication: track.publication
        ? {
            state: "published" as const,
            container: track.publication.container,
            codec: track.publication.codec,
          }
        : { state: "not-published" as const },
      selection: selectionControl({
        kind: "position",
        position: {
          release_id: release.id,
          position_id: track.id,
          title: track.title,
          release_title: release.title,
        },
      }),
    };
  });
  return (
    <LibraryAlbumPositionsView
      locale={locale}
      releaseTitle={release.title}
      positions={positions}
      layout={mobile ? "mobile" : "desktop"}
    />
  );
}

function AlbumDetail({
  item,
  text,
  locale,
  selectionControl,
  mobile = false,
}: {
  item: LibraryReleaseItem;
  text: Text;
  locale: "en" | "ru";
  selectionControl: SelectionControl;
  mobile?: boolean;
}) {
  return item.library_release ? (
    <AlbumTracks
      release={item.library_release}
      text={text}
      locale={locale}
      selectionControl={selectionControl}
      mobile={mobile}
    />
  ) : item.missing_release_group ? (
    <div className={`${detailClass} p-2`}>
      <MissingReleaseGroupRow
        group={item.missing_release_group}
        presentation="inline"
      />
    </div>
  ) : null;
}

export function LibraryAlbumRows({
  items,
  renderCheckbox,
  selectionControl,
}: {
  items: LibraryReleaseItem[];
  renderCheckbox: RenderCheckbox;
  selectionControl: SelectionControl;
}) {
  const { locale } = useI18n();
  const text = copy[locale];
  const [expanded, setExpanded] = useState<string | null>(null);
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
  const albums = items.flatMap((item) => {
    const local = item.library_release;
    const missing = item.missing_release_group;
    if (!local && !missing) return [];
    return [
      {
        id: local ? `local:${local.id}` : `provider:${missing?.provider_key}`,
        title: local?.title || missing?.title || text.untitled,
        item,
      },
    ];
  });
  const rows = albums.flatMap((album) =>
    expanded === album.id
      ? [
          { ...album, detail: false },
          { ...album, id: `${album.id}:details`, detail: true },
        ]
      : [{ ...album, detail: false }],
  );
  function checkbox(item: LibraryReleaseItem, title: string) {
    return item.library_release
      ? renderCheckbox(
          { kind: "release", id: item.library_release.id },
          `${text.select}: ${title}`,
        )
      : null;
  }
  function heading(id: string, title: string, item: LibraryReleaseItem) {
    return (
      <>
        <Button
          className={expanderClass}
          aria-expanded={expanded === id}
          onPress={() => setExpanded(expanded === id ? null : id)}
        >
          {title}
        </Button>
        {item.library_release ? (
          <a
            aria-label={`${text.open}: ${title}`}
            href={releaseHref(item.library_release.id)}
            className={`${subClass} ${linkClass}`}
          >
            {text.open}
          </a>
        ) : (
          <span className={subClass}>{text.missing}</span>
        )}
      </>
    );
  }
  return (
    <div className="w-full min-w-0">
      <div className="hidden gap-2 max-[900px]:grid">
        <div className={cardClass}>
          <div className="flex items-center gap-2">
            {renderCheckbox({ kind: "page" }, text.page)}
            {text.page}
          </div>
        </div>
        {albums.map(({ id, title, item }) => (
          <article className={cardClass} key={id}>
            <div className="flex min-w-0 items-start gap-2">
              {checkbox(item, title)}
              <div className="min-w-0">{heading(id, title, item)}</div>
            </div>
            <dl className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-x-3 gap-y-1">
              {fields(item, text).map((field, index) => (
                <div className="contents" key={headings[index + 2]}>
                  <dt className="text-[var(--muted)]">{headings[index + 2]}</dt>
                  <dd>{field}</dd>
                </div>
              ))}
            </dl>
            {expanded === id && (
              <AlbumDetail
                item={item}
                text={text}
                locale={locale}
                selectionControl={selectionControl}
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
                className={`${widths[index]} whitespace-normal! wrap-anywhere`}
                isRowHeader={index === 1}
              >
                {index === 0
                  ? renderCheckbox({ kind: "page" }, text.page)
                  : label}
              </Column>
            ))}
          </TableHeader>
          <TableBody
            items={rows}
            dependencies={[
              renderCheckbox,
              selectionControl,
              expanded,
              text,
              locale,
            ]}
          >
            {({ id, title, item, detail }) =>
              detail ? (
                <Row id={id} aria-label={`${text.track} · ${title}`}>
                  <Cell
                    colSpan={headings.length}
                    className="w-auto! min-w-0! max-w-none! px-2! text-left!"
                  >
                    <AlbumDetail
                      item={item}
                      text={text}
                      locale={locale}
                      selectionControl={selectionControl}
                    />
                  </Cell>
                </Row>
              ) : (
                <Row
                  id={id}
                  className={
                    item.missing_release_group && !item.library_release
                      ? "bg-[var(--raised)]"
                      : undefined
                  }
                >
                  <Cell className={selectColumn}>{checkbox(item, title)}</Cell>
                  <Cell>{heading(id, title, item)}</Cell>
                  {fields(item, text).map((field, index) => (
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
