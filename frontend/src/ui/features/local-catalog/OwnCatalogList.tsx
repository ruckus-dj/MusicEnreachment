import {
  Button,
  Input,
  Label,
  SearchField,
  Tab,
  TabList,
  TabPanel,
  Tabs,
} from "react-aria-components";
import { panel, primary } from "../local-release/localReleaseUi";
import { OwnCatalogPagination } from "./OwnCatalogPagination";

export type OwnCatalogListCopy = {
  title: string;
  description: string;
  tabListLabel: string;
  filterLabel: string;
  filterPlaceholder: string;
  createLabel: string;
  emptyLabel: string;
  loading: string;
  retry: string;
  noMatches: string;
  clearFilter: string;
};

export type OwnCatalogListTab = {
  id: string;
  label: string;
  count: number;
};

export type OwnCatalogListRow<T> = {
  id: string;
  title: string;
  detail: string;
  value: T;
};

export type OwnCatalogListPage<T> = {
  rows: readonly OwnCatalogListRow<T>[];
  total: number;
};

export type OwnCatalogListProps<T> = {
  locale: "ru" | "en";
  copy: OwnCatalogListCopy;
  tabs: readonly OwnCatalogListTab[];
  selectedTab: string;
  onTabChange: (key: string) => void;
  filter: string;
  hasFilter: boolean;
  onFilterChange: (value: string) => void;
  onClearFilter: () => void;
  onCreate: () => void;
  createDisabled?: boolean;
  loading?: boolean;
  loaded?: boolean;
  error?: string | null;
  page?: OwnCatalogListPage<T>;
  pageNumber: number;
  pageCount: number;
  busy?: boolean;
  onRetry: () => void;
  onOpenRow: (row: OwnCatalogListRow<T>) => void;
  onPageChange: (page: number) => void;
};

export function OwnCatalogList<T>({
  locale,
  copy,
  tabs,
  selectedTab,
  onTabChange,
  filter,
  hasFilter,
  onFilterChange,
  onClearFilter,
  onCreate,
  createDisabled,
  loading,
  loaded,
  error,
  page,
  pageNumber,
  pageCount,
  busy,
  onRetry,
  onOpenRow,
  onPageChange,
}: OwnCatalogListProps<T>) {
  const listRows = page?.rows ?? [];
  return (
    <section className="mx-auto max-w-none">
      <header className="mb-3">
        <h1 className="m-0 text-2xl font-semibold">{copy.title}</h1>
        <p className="m-0 text-sm text-[var(--muted)]">{copy.description}</p>
      </header>
      <Tabs
        selectedKey={selectedTab}
        onSelectionChange={(key) => onTabChange(String(key))}
        className="grid min-w-0 gap-2"
      >
        <TabList
          aria-label={copy.tabListLabel}
          className="flex flex-wrap gap-[3px] text-xs"
        >
          {tabs.map((tab) => (
            <Tab
              key={tab.id}
              id={tab.id}
              aria-label={`${tab.label}: ${tab.count}`}
              className="flex min-h-9 cursor-pointer items-center gap-1.5 rounded-[4px] border border-transparent px-[11px] py-[7px] font-semibold whitespace-nowrap text-[var(--muted)] data-[selected]:border-[var(--line)] data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--accent)] data-[selected]:shadow-[0_1px_2px_#0001] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] max-sm:px-2"
            >
              <span>{tab.label}</span>
              <span
                aria-hidden="true"
                className="inline-flex h-5 min-w-5 items-center justify-center rounded-full border border-[var(--line)] bg-[var(--raised)] px-1 text-xs leading-none font-medium text-[var(--ink)] tabular-nums"
              >
                {tab.count}
              </span>
            </Tab>
          ))}
        </TabList>
        <TabPanel id={selectedTab} className="min-w-0 pt-2">
          <section className={`${panel} grid gap-2`}>
            <div className="flex flex-wrap items-end justify-between gap-2">
              <SearchField
                className="grid w-full min-w-0 gap-1 sm:max-w-[32rem] sm:flex-1"
                value={filter}
                onChange={onFilterChange}
              >
                <Label className="text-xs font-medium">
                  {copy.filterLabel}
                </Label>
                <Input
                  className="min-h-8 w-full rounded border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]"
                  placeholder={copy.filterPlaceholder}
                />
              </SearchField>
              <Button
                className={primary}
                isDisabled={createDisabled}
                onPress={onCreate}
              >
                {copy.createLabel}
              </Button>
            </div>
            {loading ? (
              <p className="m-0 text-xs text-[var(--muted)]" role="status">
                {copy.loading}
              </p>
            ) : null}
            {error ? (
              <div className="grid justify-items-start gap-2">
                <p
                  className="m-0 text-xs font-medium text-[var(--error)]"
                  role="alert"
                >
                  {error}
                </p>
                <Button className={primary} onPress={onRetry}>
                  {copy.retry}
                </Button>
              </div>
            ) : null}
            {!error && page && loaded && page.total === 0 ? (
              hasFilter ? (
                <div className="grid justify-items-start gap-2">
                  <p className="m-0 text-xs text-[var(--muted)]">
                    {copy.noMatches}
                  </p>
                  <Button onPress={onClearFilter}>{copy.clearFilter}</Button>
                </div>
              ) : (
                <p className="m-0 text-xs text-[var(--muted)]">
                  {copy.emptyLabel}
                </p>
              )
            ) : null}
            {!error && page && listRows.length > 0 ? (
              <ul className="m-0 list-none divide-y divide-[var(--line)] border-y border-[var(--line)] p-0">
                {listRows.map((row) => (
                  <li key={row.id}>
                    <Button
                      className="flex min-h-14 w-full justify-start gap-2 border-0 bg-transparent px-3 py-2 text-left hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--focus)]"
                      isDisabled={busy}
                      onPress={() => onOpenRow(row)}
                    >
                      <span className="min-w-0">
                        <strong className="block wrap-anywhere">
                          {row.title || "—"}
                        </strong>
                        <small className="block text-[var(--muted)] wrap-anywhere">
                          {row.detail}
                        </small>
                      </span>
                    </Button>
                  </li>
                ))}
              </ul>
            ) : null}
            <OwnCatalogPagination
              locale={locale}
              page={pageNumber}
              totalPages={pageCount}
              totalItems={page?.total ?? 0}
              busy={busy}
              onPageChange={onPageChange}
            />
          </section>
        </TabPanel>
      </Tabs>
    </section>
  );
}
