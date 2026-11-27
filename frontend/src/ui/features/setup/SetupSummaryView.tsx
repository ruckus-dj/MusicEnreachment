import { useId } from "react";

export type SetupSummaryLocale = "en" | "ru";

export interface SetupSummaryStatus {
  healthy: boolean;
  problems: readonly string[];
}

export interface SetupSummaryPublicationValues {
  directory: string;
  format?: "source" | "mka" | "m4a" | "unselected";
  explanation?: string;
  autoCompleteAlbums?: boolean;
  removeSources?: boolean;
}

export interface SetupSummaryToolsValues {
  ffmpeg: string;
  fpcalc: string;
  notice?: string;
}

export interface SetupSummaryMetadataValues {
  musicbrainz: string;
  lrclibEnabled?: boolean;
  hasAcoustIdKey?: boolean;
  genres?: string;
}

export interface SetupSummaryTechnicalValues {
  platform: string;
  toolsDirectory: string;
  musicbrainzCheckedAt: string;
  ffmpegCheckedAt: string;
  fpcalcCheckedAt: string;
}

export interface SetupSummaryValues {
  publication: SetupSummaryPublicationValues;
  tools?: SetupSummaryToolsValues;
  metadata?: SetupSummaryMetadataValues;
  technical?: SetupSummaryTechnicalValues;
  showNextStep?: boolean;
}

export interface SetupSummaryViewProps {
  locale: SetupSummaryLocale;
  status: SetupSummaryStatus;
  values: SetupSummaryValues;
}

const copyByLocale = {
  en: {
    readyTitle: "Ready to use",
    needsAttention: "Needs attention",
    notReadyIntro: "Setup is not ready yet:",
    serverNotReady: "The server reports that setup is not ready.",
    publication: "Publication",
    serverDirectory: "Server directory",
    format: "Publication format",
    formats: {
      source: "Source format",
      mka: "MKA",
      m4a: "M4A",
      unselected: "Not selected",
    },
    whatWillHappen: "What will happen",
    automaticPublication: "Automatic publication",
    automaticPublicationEnabled:
      "Enabled — first publication starts at or above the quality threshold",
    automaticPublicationDisabled: "Disabled",
    sourceFilesAfterPublication: "Source files after successful publication",
    remove: "Remove",
    keep: "Keep",
    tools: "Tools",
    ffmpeg: "FFmpeg",
    fpcalc: "fpcalc",
    metadataAndLyrics: "Metadata and lyrics",
    musicBrainz: "MusicBrainz",
    lrclibLyrics: "LRCLIB lyrics",
    acoustid: "AcoustID",
    lyricsEnabled: "Enabled — available for search",
    lyricsDisabled: "Disabled",
    keySet: "Key is set",
    keyNotSet: "Key is not set (optional)",
    genres: "Genre catalog",
    technicalDetails: "Technical details",
    platform: "Platform",
    toolsDirectory: "Tools directory",
    lastMusicBrainzCheck: "Last MusicBrainz check",
    lastFfmpegCheck: "Last FFmpeg check",
    lastFpcalcCheck: "Last fpcalc check",
    nextStep:
      "Next: add a music source and start a scan. Import does not start automatically.",
  },
  ru: {
    readyTitle: "Готово к работе",
    needsAttention: "Требует внимания",
    notReadyIntro: "Настройка пока не готова:",
    serverNotReady: "Сервер сообщил, что конфигурация не готова.",
    publication: "Публикация",
    serverDirectory: "Каталог на сервере",
    format: "Формат публикации",
    formats: {
      source: "Исходный формат",
      mka: "MKA",
      m4a: "M4A",
      unselected: "Не выбран",
    },
    whatWillHappen: "Что будет происходить",
    automaticPublication: "Автопубликация",
    automaticPublicationEnabled:
      "Включена — первая публикация запускается при оценке не ниже порога",
    automaticPublicationDisabled: "Выключена",
    sourceFilesAfterPublication: "Исходники после успешной публикации",
    remove: "Удалять",
    keep: "Сохранять",
    tools: "Инструменты",
    ffmpeg: "FFmpeg",
    fpcalc: "fpcalc",
    metadataAndLyrics: "Метаданные и тексты",
    musicBrainz: "MusicBrainz",
    lrclibLyrics: "Тексты LRCLIB",
    acoustid: "AcoustID",
    lyricsEnabled: "Включены — доступны для поиска",
    lyricsDisabled: "Выключены",
    keySet: "Ключ задан",
    keyNotSet: "Ключ не задан (необязательно)",
    genres: "Каталог жанров",
    technicalDetails: "Технические сведения",
    platform: "Платформа",
    toolsDirectory: "Каталог инструментов",
    lastMusicBrainzCheck: "Последняя проверка MusicBrainz",
    lastFfmpegCheck: "Последняя проверка FFmpeg",
    lastFpcalcCheck: "Последняя проверка fpcalc",
    nextStep:
      "Следующий шаг: добавьте источник музыки и запустите сканирование. Импорт не запускается автоматически.",
  },
} as const;

const sectionClassName =
  "grid min-w-0 gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5";
const headingClassName = "m-0 text-sm font-semibold text-[var(--ink)]";
const listClassName = "m-0 grid gap-2";
const rowClassName = "grid min-w-0 gap-0.5";
const labelClassName = "text-xs font-semibold text-[var(--muted)]";
const valueClassName = "m-0 wrap-anywhere";

function SummaryRow({ label, value }: { label: string; value: string }) {
  return (
    <div className={rowClassName}>
      <dt className={labelClassName}>{label}</dt>
      <dd className={valueClassName}>{value}</dd>
    </div>
  );
}

function formatCheckTime(value: string, locale: SetupSummaryLocale) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });
}

export function SetupSummaryView({
  locale,
  status,
  values,
}: SetupSummaryViewProps) {
  const copy = copyByLocale[locale];
  const baseId = useId();
  const publicationId = `${baseId}-publication`;
  const toolsId = `${baseId}-tools`;
  const metadataId = `${baseId}-metadata`;
  const { publication, tools, metadata, technical } = values;
  return (
    <div className="grid min-w-0 gap-3 text-sm text-[var(--ink)]">
      <h3 className="m-0 text-base font-semibold">
        {status.healthy ? copy.readyTitle : copy.needsAttention}
      </h3>
      {!status.healthy && (
        <div
          role="alert"
          className="grid gap-1 rounded-lg border border-[color-mix(in_srgb,var(--error)_25%,transparent)] bg-[var(--error-soft)] px-3.5 py-2.5 text-sm text-[var(--error)]"
        >
          <p className="m-0">{copy.notReadyIntro}</p>
          {status.problems.length ? (
            <ul className="m-0 grid list-disc gap-1 pl-5">
              {status.problems.map((problem) => (
                <li key={problem}>{problem}</li>
              ))}
            </ul>
          ) : (
            <p className="m-0">{copy.serverNotReady}</p>
          )}
        </div>
      )}
      <section aria-labelledby={publicationId} className={sectionClassName}>
        <h4 id={publicationId} className={headingClassName}>
          {copy.publication}
        </h4>
        <dl className={listClassName}>
          <SummaryRow
            label={copy.serverDirectory}
            value={publication.directory}
          />
          {publication.format !== undefined && (
            <SummaryRow
              label={copy.format}
              value={copy.formats[publication.format]}
            />
          )}
          {publication.explanation !== undefined && (
            <SummaryRow
              label={copy.whatWillHappen}
              value={publication.explanation}
            />
          )}
          {publication.removeSources !== undefined && (
            <SummaryRow
              label={copy.sourceFilesAfterPublication}
              value={publication.removeSources ? copy.remove : copy.keep}
            />
          )}
          {publication.autoCompleteAlbums !== undefined && (
            <SummaryRow
              label={copy.automaticPublication}
              value={
                publication.autoCompleteAlbums
                  ? copy.automaticPublicationEnabled
                  : copy.automaticPublicationDisabled
              }
            />
          )}
        </dl>
      </section>
      {tools && (
        <section aria-labelledby={toolsId} className={sectionClassName}>
          <h4 id={toolsId} className={headingClassName}>
            {copy.tools}
          </h4>
          {tools.notice && (
            <p role="status" className="m-0">
              {tools.notice}
            </p>
          )}
          <dl className={listClassName}>
            <SummaryRow label={copy.ffmpeg} value={tools.ffmpeg} />
            <SummaryRow label={copy.fpcalc} value={tools.fpcalc} />
          </dl>
        </section>
      )}
      {metadata && (
        <section aria-labelledby={metadataId} className={sectionClassName}>
          <h4 id={metadataId} className={headingClassName}>
            {copy.metadataAndLyrics}
          </h4>
          <dl className={listClassName}>
            <SummaryRow label={copy.musicBrainz} value={metadata.musicbrainz} />
            {metadata.lrclibEnabled !== undefined && (
              <SummaryRow
                label={copy.lrclibLyrics}
                value={
                  metadata.lrclibEnabled
                    ? copy.lyricsEnabled
                    : copy.lyricsDisabled
                }
              />
            )}
            {metadata.hasAcoustIdKey !== undefined && (
              <SummaryRow
                label={copy.acoustid}
                value={metadata.hasAcoustIdKey ? copy.keySet : copy.keyNotSet}
              />
            )}
            {metadata.genres !== undefined && (
              <SummaryRow label={copy.genres} value={metadata.genres} />
            )}
          </dl>
        </section>
      )}
      {technical && (
        <details className={sectionClassName}>
          <summary className="cursor-pointer font-semibold">
            {copy.technicalDetails}
          </summary>
          <dl className={`${listClassName} mt-2`}>
            <SummaryRow label={copy.platform} value={technical.platform} />
            <SummaryRow
              label={copy.toolsDirectory}
              value={technical.toolsDirectory}
            />
            <SummaryRow
              label={copy.lastMusicBrainzCheck}
              value={formatCheckTime(technical.musicbrainzCheckedAt, locale)}
            />
            <SummaryRow
              label={copy.lastFfmpegCheck}
              value={formatCheckTime(technical.ffmpegCheckedAt, locale)}
            />
            <SummaryRow
              label={copy.lastFpcalcCheck}
              value={formatCheckTime(technical.fpcalcCheckedAt, locale)}
            />
          </dl>
        </details>
      )}
      {values.showNextStep && <p className="m-0">{copy.nextStep}</p>}
    </div>
  );
}
