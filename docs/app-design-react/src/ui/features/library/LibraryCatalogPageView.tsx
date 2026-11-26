import { type ReactNode, useRef } from "react";
import { Button, TabPanel, Tabs } from "react-aria-components";
import { ActiveFilterTags } from "../../components/ActiveFilterTags";
import { catalogueStyles } from "./catalogueStyles";
import {
  LibraryCatalogQueryControls,
  LibraryCatalogTabList,
  libraryActiveFilterTags,
  libraryViewLabel,
} from "./LibraryCatalogControls";
import type { LibraryStateFilter } from "./LibraryCatalogFilterView";
import {
  type LibraryCatalogResults,
  LibraryCatalogResultView,
} from "./LibraryCatalogResults";
import { LibraryPaginationView } from "./LibraryPaginationView";
import { librarySelectionButtonClass } from "./LibrarySelectionPreviewView";
import type {
  LibraryAvailabilityFilter,
  LibraryPublicationFilter,
} from "./libraryDetailRoute";
import type { LibraryView } from "./libraryView";

const statePanelClass =
  "grid justify-items-start gap-2 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-6 text-sm [&_h2]:m-0 [&_h2]:text-base [&_h2]:font-semibold [&_p]:m-0 [&_p]:max-w-[65ch] [&_p]:text-[var(--muted)]";

const copy = {
  en: {
    title: "Library",
    loading: "Loading library…",
    retry: "Retry",
    emptyLibrary: "Your library is empty",
    noResults: "No results",
    empty: {
      library:
        "Add and analyze a source, then match provider records into the library.",
      unpublished:
        "There are no published positions in the library yet. Unpublished releases are available under All.",
      filtered: "Change the search query or reset the filters.",
      view: "There are no records in this view yet.",
    },
    reset: "Reset filters",
    addSource: "Add a source",
  },
  ru: {
    title: "Медиатека",
    loading: "Загрузка медиатеки…",
    retry: "Повторить",
    emptyLibrary: "Медиатека пока пуста",
    noResults: "Ничего не найдено",
    empty: {
      library:
        "Добавьте источник и проанализируйте файлы, затем сопоставьте записи провайдера с медиатекой.",
      unpublished:
        "В медиатеке пока нет опубликованных позиций. Неопубликованные релизы доступны в режиме «Все».",
      filtered: "Измените поисковый запрос или сбросьте фильтры.",
      view: "В этом разделе пока нет записей.",
    },
    reset: "Сбросить фильтры",
    addSource: "Добавить источник",
  },
} as const;

const summaryCopy = {
  en: {
    fallback: "Releases and positions in your library",
    artists: ["artist", "artists"],
    releases: ["release", "releases"],
    positions: ["position", "positions"],
    published: "published",
    incoming: "Review incoming groups",
  },
  ru: {
    fallback: "Релизы и позиции медиатеки",
    artists: ["артист", "артиста", "артистов"],
    releases: ["релиз", "релиза", "релизов"],
    positions: ["позиция", "позиции", "позиций"],
    published: "опубликовано",
    incoming: "Разобрать входящие группы",
  },
} as const;

export type LibraryCatalogSummary = {
  artists: number;
  releases: number;
  positions: number;
  published: number;
};

function formatSummary(
  locale: "en" | "ru",
  summary: LibraryCatalogSummary | null,
) {
  const text = summaryCopy[locale];
  if (!summary) return text.fallback;
  const language = locale === "ru" ? "ru-RU" : "en-US";
  const numbers = new Intl.NumberFormat(language);
  const plurals = new Intl.PluralRules(language);
  function countLabel(count: number, forms: readonly string[]) {
    const plural =
      locale === "en" ? (count === 1 ? "one" : "other") : plurals.select(count);
    const index =
      plural === "one" ? 0 : locale === "en" || plural === "few" ? 1 : 2;
    return `${numbers.format(count)} ${forms[index]}`;
  }
  return [
    countLabel(summary.artists, text.artists),
    countLabel(summary.releases, text.releases),
    countLabel(summary.positions, text.positions),
    `${summary.published} ${text.published}`,
  ].join(" · ");
}

export type LibraryCatalogPageState =
  | { status: "loading" }
  | {
      status: "error";
      message: string;
      onRetry: () => void;
    }
  | {
      status: "empty";
      reason: keyof (typeof copy)["en"]["empty"];
      onReset?: () => void;
      addSourceHref?: string;
    }
  | {
      status: "results";
      membershipError?: {
        message: string;
        onRetry: () => void;
      };
    };

export interface LibraryCatalogPageViewProps {
  locale: "en" | "ru";
  view: LibraryView;
  /** Omit when the host does not provide a summary; null shows the fallback. */
  summary?: LibraryCatalogSummary | null;
  incomingHref?: string;
  /** Host shell decoration; presentation stays in this view. */
  className?: string;
  search: string;
  onSearch: (value: string) => void;
  filter: LibraryStateFilter;
  onFilter: (value: LibraryStateFilter) => void;
  /** Canonical independent filter axes; tags render each non-default axis. */
  publication?: LibraryPublicationFilter;
  availability?: LibraryAvailabilityFilter;
  /** Atomically clears only the publication axis, preserving query/availability. */
  onPublicationChange?: (value: LibraryPublicationFilter) => void;
  /** Atomically clears only the availability axis, preserving query/publication. */
  onAvailabilityChange?: (value: LibraryAvailabilityFilter) => void;
  /** Clears search and both canonical filter axes in one navigation. */
  onResetFilters?: () => void;
  onViewChange: (view: LibraryView) => void;
  state: LibraryCatalogPageState;
  /** Selected typed result leaf; the page supplies the album list locale. */
  results: LibraryCatalogResults;
  page: number;
  totalPages: number;
  totalItems: number;
  hasPrevious: boolean;
  hasNext: boolean;
  onPageChange: (page: number) => void;
  /** Host-owned selection dock placement. */
  selectionDock?: ReactNode;
  /** Host-owned selection preview placement. */
  selectionPreview?: ReactNode;
  /** Reserve the bottom dock space only when a host has an active selection. */
  dockSpace?: boolean;
}

export function LibraryCatalogPageView({
  locale,
  view,
  summary,
  incomingHref,
  className,
  search,
  onSearch,
  filter,
  onFilter,
  publication,
  availability,
  onPublicationChange,
  onAvailabilityChange,
  onResetFilters,
  onViewChange,
  state,
  results,
  page,
  totalPages,
  totalItems,
  hasPrevious,
  hasNext,
  onPageChange,
  selectionDock,
  selectionPreview,
  dockSpace = false,
}: LibraryCatalogPageViewProps) {
  const text = copy[locale];
  const searchInputRef = useRef<HTMLInputElement>(null);
  const activeFilterTags = libraryActiveFilterTags(
    locale,
    search,
    filter,
    publication && availability ? { publication, availability } : undefined,
  );
  return (
    <section
      className={`${catalogueStyles.shell}${dockSpace ? ` ${catalogueStyles.dockScreen}` : ""}${className ? ` ${className}` : ""}`}
      aria-labelledby="catalog-title"
    >
      <header className={catalogueStyles.pageHeader}>
        <div>
          <h1
            id="catalog-title"
            tabIndex={-1}
            className={catalogueStyles.pageTitle}
          >
            {text.title}
          </h1>
          {summary !== undefined && (
            <p className={catalogueStyles.pageSubtitle} aria-live="polite">
              {formatSummary(locale, summary)}
            </p>
          )}
        </div>
        {incomingHref && (
          <div className={catalogueStyles.pageHeaderAction}>
            <a
              className={`${librarySelectionButtonClass} inline-flex items-center text-sm`}
              href={incomingHref}
            >
              {summaryCopy[locale].incoming}
            </a>
          </div>
        )}
      </header>

      <div className={catalogueStyles.controls}>
        <Tabs
          className="min-w-0"
          selectedKey={view}
          onSelectionChange={(key) => onViewChange(key as LibraryView)}
        >
          <LibraryCatalogTabList locale={locale} />
          <TabPanel
            id={view}
            className={`${catalogueStyles.tabsPanel} focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2`}
          >
            <LibraryCatalogQueryControls
              locale={locale}
              search={search}
              onSearch={onSearch}
              filter={filter}
              onFilter={onFilter}
              inputRef={searchInputRef}
            />
            <ActiveFilterTags
              locale={locale}
              items={activeFilterTags}
              focusTargetRef={searchInputRef}
              onRemove={(id) => {
                if (id === "search") onSearch("");
                else if (id === "publication") onPublicationChange?.("all");
                else if (id === "availability") onAvailabilityChange?.("all");
                else if (id === "status") onFilter("all");
              }}
              onReset={
                onResetFilters ??
                (() => {
                  onSearch("");
                  onFilter("all");
                })
              }
            />

            {state.status === "loading" ? (
              <p className={statePanelClass} role="status">
                {text.loading}
              </p>
            ) : state.status === "error" ? (
              <section
                className={`${statePanelClass} text-[var(--error)]`}
                role="alert"
              >
                <p>{state.message}</p>
                <Button
                  className={librarySelectionButtonClass}
                  onPress={state.onRetry}
                >
                  {text.retry}
                </Button>
              </section>
            ) : state.status === "empty" ? (
              <section className={statePanelClass} aria-live="polite">
                <h2 className="font-semibold">
                  {state.reason === "library"
                    ? text.emptyLibrary
                    : text.noResults}
                </h2>
                <p>{text.empty[state.reason]}</p>
                {state.onReset && (
                  <Button
                    className={librarySelectionButtonClass}
                    onPress={state.onReset}
                  >
                    {text.reset}
                  </Button>
                )}
                {state.addSourceHref && (
                  <a
                    className="text-[var(--accent)] hover:underline"
                    href={state.addSourceHref}
                  >
                    {text.addSource}
                  </a>
                )}
              </section>
            ) : (
              <>
                {state.membershipError && (
                  <div
                    role="alert"
                    className="my-2 flex flex-wrap items-center gap-2 text-sm"
                  >
                    {state.membershipError.message}
                    <Button
                      className={librarySelectionButtonClass}
                      onPress={state.membershipError.onRetry}
                    >
                      {text.retry}
                    </Button>
                  </div>
                )}
                <section
                  className={
                    view === "artists" ? "min-w-0 py-1" : catalogueStyles.list
                  }
                  aria-label={libraryViewLabel(locale, view)}
                >
                  <LibraryCatalogResultView locale={locale} results={results} />
                </section>
                <LibraryPaginationView
                  locale={locale}
                  page={page}
                  totalPages={totalPages}
                  totalItems={totalItems}
                  hasPrevious={hasPrevious}
                  hasNext={hasNext}
                  onPageChange={onPageChange}
                />
              </>
            )}
          </TabPanel>
        </Tabs>
      </div>

      {selectionDock}
      {selectionPreview}
    </section>
  );
}
