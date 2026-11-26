import type { RefObject } from "react";
import { Tab, TabList } from "react-aria-components";
import type { ActiveFilterTag } from "../../components/ActiveFilterTags";
import { AppSearchField } from "../../components/AppSearchField";
import {
  LibraryCatalogFilterView,
  type LibraryStateFilter,
  libraryAvailabilityFilterLabel,
  libraryFilterLabel,
  libraryPublicationFilterLabel,
} from "./LibraryCatalogFilterView";
import type {
  LibraryAvailabilityFilter,
  LibraryPublicationFilter,
} from "./libraryDetailRoute";
import type { LibraryView } from "./libraryView";

export type LibraryFilterAxes = {
  publication: LibraryPublicationFilter;
  availability: LibraryAvailabilityFilter;
};

const viewOrder: readonly LibraryView[] = ["artists", "albums", "tracks"];

const labels = {
  en: {
    tabList: "Library view",
    views: {
      artists: "Artists",
      albums: "All releases",
      tracks: "All tracks",
    },
    search: "Search the library",
    searchPlaceholder: "Artist, release, track…",
    searchTag: (value: string) => `Search: “${value}”`,
  },
  ru: {
    tabList: "Раздел медиатеки",
    views: {
      artists: "Артисты",
      albums: "Все релизы",
      tracks: "Все треки",
    },
    search: "Поиск в медиатеке",
    searchPlaceholder: "Артист, релиз, трек…",
    searchTag: (value: string) => `Поиск: «${value}»`,
  },
} as const;

/** Active conditions shown above the results; "all" is hidden per axis. */
export function libraryActiveFilterTags(
  locale: "en" | "ru",
  search: string,
  filter: LibraryStateFilter,
  axes?: LibraryFilterAxes,
): ActiveFilterTag[] {
  const text = labels[locale];
  const items: ActiveFilterTag[] = [];
  const query = search.trim();
  if (query) items.push({ id: "search", label: text.searchTag(query) });
  if (!axes) {
    if (filter !== "all")
      items.push({ id: "status", label: libraryFilterLabel(locale, filter) });
    return items;
  }
  if (axes.publication !== "all")
    items.push({
      id: "publication",
      label: libraryPublicationFilterLabel(locale, axes.publication),
    });
  if (axes.availability !== "all")
    items.push({
      id: "availability",
      label: libraryAvailabilityFilterLabel(locale, axes.availability),
    });
  return items;
}

const tab =
  "flex h-8 cursor-pointer items-center rounded-md px-3 text-sm font-medium text-[var(--muted)] transition-colors data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[hovered]:not-data-[selected]:text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

const controls =
  "mb-4 grid grid-cols-[minmax(220px,1fr)_minmax(340px,1fr)] items-end gap-3 max-[1100px]:grid-cols-1 max-[600px]:gap-2";

export function libraryViewLabel(locale: "en" | "ru", view: LibraryView) {
  return labels[locale].views[view];
}

/** Renders the canonical tab list inside the host's React Aria Tabs. */
export function LibraryCatalogTabList({ locale }: { locale: "en" | "ru" }) {
  const copy = labels[locale];
  return (
    <TabList
      aria-label={copy.tabList}
      className="mb-4 inline-flex w-fit flex-wrap gap-1 rounded-lg bg-[var(--raised)] p-1"
    >
      {viewOrder.map((view) => (
        <Tab key={view} id={view} className={tab}>
          {copy.views[view]}
        </Tab>
      ))}
    </TabList>
  );
}

/** Renders the canonical debounced-search host controls and state filter. */
export function LibraryCatalogQueryControls({
  locale,
  search,
  onSearch,
  filter,
  onFilter,
  inputRef,
}: {
  locale: "en" | "ru";
  search: string;
  onSearch: (value: string) => void;
  filter: LibraryStateFilter;
  onFilter: (value: LibraryStateFilter) => void;
  inputRef?: RefObject<HTMLInputElement | null>;
}) {
  const copy = labels[locale];
  return (
    <div className={controls}>
      <AppSearchField
        aria-label={copy.search}
        placeholder={copy.searchPlaceholder}
        value={search}
        onChange={onSearch}
        inputRef={inputRef}
      />
      <LibraryCatalogFilterView
        locale={locale}
        value={filter}
        onChange={onFilter}
      />
    </div>
  );
}
