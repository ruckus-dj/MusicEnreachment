import { useRef } from "react";
import { Button, Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import { ActiveFilterTags } from "../../components/ActiveFilterTags";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import { AppSearchField } from "../../components/AppSearchField";
import { AppToolbar } from "../../components/AppToolbar";
import { PageHeader } from "../../components/PageHeader";
import { ui } from "../../components/uiStyles";
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

/**
 * Data-only row. The provider owns the record behind `id` and supplies the
 * action, so the shared list never receives a domain-specific value.
 */
export type OwnCatalogListRow = {
  id: string;
  title: string;
  detail: string;
  onOpen: () => void;
};

export type OwnCatalogListPage = {
  rows: readonly OwnCatalogListRow[];
  total: number;
};

export type OwnCatalogListProps = {
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
  page?: OwnCatalogListPage;
  pageNumber: number;
  pageCount: number;
  busy?: boolean;
  onRetry: () => void;
  onPageChange: (page: number) => void;
};

const tabClass =
  "flex h-8 cursor-pointer items-center gap-2 rounded-md px-3 text-sm font-medium text-[var(--muted)] transition-colors data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[hovered]:not-data-[selected]:text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

export function OwnCatalogList({
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
  onPageChange,
}: OwnCatalogListProps) {
  const listRows = page?.rows ?? [];
  const filterInputRef = useRef<HTMLInputElement>(null);
  return (
    <section className="mx-auto w-full min-w-0 max-w-[1100px]">
      <PageHeader title={copy.title} subtitle={copy.description} />
      <Tabs
        selectedKey={selectedTab}
        onSelectionChange={(key) => onTabChange(String(key))}
        className="grid min-w-0 gap-4"
      >
        <TabList
          aria-label={copy.tabListLabel}
          className="inline-flex w-fit flex-wrap gap-1 rounded-lg bg-[var(--raised)] p-1"
        >
          {tabs.map((tab) => (
            <Tab
              key={tab.id}
              id={tab.id}
              aria-label={`${tab.label}: ${tab.count}`}
              className={tabClass}
            >
              <span>{tab.label}</span>
              <span
                aria-hidden="true"
                className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-[var(--surface)] px-1.5 text-xs leading-none font-medium text-[var(--muted)] tabular-nums ring-1 ring-[var(--line)]"
              >
                {tab.count}
              </span>
            </Tab>
          ))}
        </TabList>
        <TabPanel id={selectedTab} className="min-w-0">
          <div className="grid gap-5">
            <AppToolbar label={copy.title}>
              <AppSearchField
                aria-label={copy.filterLabel}
                placeholder={copy.filterPlaceholder}
                value={filter}
                onChange={onFilterChange}
                inputRef={filterInputRef}
                className="w-full max-w-[420px] flex-1"
              />
              <AppButton
                variant="primary"
                isDisabled={createDisabled}
                onPress={onCreate}
              >
                <AppIcon name="plus" />
                {copy.createLabel}
              </AppButton>
            </AppToolbar>
            <ActiveFilterTags
              locale={locale}
              items={
                filter.trim()
                  ? [
                      {
                        id: "query",
                        label: `${copy.filterLabel}: ${filter}`,
                      },
                    ]
                  : []
              }
              focusTargetRef={filterInputRef}
              onRemove={() => onClearFilter()}
              onReset={() => onClearFilter()}
            />
            {loading ? (
              <p className="m-0 text-sm text-[var(--muted)]" role="status">
                {copy.loading}
              </p>
            ) : null}
            {error ? (
              <div className="grid justify-items-start gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5">
                <p className={ui.fieldError} role="alert">
                  {error}
                </p>
                <AppButton onPress={onRetry}>{copy.retry}</AppButton>
              </div>
            ) : null}
            {!error && page && loaded && page.total === 0 ? (
              hasFilter ? (
                <div className="grid justify-items-start gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-6">
                  <p className="m-0 text-sm text-[var(--muted)]">
                    {copy.noMatches}
                  </p>
                  <AppButton onPress={onClearFilter}>
                    {copy.clearFilter}
                  </AppButton>
                </div>
              ) : (
                <p className="m-0 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-6 text-sm text-[var(--muted)]">
                  {copy.emptyLabel}
                </p>
              )
            ) : null}
            {!error && page && listRows.length > 0 ? (
              <ul
                className={`${ui.tableFrame} m-0 list-none divide-y divide-[var(--line)] p-0`}
              >
                {listRows.map((row) => (
                  <li key={row.id}>
                    <Button
                      className="group flex min-h-14 w-full items-center justify-between gap-4 border-0 bg-transparent px-4 py-3 text-left transition-colors hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--accent)]"
                      isDisabled={busy}
                      onPress={row.onOpen}
                    >
                      <span className="min-w-0">
                        <strong className="block text-sm font-medium text-[var(--ink)] wrap-anywhere">
                          {row.title || "—"}
                        </strong>
                        <small className="mt-0.5 block text-xs text-[var(--muted)] wrap-anywhere">
                          {row.detail}
                        </small>
                      </span>
                      <AppIcon
                        name="chevronRight"
                        className="size-4 shrink-0 text-[var(--control)] transition-colors group-hover:text-[var(--ink)]"
                      />
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
          </div>
        </TabPanel>
      </Tabs>
    </section>
  );
}
