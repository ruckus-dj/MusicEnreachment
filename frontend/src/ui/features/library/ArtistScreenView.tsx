import type { ComponentProps, ReactElement, ReactNode } from "react";
import { useContext } from "react";
import {
  Checkbox,
  Disclosure,
  DisclosureGroup,
  DisclosurePanel,
  DisclosureStateContext,
  Heading,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import { type CatalogueMessage, catalogueMessage } from "./catalogueMessages";
import { catalogueStyles } from "./catalogueStyles";
import { LibraryArtistHeadingView } from "./LibraryArtistHeadingView";
import {
  type LibraryArtistReleaseTrackRow,
  LibraryArtistReleaseTracksView,
} from "./LibraryArtistReleaseTracksView";
import { LibraryCatalogQueryControls } from "./LibraryCatalogControls";
import type { LibraryStateFilter } from "./LibraryCatalogFilterView";
import { LibraryEditionArtwork } from "./LibraryEditionArtwork";
import { LibraryPaginationControls } from "./LibraryPaginationView";

export type ArtistHeadingModel = {
  name: string;
  disambiguation?: string | null;
  genres: readonly string[];
  palette?: string;
  count: {
    releases: number;
    positions: number;
    published: number;
    available?: number;
    missing: number;
    local: number;
  };
  discographyComplete?: boolean;
  includesCachedGroups?: boolean;
  selection?: {
    total: number;
    selected: number;
    onChange?: (value: boolean) => void;
  };
};

/**
 * Artist-level deletion control. A workflow actor (the deletion dialog) is
 * mounted through a typed slot; the inert button fallback stays local markup.
 */
export type ArtistDeletionAction =
  | { kind: "actor"; node: ReactElement }
  | { kind: "button"; isDisabled: boolean; onPress?: () => void };

type ReleaseSelection = {
  isSelected: boolean;
  isIndeterminate?: boolean;
  isDisabled?: boolean;
  onChange?: (selected: boolean) => void;
};

export type ArtistEditionModel = {
  key: string;
  title: string;
  /** Release detail route; omitted by hosts without a release screen. */
  titleHref?: string;
  year: string;
  catalog: string;
  /** Preformatted accessibility label: title · year · catalogue number. */
  ariaLabel: string;
  artwork: {
    palette?: string;
    title: string;
    imageSrc?: string;
    imageAlt?: string;
  };
  /** Total duration in seconds, or `null` when any position is unknown. */
  durationSeconds?: number | null;
  /** Provider attribution text, already resolved by the host. */
  providerText?: string;
  positionCount: number;
  country: string;
  label: string;
  availableCount?: number;
  publishedCount: number;
  /** Declared provider total, present only when the provider cache is complete. */
  providerTrackCount: number | null;
  metadataChangedCount: number;
  selection?: ReleaseSelection;
  trackSelection?: ReleaseSelection;
  tracks?: readonly LibraryArtistReleaseTrackRow[];
  /** Narrow lazy actor that owns loading the edition positions. */
  positions?: ReactElement;
  /** Independent metadata editor actor, keyed by the host. */
  editor?: ReactElement;
  /** Optional publication workflow actor for this edition. */
  publication?: ReactElement;
  /** Optional album deletion dialog actor for this edition. */
  deletion?: ReactElement;
};

export type ArtistMissingGroupCredit = {
  name: string;
  href?: string | null;
  joinPhrase?: string | null;
};

export type ArtistMissingGroupModel = {
  key: string;
  title: string;
  artistName: string;
  originalReleaseDate?: string | null;
  providerKey?: string;
  artistCredits?: readonly ArtistMissingGroupCredit[];
  /** Independent edition chooser actor for this absent group. */
  chooser: ReactElement;
};

export type ArtistTagFocus = {
  scope: "artist" | "release";
  releaseId?: string;
  ref: (node: HTMLDivElement | null) => void;
};

/** One ordered server row: a local edition or an absent provider group. */
export type ArtistReleaseRowModel =
  | { kind: "edition"; edition: ArtistEditionModel }
  | { kind: "missing-group"; group: ArtistMissingGroupModel };

/** Separate release transport states; the heading stays visible while loading. */
export type ArtistReleasesModel =
  | { state: "loading"; message: string }
  | {
      state: "error";
      message: string;
      retryLabel: string;
      onRetry: () => void;
    }
  | {
      state: "empty";
      title: string;
      description: string;
      pagination?: ComponentProps<typeof LibraryPaginationControls> | null;
    }
  | {
      state: "ready";
      rows: readonly ArtistReleaseRowModel[];
      pagination?: ComponentProps<typeof LibraryPaginationControls> | null;
    };

/** Artist transport states shown before a heading is available. */
export type ArtistStatusModel =
  | { state: "loading"; message: string }
  | {
      state: "error";
      message: string;
      retryLabel: string;
      onRetry: () => void;
    };

export type ArtistScreenViewContent = {
  deletion: ArtistDeletionAction;
  /** Independent artist metadata editor actor, keyed by the host. */
  metadataCard: ReactElement;
  query: string;
  filter: LibraryStateFilter;
  onQueryChange: (value: string) => void;
  onFilterChange: (value: LibraryStateFilter) => void;
  editionsKey: string;
  releases: ArtistReleasesModel;
  expandedReleaseId?: string;
  tagFocus?: ArtistTagFocus;
};

export type ArtistScreenViewProps =
  | {
      locale: Locale;
      libraryHref: string;
      heading: null;
      /** Omitted means the link does not resolve to a library artist. */
      status?: ArtistStatusModel;
    }
  | ({
      locale: Locale;
      libraryHref: string;
      heading: ArtistHeadingModel;
    } & ArtistScreenViewContent);

const statePanelClass =
  "grid justify-items-start gap-2 rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm [&>h2]:m-0 [&>h2]:text-base [&>p]:m-0";
const headerGrid =
  "grid min-h-[94px] grid-cols-[68px_minmax(0,1fr)_auto] items-center gap-[13px] px-[15px] py-3 text-left max-[600px]:min-h-20 max-[600px]:grid-cols-[54px_minmax(0,1fr)_auto] max-[600px]:items-start max-[600px]:gap-[9px] max-[600px]:px-[9px] max-[600px]:py-[9px]";
const cardCheckboxFocus =
  "data-[focus-visible]:outline-2 data-[focus-visible]:outline-offset-2 data-[focus-visible]:outline-[var(--focus)]";
const toggleButton =
  "inline-flex min-h-8 cursor-pointer items-center rounded-[4px] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-xs text-[var(--ink)] hover:border-[var(--accent)] hover:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2";

/** Single trigger label driven by the enclosing Disclosure's actual state. */
function PositionsToggle({ show, hide }: { show: string; hide: string }) {
  const state = useContext(DisclosureStateContext);
  return <>{state?.isExpanded ? hide : show}</>;
}

/** Defers mounting a lazy slot until its disclosure is actually expanded. */
function ExpandedOnly({ children }: { children: ReactElement }) {
  const state = useContext(DisclosureStateContext);
  return state?.isExpanded ? children : null;
}

function MissingGroupCredits({
  credits,
}: {
  credits: readonly ArtistMissingGroupCredit[];
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
              <a href={credit.href}>{credit.name}</a>
            ) : (
              credit.name
            )}
            {credit.joinPhrase ?? (index < credits.length - 1 ? " " : "")}
          </span>
        );
      })}
    </>
  );
}

const chooserToggleCopy = {
  en: { open: "Choose an edition", closed: "Browse editions" },
  ru: { open: "Выберите издание", closed: "Показать издания" },
} as const;

/**
 * Toggle and wrapper for one absent provider group's edition chooser. The host
 * owns the open state and fetch eligibility and supplies the chooser content,
 * so this presentation stays independent from provider cache reads.
 */
export function ArtistMissingGroupChooser({
  locale,
  isExpanded,
  onToggle,
  children,
}: {
  locale: Locale;
  isExpanded: boolean;
  onToggle: () => void;
  children?: ReactNode;
}) {
  const copy = chooserToggleCopy[locale];
  return (
    <div className="grid min-w-0 gap-2">
      <AppButton aria-expanded={isExpanded} onPress={onToggle}>
        {isExpanded ? copy.open : copy.closed}
      </AppButton>
      {children}
    </div>
  );
}

/**
 * Copy-ready artist screen composition.
 *
 * The provider owns fixture lookups, metadata and selection state, edition
 * derivation and all callbacks. This module only renders prepared models; the
 * stateful editors, lazy positions and workflow dialogs cross the boundary as
 * typed element slots, so the view does not depend on catalog entities,
 * fixtures or demo execution code.
 */
export function ArtistScreenView(props: ArtistScreenViewProps) {
  const { locale, libraryHref } = props;
  const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
    catalogueMessage(locale, key, values);

  if (!props.heading)
    return (
      <section className={catalogueStyles.object}>
        <nav className={catalogueStyles.heroBack} aria-label={m("navigation")}>
          <a href={libraryHref}>{m("backToLibrary")}</a>
        </nav>
        {props.status?.state === "loading" ? (
          <p className={statePanelClass} role="status">
            {props.status.message}
          </p>
        ) : props.status?.state === "error" ? (
          <section
            className={`${statePanelClass} text-[var(--error)]`}
            role="alert"
          >
            <p>{props.status.message}</p>
            <AppButton onPress={props.status.onRetry}>
              {props.status.retryLabel}
            </AppButton>
          </section>
        ) : (
          <header className={catalogueStyles.objectHeader}>
            <div>
              <h1>{m("artistNotFound")}</h1>
              <p>{m("artistLinkMismatch")}</p>
            </div>
            <a href={libraryHref}>{m("allLibrary")}</a>
          </header>
        )}
      </section>
    );

  const {
    heading,
    deletion,
    metadataCard,
    query,
    filter,
    onQueryChange,
    onFilterChange,
    editionsKey,
    releases,
    expandedReleaseId,
    tagFocus,
  } = props;

  return (
    <section className={catalogueStyles.object} aria-labelledby="artist-title">
      <nav className={catalogueStyles.heroBack} aria-label={m("navigation")}>
        <a href={libraryHref}>{m("backToLibrary")}</a>
      </nav>
      <LibraryArtistHeadingView
        locale={locale}
        name={heading.name}
        disambiguation={heading.disambiguation}
        genres={heading.genres}
        palette={heading.palette}
        count={heading.count}
        discographyComplete={heading.discographyComplete}
        includesCachedGroups={heading.includesCachedGroups}
        selection={heading.selection}
      />
      <div
        className={`${catalogueStyles.actions} ${catalogueStyles.artistDanger}`}
      >
        {deletion.kind === "actor" ? (
          deletion.node
        ) : (
          <AppButton
            className="border-[color-mix(in_srgb,var(--error)_45%,var(--control))]! bg-transparent! text-[var(--error)]!"
            isDisabled={deletion.isDisabled}
            onPress={deletion.onPress}
          >
            {m("deleteArtist")}
          </AppButton>
        )}
      </div>
      <div
        ref={tagFocus?.scope === "artist" ? tagFocus.ref : undefined}
        tabIndex={tagFocus?.scope === "artist" ? -1 : undefined}
      >
        {metadataCard}
      </div>
      <div className={catalogueStyles.controls}>
        <LibraryCatalogQueryControls
          locale={locale}
          search={query}
          onSearch={onQueryChange}
          filter={filter}
          onFilter={onFilterChange}
        />
      </div>
      {releases.state === "loading" ? (
        <p className={statePanelClass} role="status">
          {releases.message}
        </p>
      ) : releases.state === "error" ? (
        <section
          className={`${statePanelClass} text-[var(--error)]`}
          role="alert"
        >
          <p>{releases.message}</p>
          <AppButton onPress={releases.onRetry}>
            {releases.retryLabel}
          </AppButton>
        </section>
      ) : releases.state === "empty" ? (
        <>
          <section className={statePanelClass}>
            <h2>{releases.title}</h2>
            <p>{releases.description}</p>
          </section>
          {releases.pagination && (
            <LibraryPaginationControls {...releases.pagination} />
          )}
        </>
      ) : (
        <>
          <DisclosureGroup
            key={editionsKey}
            className={catalogueStyles.editions}
            allowsMultipleExpanded
            defaultExpandedKeys={
              expandedReleaseId ? new Set([expandedReleaseId]) : undefined
            }
          >
            {releases.rows.map((row) =>
              row.kind === "edition" ? (
                <div className={catalogueStyles.albumRow} key={row.edition.key}>
                  <Disclosure
                    className={`group ${catalogueStyles.albumCard}`}
                    id={row.edition.key}
                  >
                    {row.edition.selection && (
                      <div className={catalogueStyles.cardSelection}>
                        <Checkbox
                          aria-label={`${m("selectAllEditionPositions")}: ${row.edition.ariaLabel}`}
                          className={`${catalogueStyles.cardCheckbox} ${cardCheckboxFocus}`}
                          isSelected={row.edition.selection.isSelected}
                          isIndeterminate={
                            row.edition.selection.isIndeterminate
                          }
                          isDisabled={row.edition.selection.isDisabled}
                          onChange={row.edition.selection.onChange}
                        >
                          <span
                            className={catalogueStyles.cardCheckboxBox}
                            aria-hidden="true"
                          >
                            {row.edition.selection.isSelected
                              ? "✓"
                              : row.edition.selection.isIndeterminate
                                ? "−"
                                : ""}
                          </span>
                        </Checkbox>
                      </div>
                    )}
                    <div className={headerGrid}>
                      <LibraryEditionArtwork
                        palette={row.edition.artwork.palette}
                        title={row.edition.artwork.title}
                        imageSrc={row.edition.artwork.imageSrc}
                        imageAlt={row.edition.artwork.imageAlt}
                      />
                      <div className={catalogueStyles.albumCopy}>
                        <Heading
                          level={2}
                          className="min-w-0"
                          data-library-edition-title
                        >
                          {row.edition.titleHref ? (
                            <a
                              className="text-[var(--ink)]! no-underline hover:underline"
                              href={row.edition.titleHref}
                            >
                              <strong>{row.edition.title}</strong>
                            </a>
                          ) : (
                            <strong>{row.edition.title}</strong>
                          )}
                        </Heading>
                        {row.edition.durationSeconds !== undefined && (
                          <span className={catalogueStyles.albumMeta}>
                            {row.edition.durationSeconds === null
                              ? m("durationUnknown")
                              : `${Math.floor(row.edition.durationSeconds / 60)}:${String(Math.round(row.edition.durationSeconds % 60)).padStart(2, "0")} ${m("total")}`}
                          </span>
                        )}
                        <span className={catalogueStyles.albumMeta}>
                          {row.edition.year} · {row.edition.positionCount}{" "}
                          {m("positionsCount")}
                          {row.edition.availableCount !== undefined &&
                            ` · ${row.edition.availableCount} ${m("withAudio")}`}
                          {row.edition.metadataChangedCount
                            ? ` · ${m("metadataChangedFor", { count: row.edition.metadataChangedCount })}`
                            : ""}
                        </span>
                        <span className={catalogueStyles.albumMeta}>
                          {row.edition.country || m("countryUnspecified")} ·{" "}
                          {row.edition.label || m("local")} ·{" "}
                          {row.edition.catalog || "—"}
                          {row.edition.providerText
                            ? ` · ${row.edition.providerText}`
                            : ""}
                        </span>
                        <span className={catalogueStyles.albumMeta}>
                          {row.edition.providerTrackCount !== null
                            ? `${row.edition.publishedCount}/${row.edition.providerTrackCount} ${m("publishedCount")}`
                            : `${row.edition.publishedCount} ${m("publishedCount")} · ${m("providerCompletenessUnknown")}`}
                        </span>
                      </div>
                      <div className="flex items-center gap-1">
                        <AppButton slot="trigger" className={toggleButton}>
                          <PositionsToggle
                            show={m("showPositions")}
                            hide={m("hidePositions")}
                          />
                        </AppButton>
                        {row.edition.deletion}
                      </div>
                    </div>
                    <DisclosurePanel className={catalogueStyles.albumPanel}>
                      {row.edition.editor && (
                        <div
                          ref={
                            tagFocus?.scope === "release" &&
                            tagFocus.releaseId === row.edition.key
                              ? tagFocus.ref
                              : undefined
                          }
                          tabIndex={
                            tagFocus?.scope === "release" &&
                            tagFocus.releaseId === row.edition.key
                              ? -1
                              : undefined
                          }
                          className="grid min-w-0 gap-3"
                        >
                          {row.edition.editor}
                        </div>
                      )}
                      {row.edition.publication}
                      {row.edition.positions ? (
                        <ExpandedOnly>{row.edition.positions}</ExpandedOnly>
                      ) : (
                        <LibraryArtistReleaseTracksView
                          locale={locale}
                          releaseTitle={row.edition.title}
                          releaseSelection={row.edition.trackSelection}
                          tracks={row.edition.tracks ?? []}
                        />
                      )}
                    </DisclosurePanel>
                  </Disclosure>
                </div>
              ) : (
                <div className={catalogueStyles.albumRow} key={row.group.key}>
                  <article
                    className={`${catalogueStyles.albumCard} grid gap-2 p-3`}
                  >
                    <Heading level={2} className="m-0 text-base">
                      {row.group.title}
                    </Heading>
                    <span className={catalogueStyles.albumMeta}>
                      {row.group.originalReleaseDate || m("dateNotSpecified")}
                      {row.group.artistCredits?.length ? (
                        <>
                          {" · "}
                          <MissingGroupCredits
                            credits={row.group.artistCredits}
                          />
                        </>
                      ) : row.group.artistName ? (
                        <> · {row.group.artistName}</>
                      ) : null}
                    </span>
                    {row.group.providerKey && (
                      <span className={catalogueStyles.albumMeta}>
                        {m("musicBrainzId")}: {row.group.providerKey}
                      </span>
                    )}
                    <span className={catalogueStyles.secondary}>
                      {m("cacheNotice")}
                    </span>
                    {row.group.chooser}
                  </article>
                </div>
              ),
            )}
          </DisclosureGroup>
          {releases.pagination && (
            <LibraryPaginationControls {...releases.pagination} />
          )}
        </>
      )}
    </section>
  );
}
