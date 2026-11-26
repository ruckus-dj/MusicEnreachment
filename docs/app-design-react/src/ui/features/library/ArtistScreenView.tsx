import type { ComponentProps, ReactElement } from "react";
import { useRef } from "react";
import { DisclosureGroup, Heading } from "react-aria-components";
import { ActiveFilterTags } from "../../components/ActiveFilterTags";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import { ArtistEditionRowView } from "./ArtistEditionRowView";
import { type CatalogueMessage, catalogueMessage } from "./catalogueMessages";
import { catalogueStyles } from "./catalogueStyles";
import { LibraryArtistHeadingView } from "./LibraryArtistHeadingView";
import type { LibraryArtistReleaseTrackRow } from "./LibraryArtistReleaseTracksView";
import {
  LibraryCatalogQueryControls,
  libraryActiveFilterTags,
} from "./LibraryCatalogControls";
import type { LibraryStateFilter } from "./LibraryCatalogFilterView";
import { LibraryPaginationControls } from "./LibraryPaginationView";
import type {
  LibraryAvailabilityFilter,
  LibraryPublicationFilter,
} from "./libraryDetailRoute";

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
  /** Canonical independent filter axes; tags render each non-default axis. */
  publication?: LibraryPublicationFilter;
  availability?: LibraryAvailabilityFilter;
  /** Atomically clears only the publication axis, preserving query/availability. */
  onPublicationChange?: (value: LibraryPublicationFilter) => void;
  /** Atomically clears only the availability axis, preserving query/publication. */
  onAvailabilityChange?: (value: LibraryAvailabilityFilter) => void;
  /** Clears search and both canonical filter axes in one navigation. */
  onResetFilters?: () => void;
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
  "grid justify-items-start gap-2 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 text-sm [&>h2]:m-0 [&>h2]:text-base [&>h2]:font-semibold [&>p]:m-0 [&>p]:text-[var(--muted)]";

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
  const searchInputRef = useRef<HTMLInputElement>(null);

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
    publication,
    availability,
    onPublicationChange,
    onAvailabilityChange,
    onResetFilters,
    editionsKey,
    releases,
    expandedReleaseId,
    tagFocus,
  } = props;

  const activeFilterTags = libraryActiveFilterTags(
    locale,
    query,
    filter,
    publication && availability ? { publication, availability } : undefined,
  );

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
          inputRef={searchInputRef}
        />
        <ActiveFilterTags
          locale={locale}
          items={activeFilterTags}
          focusTargetRef={searchInputRef}
          onRemove={(id) => {
            if (id === "search") onQueryChange("");
            else if (id === "publication") onPublicationChange?.("all");
            else if (id === "availability") onAvailabilityChange?.("all");
            else if (id === "status") onFilterChange("all");
          }}
          onReset={
            onResetFilters ??
            (() => {
              onQueryChange("");
              onFilterChange("all");
            })
          }
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
                <ArtistEditionRowView
                  key={row.edition.key}
                  locale={locale}
                  edition={row.edition}
                  tagFocus={tagFocus}
                />
              ) : (
                <div className={catalogueStyles.albumRow} key={row.group.key}>
                  <article
                    className={`${catalogueStyles.albumCard} grid gap-2 p-5`}
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
