import { Input, SearchField, Tab, TabList } from "react-aria-components";
import {
  LibraryCatalogFilterView,
  type LibraryStateFilter,
} from "./LibraryCatalogFilterView";
import type { LibraryView } from "./libraryView";

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
  },
} as const;

const tab =
  "flex min-h-9 cursor-pointer items-center rounded-[4px] border border-transparent px-[11px] py-[7px] font-semibold text-[var(--muted)] data-[selected]:border-[var(--line)] data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--accent)] data-[selected]:shadow-[0_1px_2px_#0001] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]";

const controls =
  "mb-3 grid grid-cols-[minmax(220px,1fr)_minmax(340px,1fr)] items-end gap-3 max-[1100px]:grid-cols-1 max-[600px]:gap-2";

const searchField =
  "min-h-[34px] min-w-0 rounded-[4px] border border-[var(--control)] bg-[var(--surface)] px-2 focus-within:outline focus-within:outline-2 focus-within:outline-[var(--focus)] focus-within:outline-offset-1";

const searchInput = "min-h-8 w-full text-xs outline-none";

export function libraryViewLabel(locale: "en" | "ru", view: LibraryView) {
  return labels[locale].views[view];
}

/** Renders the canonical tab list inside the host's React Aria Tabs. */
export function LibraryCatalogTabList({ locale }: { locale: "en" | "ru" }) {
  const copy = labels[locale];
  return (
    <TabList
      aria-label={copy.tabList}
      className="mb-3 flex flex-wrap gap-[3px] text-xs"
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
}: {
  locale: "en" | "ru";
  search: string;
  onSearch: (value: string) => void;
  filter: LibraryStateFilter;
  onFilter: (value: LibraryStateFilter) => void;
}) {
  const copy = labels[locale];
  return (
    <div className={controls}>
      <SearchField
        aria-label={copy.search}
        className={searchField}
        value={search}
        onChange={onSearch}
      >
        <Input className={searchInput} placeholder={copy.searchPlaceholder} />
      </SearchField>
      <LibraryCatalogFilterView
        locale={locale}
        value={filter}
        onChange={onFilter}
      />
    </div>
  );
}
