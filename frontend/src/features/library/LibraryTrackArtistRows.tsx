import type { ReactNode } from "react";
import type {
  LibraryArtistCreditResponse,
  LibraryPositionPage,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
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
    selectPage: "Select all tracks on this page",
    selectTrack: "Select {track}",
  },
  ru: {
    selection: "Выбор",
    track: "Трек",
    album: "Релиз",
    artist: "Артист",
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
    selectPage: "Выбрать все треки на этой странице",
    selectTrack: "Выбрать трек {track}",
  },
} as const;

function formatCopy(template: string, values: Record<string, string | number>) {
  return template.replace(/\{([^}]+)\}/g, (_, name: string) =>
    String(values[name] ?? `{${name}}`),
  );
}

const subClass =
  "mt-[3px] block text-xs font-normal text-[var(--muted)] [overflow-wrap:anywhere]";

const tableClass =
  "w-full border-separate border-spacing-0 text-xs [font-variant-numeric:tabular-nums] [&_tbody_tr]:bg-[var(--surface)] [&_tbody_tr:hover]:bg-[var(--raised)] [&_td]:min-w-0 [&_td]:border-b [&_td]:border-[var(--line)] [&_td]:px-2 [&_td]:py-[6px] [&_td]:align-top [&_td]:[overflow-wrap:anywhere] [&_td_a]:font-semibold [&_td_a]:text-[var(--accent)] [&_td_a]:underline-offset-2 [&_td_a:hover]:underline [&_th]:whitespace-nowrap [&_th]:border-b [&_th]:border-[var(--line)] [&_th]:bg-[var(--raised)] [&_th]:px-[9px] [&_th]:py-2 [&_th]:text-left [&_th]:text-[12px] [&_th]:font-bold [&_th]:text-[var(--muted)] [&_th]:align-middle";

const selectionCellClass =
  "box-border w-[34px] min-w-[34px] max-w-[34px] px-0! text-center! align-middle!";

const mobileCardClass =
  "grid min-w-0 gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2 text-xs [font-variant-numeric:tabular-nums]";

const mobileFieldClass =
  "m-0 grid min-w-0 grid-cols-[minmax(76px,.7fr)_minmax(0,1.5fr)] gap-2 border-b border-[var(--line)] pb-1.5 last:border-b-0 last:pb-0 [&>dt]:text-[11px] [&>dt]:text-[var(--muted)] [&>dd]:m-0 [&>dd]:[overflow-wrap:anywhere] [&_a]:font-semibold [&_a]:text-[var(--accent)] [&_a]:underline-offset-2 [&_a:hover]:underline";

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
