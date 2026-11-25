import { Checkbox } from "react-aria-components";

const copy = {
  en: {
    positions: "Edition positions",
    selectAll: "Select all positions in this edition",
    selectTrack: "Select {track}",
    columns: [
      "No.",
      "Recording",
      "Properties",
      "Has audio",
      "Source links",
      "Publication",
    ],
    yes: "Yes",
    no: "No",
    untitled: "Untitled",
    empty: "No positions added.",
    newRecording: "New recording",
    metadataChanged: "Metadata changed",
    published: "Published",
    notPublished: "Not published",
    changed: "Changed outside the application",
    missing: "Missing",
    unavailable:
      "Publication directory unavailable; file presence could not be determined",
    unchanged: "No deviations reported",
    unchecked: "File state not checked",
  },
  ru: {
    positions: "Позиции издания",
    selectAll: "Выбрать все позиции этого издания",
    selectTrack: "Выбрать {track}",
    columns: [
      "№",
      "Композиция",
      "Параметры",
      "Есть аудио",
      "Связей с исходниками",
      "Публикация",
    ],
    yes: "Да",
    no: "Нет",
    untitled: "Без названия",
    empty: "Позиции не добавлены.",
    newRecording: "Новая запись",
    metadataChanged: "Метаданные изменились",
    published: "Опубликовано",
    notPublished: "Не опубликовано",
    changed: "Изменён вне приложения",
    missing: "Отсутствует",
    unavailable: "Выходной каталог недоступен; наличие файла не установлено",
    unchanged: "Отклонений не отмечено",
    unchecked: "Состояние файла не проверено",
  },
} as const;

type Selection = {
  isSelected: boolean;
  isIndeterminate?: boolean;
  isDisabled?: boolean;
  onChange?: (selected: boolean) => void;
};

type Publication =
  | { state: "published"; container?: string; codec?: string }
  | { state: "not-published" };

export type LibraryArtistReleaseTrackRow = {
  id: string;
  numberLabel: string;
  title: string;
  href: string;
  durationMs?: number | null;
  properties?: string;
  hasAudio?: boolean;
  sourceCount: number;
  publication?: Publication;
  isNewRecording?: boolean;
  fileEvidence?: { state?: string; rootAvailable?: boolean | null };
  metadataChanged?: boolean;
  filterClass?: string;
  selection?: Selection;
};

const tableClass =
  "min-w-0 max-w-full overflow-auto rounded-[4px] border border-[var(--line)] [&_table]:w-full [&_table]:min-w-[700px] [&_table]:border-collapse [&_table]:text-[11px] [&_th]:px-2 [&_th]:py-[7px] [&_td]:px-2 [&_td]:py-[7px] [&_th]:border-b [&_th]:border-[var(--line)] [&_td]:border-b [&_td]:border-[var(--line)] [&_th]:text-left [&_td]:text-left [&_th]:align-middle [&_td]:align-top [&_th]:wrap-anywhere [&_td]:wrap-anywhere [&_th]:bg-[var(--raised)] [&_th]:text-[var(--muted)] [&_th]:font-semibold [&_tr:last-child>*]:border-b-0";
const secondary =
  "mt-[3px] block text-[11px] font-normal text-[var(--muted)] wrap-anywhere";
const selectionCell =
  "box-border w-[34px] min-w-[34px] max-w-[34px] px-0! text-center! align-middle!";

function TrackSelection({
  selection,
  label,
}: {
  selection?: Selection;
  label: string;
}) {
  if (!selection) return null;
  return (
    <Checkbox
      {...selection}
      aria-label={label}
      className="react-aria-Checkbox ui-checkbox group mx-auto grid h-8 min-h-8 w-[34px] flex-[0_0_34px] cursor-pointer place-items-center data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 pointer-coarse:min-h-[40px]"
    >
      <span
        aria-hidden="true"
        className="grid size-[18px] place-items-center rounded-[3px] border border-[var(--control)] text-sm leading-none text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)] group-data-[focus-visible]:outline group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-[3px]"
      >
        {selection.isSelected ? "✓" : selection.isIndeterminate ? "−" : ""}
      </span>
    </Checkbox>
  );
}

function duration(value?: number | null) {
  if (value == null || !Number.isFinite(value) || value < 0) return "—";
  const seconds = Math.round(value / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function fileState({
  state,
  rootAvailable,
}: NonNullable<LibraryArtistReleaseTrackRow["fileEvidence"]>) {
  if (state === "changed" || state === "drift") return "changed";
  if (state === "missing")
    return rootAvailable === true ? "missing" : "unavailable";
  if (state === "output-unavailable") return "unavailable";
  if (state === "available" || state === "unchanged") return "unchanged";
  return "unchecked";
}

function publicationText(
  publication: Publication | undefined,
  text: (typeof copy)["en" | "ru"],
) {
  if (!publication) return "—";
  if (publication.state === "not-published") return text.notPublished;
  return (
    [publication.container, publication.codec].filter(Boolean).join(" · ") ||
    text.published
  );
}

export function LibraryArtistReleaseTracksView({
  locale,
  releaseTitle,
  tracks,
  releaseSelection,
}: {
  locale: "en" | "ru";
  releaseTitle: string;
  tracks: readonly LibraryArtistReleaseTrackRow[];
  releaseSelection?: Selection;
}) {
  const text = copy[locale];
  if (!tracks.length)
    return <p className="text-[var(--muted)]">{text.empty}</p>;
  const showSelection =
    releaseSelection || tracks.some((track) => track.selection);
  return (
    <div className={tableClass}>
      <table
        aria-label={`${text.positions} · ${releaseTitle || text.untitled}`}
      >
        <thead>
          <tr>
            {showSelection && (
              <th scope="col" className={selectionCell}>
                <TrackSelection
                  selection={releaseSelection}
                  label={text.selectAll}
                />
              </th>
            )}
            {text.columns.map((column) => (
              <th key={column} scope="col">
                {column}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {tracks.map((track) => {
            const title = track.title || text.untitled;
            return (
              <tr key={track.id} className={track.filterClass}>
                {showSelection && (
                  <td className={selectionCell}>
                    <TrackSelection
                      selection={track.selection}
                      label={text.selectTrack.replace("{track}", title)}
                    />
                  </td>
                )}
                <td className="align-middle!">{track.numberLabel}</td>
                <td>
                  <a
                    className="text-[var(--accent)]! underline-offset-2 hover:underline"
                    href={track.href}
                  >
                    {title}
                  </a>
                  <span className={secondary}>
                    {duration(track.durationMs)}
                  </span>
                  {track.isNewRecording && (
                    <span className={secondary}>{text.newRecording}</span>
                  )}
                  {track.fileEvidence && (
                    <span className={secondary}>
                      {text[fileState(track.fileEvidence)]}
                    </span>
                  )}
                </td>
                <td>{track.properties || "—"}</td>
                <td>
                  {track.hasAudio === undefined
                    ? "—"
                    : track.hasAudio
                      ? text.yes
                      : text.no}
                </td>
                <td>{track.sourceCount}</td>
                <td>
                  {publicationText(track.publication, text)}
                  {track.publication?.state === "published" &&
                    track.metadataChanged && (
                      <span className={secondary}>{text.metadataChanged}</span>
                    )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
