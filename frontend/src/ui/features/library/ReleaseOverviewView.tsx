import type { ReactElement } from "react";
import { Checkbox } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import type { Locale } from "../../i18n/types";
import { type CatalogueMessage, catalogueMessage } from "./catalogueMessages";
import { catalogueStyles } from "./catalogueStyles";
import {
  type LibraryArtistReleaseTrackRow,
  LibraryArtistReleaseTracksView,
} from "./LibraryArtistReleaseTracksView";
import { LibraryEditionArtwork } from "./LibraryEditionArtwork";
import {
  LibraryPrimaryArtistControl,
  type LibraryPrimaryArtistModel,
} from "./LibraryPrimaryArtistControl";
import {
  ReleaseMetadataEditorOutlet,
  type ReleaseMetadataEditorSource,
} from "./metadata/ReleaseMetadataEditorOutlet";
import {
  ReleaseCoverEditorOutlet,
  type ReleaseCoverEditorSource,
} from "./ReleaseCoverEditorOutlet";

export type ReleaseOverviewSelection = {
  isSelected: boolean;
  isIndeterminate?: boolean;
  isDisabled?: boolean;
  onChange?: (selected: boolean) => void;
};

export type ReleaseOverviewHeading = {
  title: string;
  disambiguation?: string | null;
  year?: string | null;
  country?: string | null;
  label?: string | null;
  catalog?: string | null;
  /** Provider attribution text, already resolved by the host. */
  providerText?: string | null;
  palette?: string;
  artwork: {
    imageSrc?: string;
    imageAlt?: string;
  };
  counts: {
    positions: number;
    published: number;
    available?: number;
  };
  /** Total duration in seconds, or `null` when any position is unknown. */
  durationSeconds?: number | null;
  selection?: ReleaseOverviewSelection;
};

export type ReleaseOverviewCredit = {
  name: string;
  canonicalName?: string | null;
  href?: string | null;
  joinPhrase?: string | null;
  role?: string | null;
};

export type ReleaseOverviewMedia = {
  key: string;
  /** One-based medium number used by the release positions. */
  number: number;
  title: string;
  format: string;
  tracks: readonly LibraryArtistReleaseTrackRow[];
};

type ReleaseOverviewContent = {
  heading: ReleaseOverviewHeading;
  /** Optional exact artist destination for quick navigation. */
  artistHref?: string;
  credentials: readonly ReleaseOverviewCredit[];
  media: readonly ReleaseOverviewMedia[];
  /** Saved positions shown when the release has no medium records. */
  fallbackTracks?: readonly LibraryArtistReleaseTrackRow[];
  /** Saved positions that do not belong to any recorded medium. */
  unassignedTracks?: readonly LibraryArtistReleaseTrackRow[];
  /** Narrow slots for the existing management actors. */
  metadataEditor?: ReleaseMetadataEditorSource;
  /** Whether the retained metadata editor is on the active surface. */
  publication?: ReactElement;
  deletion?: ReactElement;
  /** Real-only release cover management; not bound by the prototype. */
  cover?: ReleaseCoverEditorSource;
  /** Real-only provider refresh; not bound by the prototype. */
  providerRefresh?: ReactElement;
  primaryArtist?: LibraryPrimaryArtistModel;
  primaryArtistResolution?: {
    ready: boolean;
    backHref: string;
    trackHref: string;
  };
};

export type ReleaseOverviewViewProps = {
  locale: Locale;
  libraryHref: string;
  metadataEditorVisible?: boolean;
} & (
  | {
      heading: null;
      unavailable?:
        | { status: "loading"; message: string }
        | {
            status: "error";
            title: string;
            message: string;
            action: { label: string; onPress: () => void };
          };
    }
  | ReleaseOverviewContent
);

const selectionClass =
  "group inline-flex min-h-9 cursor-pointer items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 py-1.5 text-sm text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2 data-[disabled]:cursor-default data-[disabled]:opacity-50";
const selectionBox =
  "grid size-4 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-xs leading-none text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)]";
const sectionHeading = "m-0 text-base leading-[22px] font-[650] wrap-anywhere";
const subHeading = "m-0 text-sm font-semibold wrap-anywhere";
const meta = "m-0 text-sm text-[var(--muted)] wrap-anywhere";

/**
 * Copy that belongs to the exact release destination only. The paired
 * catalogue namespace stays identical to the application copy.
 */
const exactReleaseCopy = {
  en: {
    releaseNotFound: "Release not found",
    releaseLinkMismatch: "This link does not match a library release.",
    backToArtist: "Artist",
    releaseArtists: "Release artists",
    releaseMedia: "Media and positions",
    medium: "Medium",
    mediaUntitled: "Untitled",
    mediaUnspecified: "Not specified",
    mediaPositionsUnspecified: "No positions specified",
    mediaNotSaved: "Media information has not been saved.",
    releasePositions: "Release positions:",
    positionsWithoutMedia: "Positions without saved media information:",
  },
  ru: {
    releaseNotFound: "Издание не найдено",
    releaseLinkMismatch: "Эта ссылка не соответствует изданию медиатеки.",
    backToArtist: "К артисту",
    releaseArtists: "Исполнители релиза",
    releaseMedia: "Носители и позиции",
    medium: "Носитель",
    mediaUntitled: "Без названия",
    mediaUnspecified: "Не указан",
    mediaPositionsUnspecified: "Позиции не указаны",
    mediaNotSaved: "Сведения о носителях не сохранены.",
    releasePositions: "Позиции релиза:",
    positionsWithoutMedia: "Позиции без сведений о носителе:",
  },
} as const;

type ExactReleaseMessage = keyof (typeof exactReleaseCopy)["en"];

function totalDuration(seconds: number | null | undefined, unknown: string) {
  if (seconds == null || !Number.isFinite(seconds) || seconds < 0)
    return unknown;
  const rounded = Math.round(seconds);
  return `${Math.floor(rounded / 60)}:${String(rounded % 60).padStart(2, "0")}`;
}

function CreditList({
  locale,
  credits,
}: {
  locale: Locale;
  credits: readonly ReleaseOverviewCredit[];
}) {
  const m = (key: CatalogueMessage) => catalogueMessage(locale, key);
  if (!credits.length) return <p className={meta}>{m("notSpecified")}</p>;
  const occurrences = new Map<string, number>();
  const keyedCredits = credits.map((credit) => {
    const identity = JSON.stringify(credit);
    const occurrence = occurrences.get(identity) ?? 0;
    occurrences.set(identity, occurrence + 1);
    return { credit, key: `${identity}:${occurrence}` };
  });
  return (
    <ul className="my-1 flex list-none flex-wrap gap-x-4 gap-y-2 p-0">
      {keyedCredits.map(({ credit, key }, index) => (
        <li key={key}>
          {credit.href ? (
            <a
              className="text-[var(--accent)] hover:underline"
              href={credit.href}
            >
              {credit.name}
            </a>
          ) : (
            credit.name
          )}
          {credit.canonicalName && credit.canonicalName !== credit.name && (
            <span className="text-[var(--muted)]">{` (${credit.canonicalName})`}</span>
          )}
          {credit.joinPhrase ?? (index < credits.length - 1 ? " " : "")}
          {credit.role ? (
            <span className="text-[var(--muted)]"> · {credit.role}</span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

/**
 * Copy-ready focused release composition. The host resolves all release facts,
 * selection and workflow state; management editors and publication/deletion
 * dialogs cross the boundary as narrow element slots. Cover and provider
 * refresh remain real-only capabilities, deliberately unbound here.
 */
export function ReleaseOverviewView(props: ReleaseOverviewViewProps) {
  const { locale, libraryHref } = props;
  const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
    catalogueMessage(locale, key, values);
  const t = (key: ExactReleaseMessage) => exactReleaseCopy[locale][key];

  if (props.heading === null) {
    const { unavailable } = props;
    if (unavailable?.status === "loading")
      return <p role="status">{unavailable.message}</p>;
    if (unavailable?.status === "error")
      return (
        <section className={catalogueStyles.object}>
          <h1>{unavailable.title}</h1>
          <p role="alert" className="text-[var(--error)]">
            {unavailable.message}
          </p>
          <AppButton onPress={unavailable.action.onPress}>
            {unavailable.action.label}
          </AppButton>
        </section>
      );
    return (
      <section className={catalogueStyles.object}>
        <nav className={catalogueStyles.heroBack} aria-label={m("navigation")}>
          <a href={libraryHref}>{m("backToLibrary")}</a>
        </nav>
        <header className={catalogueStyles.objectHeader}>
          <div>
            <h1>{t("releaseNotFound")}</h1>
            <p>{t("releaseLinkMismatch")}</p>
          </div>
          <a href={libraryHref}>{m("allLibrary")}</a>
        </header>
      </section>
    );
  }

  const {
    heading,
    artistHref,
    credentials,
    media,
    fallbackTracks,
    unassignedTracks,
    metadataEditor,
    metadataEditorVisible,
    publication,
    deletion,
    cover,
    providerRefresh,
    primaryArtist,
    primaryArtistResolution,
  } = props;
  const formatter = new Intl.NumberFormat(locale);
  const selection = heading.selection;
  const selected = Boolean(selection?.isSelected);
  const mixed = Boolean(selection?.isIndeterminate);
  const duration = totalDuration(heading.durationSeconds, m("durationUnknown"));

  return (
    <section className={catalogueStyles.object} aria-labelledby="release-title">
      <nav className={catalogueStyles.heroBack} aria-label={m("navigation")}>
        <a href={libraryHref}>{m("backToLibrary")}</a>
        {artistHref && (
          <>
            {" · "}
            <a href={artistHref}>{t("backToArtist")}</a>
          </>
        )}
      </nav>
      <header className="grid min-w-0 gap-3 border-b border-[var(--line)] pb-4">
        <div className="flex min-w-0 items-center gap-[13px] max-[600px]:gap-[9px]">
          <LibraryEditionArtwork
            title={heading.title}
            palette={heading.palette}
            imageSrc={heading.artwork.imageSrc}
            imageAlt={heading.artwork.imageAlt}
          />
          <div className="grid min-w-0 gap-1 wrap-anywhere">
            <h1
              id="release-title"
              className="m-0 text-[26px]/[1.2] font-semibold tracking-[-.02em]"
            >
              {heading.title}
            </h1>
            {heading.disambiguation && (
              <p className={meta}>{heading.disambiguation}</p>
            )}
            {(heading.year ||
              heading.country ||
              heading.label ||
              heading.catalog) && (
              <p className={meta}>
                {heading.year || m("dateNotSpecified")} ·{" "}
                {heading.country || m("countryUnspecified")} ·{" "}
                {heading.label || "—"} · {heading.catalog || "—"}
              </p>
            )}
            {heading.providerText && (
              <p className={meta}>{heading.providerText}</p>
            )}
            <p className={meta}>
              {formatter.format(heading.counts.positions)} {m("positionsCount")}{" "}
              · {formatter.format(heading.counts.published)}{" "}
              {m("publishedCount")}
              {heading.counts.available !== undefined &&
                ` · ${m("withAudio")}: ${formatter.format(heading.counts.available)}`}
              {` · ${duration} ${m("total")}`}
            </p>
          </div>
        </div>
        {(selection || deletion) && (
          <div className="flex flex-wrap items-center gap-2">
            {selection && (
              <Checkbox
                aria-label={m("selectAllEditionPositions")}
                className={selectionClass}
                isSelected={selected}
                isIndeterminate={mixed}
                isDisabled={selection.isDisabled || !selection.onChange}
                onChange={selection.onChange}
              >
                <span aria-hidden="true" className={selectionBox}>
                  {selected ? (
                    <AppIcon name="check" className="size-3" />
                  ) : mixed ? (
                    <span className="h-0.5 w-2 rounded bg-current" />
                  ) : null}
                </span>
                <span>{m("selectAllEditionPositions")}</span>
              </Checkbox>
            )}
            {deletion}
          </div>
        )}
      </header>
      <section
        className="grid min-w-0 gap-1"
        aria-labelledby="release-credentials-heading"
      >
        <h2 id="release-credentials-heading" className={subHeading}>
          {t("releaseArtists")}
        </h2>
        <CreditList locale={locale} credits={credentials} />
        {primaryArtist && (
          <div
            className={
              primaryArtistResolution
                ? "mt-2 grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--raised)] p-3"
                : undefined
            }
          >
            {primaryArtistResolution && (
              <p className={meta} role="status">
                {primaryArtistResolution.ready
                  ? locale === "ru"
                    ? "Основной исполнитель сохранён. Теперь можно открыть предпросмотр публикации."
                    : "Primary artist saved. You can now preview publication."
                  : locale === "ru"
                    ? "Выберите основного исполнителя для пути публикации. Состав исполнителей релиза не изменится."
                    : "Choose the primary artist for the publication path. The release credits will not change."}
              </p>
            )}
            <LibraryPrimaryArtistControl
              locale={locale}
              model={primaryArtist}
              focusOnMount={
                Boolean(primaryArtistResolution) &&
                metadataEditorVisible !== false
              }
            />
            {primaryArtistResolution && (
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
                {primaryArtistResolution.ready && (
                  <a
                    className="inline-flex min-h-9 items-center font-semibold text-[var(--accent-strong)] underline"
                    href={primaryArtistResolution.trackHref}
                  >
                    {locale === "ru"
                      ? "К публикации трека"
                      : "Open track publication"}
                  </a>
                )}
                <a
                  className="inline-flex min-h-9 items-center text-[var(--accent-strong)] underline"
                  href={primaryArtistResolution.backHref}
                >
                  {locale === "ru"
                    ? "Назад к очереди публикации"
                    : "Back to publication queue"}
                </a>
              </div>
            )}
          </div>
        )}
      </section>
      {providerRefresh}
      {metadataEditor && (
        <div className="grid min-w-0 gap-3">
          <ReleaseMetadataEditorOutlet
            source={metadataEditor}
            visible={metadataEditorVisible ?? true}
          />
        </div>
      )}
      <section
        className="grid min-w-0 gap-3"
        aria-labelledby="release-media-heading"
      >
        <h2 id="release-media-heading" className={sectionHeading}>
          {t("releaseMedia")}
        </h2>
        {media.length ? (
          media.map((medium) => {
            const label = `${t("medium")} ${medium.number} · ${
              medium.title || t("mediaUntitled")
            } · ${medium.format || t("mediaUnspecified")}`;
            return (
              <section className="min-w-0" key={medium.key} aria-label={label}>
                <h3 className={`${subHeading} mb-1.5`}>{label}</h3>
                {medium.tracks.length ? (
                  <LibraryArtistReleaseTracksView
                    locale={locale}
                    releaseTitle={label}
                    tracks={medium.tracks}
                  />
                ) : (
                  <p className={meta}>{t("mediaPositionsUnspecified")}</p>
                )}
              </section>
            );
          })
        ) : (
          <p className={meta}>{t("mediaNotSaved")}</p>
        )}
        {!media.length && fallbackTracks?.length ? (
          <div className="min-w-0">
            <p className={meta}>{t("releasePositions")}</p>
            <LibraryArtistReleaseTracksView
              locale={locale}
              releaseTitle={heading.title}
              tracks={fallbackTracks}
            />
          </div>
        ) : null}
        {media.length && unassignedTracks?.length ? (
          <div className="min-w-0">
            <p className={meta}>{t("positionsWithoutMedia")}</p>
            <LibraryArtistReleaseTracksView
              locale={locale}
              releaseTitle={heading.title}
              tracks={unassignedTracks}
            />
          </div>
        ) : null}
      </section>
      {publication}
      {cover && <ReleaseCoverEditorOutlet locale={locale} source={cover} />}
    </section>
  );
}
