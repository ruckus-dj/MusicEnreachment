import type { ReactNode } from "react";
import type {
  LibraryArtistCreditResponse,
  LibraryArtistPage,
  LibraryPositionPage,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { type LibraryRoute, libraryArtistRouteHref } from "./libraryRoute";
import type { LibrarySelectionScope } from "./useLibrarySelection";

type RenderCheckbox = (
  scope: LibrarySelectionScope,
  label: string,
) => ReactNode;

const rowCopy = {
  en: {
    selection: "Selection",
    track: "Track",
    album: "Release",
    artist: "Artist",
    artistsLibrary: "Artists",
    position: "Position",
    audio: "Audio",
    sources: "Sources",
    publication: "Publication",
    tracksTable: "Tracks",
    available: "Available",
    missing: "Missing",
    published: "Published",
    unpublished: "Unpublished",
    unnamed: "Untitled",
    durationUnknown: "Duration not specified",
    unknownArtist: "Unnamed artist",
    sourceCount: "Sources: {count}",
    countPublished: "published",
    cacheNotice:
      "includes known groups from the cache; the discography may be incomplete",
    selectPage: "Select all tracks on this page",
    selectArtist: "Select all positions by {artist}",
    selectTrack: "Select {track}",
    releaseCount: {
      en: ["release", "releases"],
      ru: ["релиз", "релиза", "релизов"],
    },
    positionCount: {
      en: ["position", "positions"],
      ru: ["позиция", "позиции", "позиций"],
    },
  },
  ru: {
    selection: "Выбор",
    track: "Трек",
    album: "Релиз",
    artist: "Артист",
    artistsLibrary: "Артисты",
    position: "Позиция",
    audio: "Аудио",
    sources: "Исходники",
    publication: "Публикация",
    tracksTable: "Треки",
    available: "В наличии",
    missing: "Отсутствует",
    published: "Опубликовано",
    unpublished: "Не опубликовано",
    unnamed: "Без названия",
    durationUnknown: "Длительность не указана",
    unknownArtist: "Артист не указан",
    sourceCount: "Исходники: {count}",
    countPublished: "опубликовано",
    cacheNotice:
      "включая известные группы из кэша; дискография может быть неполной",
    selectPage: "Выбрать все треки на этой странице",
    selectArtist: "Выбрать все позиции артиста {artist}",
    selectTrack: "Выбрать трек {track}",
    releaseCount: {
      en: ["release", "releases"],
      ru: ["релиз", "релиза", "релизов"],
    },
    positionCount: {
      en: ["position", "positions"],
      ru: ["позиция", "позиции", "позиций"],
    },
  },
} as const;

function countLabel(
  count: number,
  locale: "en" | "ru",
  forms: {
    en: readonly [string, string];
    ru: readonly [string, string, string];
  },
) {
  const formatted = new Intl.NumberFormat(
    locale === "ru" ? "ru-RU" : "en-US",
  ).format(count);
  if (locale === "en") return `${formatted} ${forms.en[count === 1 ? 0 : 1]}`;
  const plural = new Intl.PluralRules("ru-RU").select(count);
  return `${formatted} ${forms.ru[plural === "one" ? 0 : plural === "few" ? 1 : 2]}`;
}

function formatCopy(template: string, values: Record<string, string | number>) {
  return template.replace(/\{([^}]+)\}/g, (_, name: string) =>
    String(values[name] ?? `{${name}}`),
  );
}

const palettes = ["clay", "forest", "dusk", "sand", "slate"] as const;

function paletteFor(id: string) {
  let hash = 0;
  for (const character of id) {
    hash = (hash * 31 + (character.codePointAt(0) ?? 0)) | 0;
  }
  return palettes[Math.abs(hash) % palettes.length];
}

const subClass =
  "mt-[3px] block text-xs font-normal text-[var(--muted)] [overflow-wrap:anywhere]";

const artistGridClass =
  "grid grid-cols-[repeat(auto-fill,minmax(min(100%,224px),1fr))] gap-[14px] p-[clamp(12px,1.5vw,20px)] max-[600px]:grid-cols-1 max-[600px]:gap-[11px] max-[600px]:p-[10px]";

const artistCardClass =
  "min-w-0 transition-transform duration-[180ms] motion-reduce:transition-none [@media(hover:hover)_and_(pointer:fine)]:hover:-translate-y-[3px]";

const artistArtClass =
  "relative isolate aspect-[1.18/1] min-h-0 w-full min-w-0 overflow-hidden rounded-[11px] border border-[color-mix(in_srgb,var(--artist-color)_45%,var(--line))] bg-[linear-gradient(145deg,color-mix(in_srgb,var(--artist-color)_77%,#111),color-mix(in_srgb,var(--artist-color)_46%,#101820))] text-white shadow-[0_7px_20px_color-mix(in_srgb,var(--artist-color)_20%,transparent)] [--artist-color:#ac7059] transition-shadow duration-[180ms] data-[palette=forest]:[--artist-color:#486d59] data-[palette=dusk]:[--artist-color:#676e95] data-[palette=sand]:[--artist-color:#aa905f] data-[palette=slate]:[--artist-color:#4c717e] before:pointer-events-none before:absolute before:inset-[10%_8%_13%_31%] before:z-[-1] before:rounded-[48%_52%_42%_58%/54%_38%_62%_46%] before:border before:border-[#ffffff47] before:bg-[linear-gradient(135deg,#ffffff16,transparent_68%)] before:transition-transform before:duration-300 before:[transform:rotate(-18deg)] after:pointer-events-none after:absolute after:right-[14%] after:top-[12%] after:z-[-1] after:aspect-square after:w-[38%] after:rounded-full after:border after:border-[#ffffff30] after:bg-[radial-gradient(circle_at_35%_32%,#ffffff28,#ffffff05_68%)] after:transition-transform after:duration-300 [@media(hover:hover)_and_(pointer:fine)]:hover:shadow-[0_14px_32px_color-mix(in_srgb,var(--artist-color)_30%,transparent)] [@media(hover:hover)_and_(pointer:fine)]:hover:before:[transform:rotate(-18deg)_scale(1.04)] [@media(hover:hover)_and_(pointer:fine)]:hover:after:scale-105 motion-reduce:before:transition-none motion-reduce:after:transition-none motion-reduce:hover:translate-y-0";

const artistLinkClass =
  "absolute inset-0 flex flex-col justify-between overflow-hidden p-[17px] text-white no-underline hover:text-white hover:no-underline focus-visible:rounded-[inherit] focus-visible:outline focus-visible:outline-[3px] focus-visible:outline-[var(--focus)] focus-visible:-outline-offset-1";

const artistMonogramClass =
  "block size-[62px] text-center text-[42px] font-semibold leading-[62px] text-white [text-shadow:0_2px_15px_#0005]";

const artistOverlayClass =
  "-mx-[17px] -mb-[17px] -mt-[17px] grid gap-[11px] bg-[linear-gradient(transparent,#101415e8_22%,#101415f2)] px-[17px] pb-[15px] pt-[34px]";

const artistCopyClass =
  "grid min-w-0 gap-[3px] [&_strong]:text-[clamp(16px,1.3vw,20px)] [&_strong]:leading-[1.15] [&_strong]:tracking-[-.02em] [&_strong]:[overflow-wrap:anywhere] [&>span]:text-[11px] [&>span]:text-[#ffffffbf]";

const artistStatsClass =
  "grid grid-cols-2 gap-x-[10px] gap-y-[6px] text-[10px] leading-[1.25] text-[#ffffffbd] [&>span]:min-w-0 [&_strong]:text-[11px] [&_strong]:text-white [&_strong]:[font-variant-numeric:tabular-nums]";

const tableClass =
  "w-full border-separate border-spacing-0 text-xs [font-variant-numeric:tabular-nums] [&_tbody_tr]:bg-[var(--surface)] [&_tbody_tr:hover]:bg-[var(--raised)] [&_td]:min-w-0 [&_td]:border-b [&_td]:border-[var(--line)] [&_td]:px-2 [&_td]:py-[6px] [&_td]:align-top [&_td]:[overflow-wrap:anywhere] [&_td_a]:font-semibold [&_td_a]:text-[var(--accent)] [&_td_a]:underline-offset-2 [&_td_a:hover]:underline [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-[var(--line)] [&_th]:bg-[var(--raised)] [&_th]:px-[9px] [&_th]:py-2 [&_th]:text-left [&_th]:text-[12px] [&_th]:font-bold [&_th]:text-[var(--muted)] [&_th]:align-middle";

const selectionCellClass =
  "box-border w-[34px] min-w-[34px] max-w-[34px] px-0! text-center! align-middle!";

const mobileCardClass =
  "grid min-w-0 gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2 text-xs [font-variant-numeric:tabular-nums]";

const mobileFieldClass =
  "m-0 grid min-w-0 grid-cols-[minmax(76px,.7fr)_minmax(0,1.5fr)] gap-2 border-b border-[var(--line)] pb-1.5 last:border-b-0 last:pb-0 [&>dt]:text-[11px] [&>dt]:text-[var(--muted)] [&>dd]:m-0 [&>dd]:[overflow-wrap:anywhere] [&_a]:font-semibold [&_a]:text-[var(--accent)] [&_a]:underline-offset-2 [&_a:hover]:underline";

export function LibraryArtistCards({
  items,
  route,
  renderCheckbox,
}: {
  items: LibraryArtistPage["items"];
  route: LibraryRoute;
  renderCheckbox: RenderCheckbox;
}) {
  const { locale } = useI18n();
  const copy = rowCopy[locale];
  return (
    <ul
      className={`${artistGridClass} m-0 list-none`}
      aria-label={copy.artistsLibrary}
    >
      <li className="col-span-full flex items-center gap-2 text-xs text-[var(--muted)]">
        {renderCheckbox({ kind: "page" }, copy.selectPage)}
        {copy.selectPage}
      </li>
      {items.map((artist) => (
        <li className={artistCardClass} key={artist.id}>
          <div className={artistArtClass} data-palette={paletteFor(artist.id)}>
            <a
              className={artistLinkClass}
              href={libraryArtistRouteHref(artist.id, route)}
            >
              <span className={artistMonogramClass} aria-hidden="true">
                {artist.name.trim().charAt(0).toLocaleUpperCase() || "?"}
              </span>
              <span className={artistOverlayClass}>
                <span className={artistCopyClass}>
                  <strong>{artist.name}</strong>
                  <span>{artist.genres.join(" · ") || copy.artist}</span>
                </span>
                <span className={artistStatsClass}>
                  <span>
                    <strong>
                      {countLabel(
                        artist.release_count,
                        locale,
                        copy.releaseCount,
                      )}
                    </strong>
                  </span>
                  <span>
                    <strong>
                      {countLabel(
                        artist.position_count,
                        locale,
                        copy.positionCount,
                      )}
                    </strong>
                  </span>
                  <span className="col-span-2">
                    <strong>
                      {copy.audio}: {artist.available_position_count} ·{" "}
                      {artist.published_position_count} {copy.countPublished}
                    </strong>
                  </span>
                  <span className="col-span-2 text-[#ffffffa8]">
                    {copy.cacheNotice}
                  </span>
                </span>
              </span>
            </a>
            <div className="absolute left-[9px] top-[9px] z-[2] grid size-6 place-items-center">
              {renderCheckbox(
                { kind: "artist", id: artist.id },
                formatCopy(copy.selectArtist, { artist: artist.name }),
              )}
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}

function CreditNames({
  credits,
  unknownArtist,
}: {
  credits: LibraryArtistCreditResponse[];
  unknownArtist: string;
}) {
  const occurrences = new Map<string, number>();
  const keyedCredits = credits.map((credit) => {
    const identity = JSON.stringify(credit);
    const occurrence = occurrences.get(identity) ?? 0;
    occurrences.set(identity, occurrence + 1);
    return { credit, key: `${identity}-${occurrence}` };
  });
  return (
    <span>
      {keyedCredits.map(({ credit, key }, index) => (
        <span key={key}>
          {credit.artist_id ? (
            <a
              href={`#/library/artists/${encodeURIComponent(credit.artist_id)}`}
            >
              {credit.credit_name || credit.artist_name || unknownArtist}
            </a>
          ) : (
            credit.credit_name || credit.artist_name || unknownArtist
          )}
          {credit.join_phrase ?? (index < credits.length - 1 ? " " : "")}
        </span>
      ))}
    </span>
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
    <dl className={mobileFieldClass}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </dl>
  );
}

function durationLabel(milliseconds: number | null, unknown: string): string {
  if (milliseconds == null) return unknown;
  return `${Math.floor(milliseconds / 60000)}:${String(Math.floor(milliseconds / 1000) % 60).padStart(2, "0")}`;
}

export function LibraryPositionRows({
  items,
  renderCheckbox,
}: {
  items: LibraryPositionPage["items"];
  renderCheckbox: RenderCheckbox;
}) {
  const { locale } = useI18n();
  const copy = rowCopy[locale];
  return (
    <>
      <ul
        className="m-0 hidden list-none grid-cols-1 gap-2 p-0 max-[599px]:grid"
        aria-label={copy.tracksTable}
      >
        <li className={`${mobileCardClass} flex items-center`}>
          {renderCheckbox({ kind: "page" }, copy.selectPage)}
          {copy.selectPage}
        </li>
        {items.map((position) => {
          const title = position.title || copy.unnamed;
          return (
            <li className={mobileCardClass} key={position.id}>
              <MobileField label={copy.selection}>
                {renderCheckbox(
                  {
                    kind: "position",
                    position: {
                      release_id: position.release.id,
                      position_id: position.id,
                      title,
                      release_title: position.release.title,
                    },
                  },
                  formatCopy(copy.selectTrack, { track: title }),
                )}
              </MobileField>
              <MobileField label={copy.track}>
                <a
                  href={`#/library/releases/${encodeURIComponent(position.release.id)}/positions/${encodeURIComponent(position.id)}`}
                >
                  {title}
                </a>
                <span className={subClass}>
                  {durationLabel(
                    position.provider_duration_ms,
                    copy.durationUnknown,
                  )}
                </span>
              </MobileField>
              <MobileField label={copy.album}>
                <a
                  href={`#/library/releases/${encodeURIComponent(position.release.id)}`}
                >
                  {position.release.title || copy.unnamed}
                </a>
              </MobileField>
              <MobileField label={copy.artist}>
                <CreditNames
                  credits={position.artists}
                  unknownArtist={copy.unknownArtist}
                />
              </MobileField>
              <MobileField label={copy.position}>
                {position.medium} ·{" "}
                {position.displayed_number || position.position}
              </MobileField>
              <MobileField label={copy.audio}>
                {position.audio_available ? copy.available : copy.missing}
              </MobileField>
              <MobileField label={copy.sources}>
                {formatCopy(copy.sourceCount, {
                  count: position.source_variant_count,
                })}
              </MobileField>
              <MobileField label={copy.publication}>
                {position.publication ? copy.published : copy.unpublished}
              </MobileField>
            </li>
          );
        })}
      </ul>
      <div className="w-full overflow-auto max-[599px]:hidden">
        <table className={tableClass} aria-label={copy.tracksTable}>
          <thead>
            <tr>
              <th className={selectionCellClass} scope="col">
                {renderCheckbox({ kind: "page" }, copy.selectPage)}
              </th>
              <th scope="col">{copy.track}</th>
              <th scope="col">{copy.album}</th>
              <th scope="col">{copy.artist}</th>
              <th scope="col">{copy.position}</th>
              <th scope="col">{copy.audio}</th>
              <th scope="col">{copy.sources}</th>
              <th scope="col">{copy.publication}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((position) => {
              const title = position.title || copy.unnamed;
              return (
                <tr key={position.id}>
                  <td className={selectionCellClass}>
                    {renderCheckbox(
                      {
                        kind: "position",
                        position: {
                          release_id: position.release.id,
                          position_id: position.id,
                          title,
                          release_title: position.release.title,
                        },
                      },
                      formatCopy(copy.selectTrack, { track: title }),
                    )}
                  </td>
                  <td>
                    <a
                      href={`#/library/releases/${encodeURIComponent(position.release.id)}/positions/${encodeURIComponent(position.id)}`}
                    >
                      {title}
                    </a>
                    <span className={subClass}>
                      {durationLabel(
                        position.provider_duration_ms,
                        copy.durationUnknown,
                      )}
                    </span>
                  </td>
                  <td>
                    <a
                      href={`#/library/releases/${encodeURIComponent(position.release.id)}`}
                    >
                      {position.release.title || copy.unnamed}
                    </a>
                  </td>
                  <td>
                    <CreditNames
                      credits={position.artists}
                      unknownArtist={copy.unknownArtist}
                    />
                  </td>
                  <td>
                    {position.medium} ·{" "}
                    {position.displayed_number || position.position}
                  </td>
                  <td>
                    {position.audio_available ? copy.available : copy.missing}
                  </td>
                  <td>
                    {formatCopy(copy.sourceCount, {
                      count: position.source_variant_count,
                    })}
                  </td>
                  <td>
                    {position.publication ? copy.published : copy.unpublished}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}
