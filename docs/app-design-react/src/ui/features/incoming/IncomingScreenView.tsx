import { useState } from "react";
import { Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import type { Locale } from "../../i18n/types";
import {
  IncomingGroupsOverviewView,
  type IncomingGroupsOverviewViewProps,
} from "./IncomingGroupsOverviewView";
import {
  IncomingGroupsPanel,
  IncomingPageHeading,
} from "./IncomingGroupsPanel";
import { IncomingPublicationView } from "./IncomingPublicationView";
import {
  type IncomingPublicationModel,
  incomingPublicationCounts,
} from "./incomingPublicationModel";

const sectionsCopy = {
  en: {
    label: "Incoming sections",
    matching: "Matching",
    publication: "To publish",
  },
  ru: {
    label: "Разделы входящих",
    matching: "Сопоставление",
    publication: "К публикации",
  },
} as const;

const topTabListClass =
  "mb-4 inline-flex w-fit min-w-0 flex-wrap items-center gap-1 rounded-lg bg-[var(--raised)] p-1";
const topTabClass =
  "flex h-8 cursor-pointer items-center gap-1.5 rounded-md px-3 text-sm font-medium text-[var(--muted)] transition-colors data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";
const topCountClass =
  "inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-[var(--surface)] px-1.5 text-xs leading-none font-medium text-[var(--muted)] tabular-nums ring-1 ring-[var(--line)]";
const topTabPanelClass =
  "min-w-0 focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";

const stagesCopy = {
  en: {
    stages: "Processing stages",
    groupFiles: "Group files",
    match: "Match",
    allConfidentAutomatic: "All assignments confident",
    automaticDirection: "automatic",
    otherwiseManual: "Otherwise",
    manualDirection: "manual review, including partial groups",
  },
  ru: {
    stages: "Этапы обработки",
    groupFiles: "Сгруппировать файлы",
    match: "Сопоставить",
    allConfidentAutomatic: "Все назначения уверенные",
    automaticDirection: "автоматически",
    otherwiseManual: "Иначе",
    manualDirection: "ручной разбор, в том числе частичный",
  },
} as const;

function InfoIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 16 16"
      className="mt-0.5 size-4 flex-none"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
    >
      <circle cx="8" cy="8" r="6.25" />
      <path d="M8 7.25v3.5" strokeLinecap="round" />
      <circle cx="8" cy="4.9" r="0.75" fill="currentColor" stroke="none" />
    </svg>
  );
}

function SpinnerIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 16 16"
      className="size-4 flex-none motion-safe:animate-spin"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
    >
      <circle cx="8" cy="8" r="5.75" opacity="0.25" />
      <path d="M8 2.25A5.75 5.75 0 0 1 13.75 8" strokeLinecap="round" />
    </svg>
  );
}

/**
 * Copy-ready incoming list composition.
 *
 * The provider owns the groups model, automatic processing and regrouping
 * workflows. This view owns the section heading and action, the optional demo
 * processing stages, notice, loading and error regions, the groups panel and
 * the overview table supplied through `panel`. An optional publication queue
 * moves the groups under the default section tab without changing them.
 */
export type IncomingScreenViewProps = {
  locale: Locale;
  hidden?: boolean;
  headingAction: { label: string; onPress: () => void };
  loading?: string;
  error?: string;
  notice?: string;
  showProcessingStages?: boolean;
  panel?: IncomingGroupsOverviewViewProps & { status?: string };
  matchingCount?: number;
  publication?: IncomingPublicationModel;
  selectedSection?: "matching" | "publication";
  onSectionChange?: (section: "matching" | "publication") => void;
};

export function IncomingScreenView({
  locale,
  hidden,
  headingAction,
  loading,
  error,
  notice,
  showProcessingStages = false,
  panel,
  matchingCount = panel?.rows.length ?? 0,
  publication,
  selectedSection,
  onSectionChange,
}: IncomingScreenViewProps) {
  const [section, setSection] = useState<"matching" | "publication">(
    "matching",
  );
  const stages = stagesCopy[locale];
  const sections = sectionsCopy[locale];
  const publicationCounts = publication
    ? incomingPublicationCounts(publication.editions)
    : null;
  const activeSection = publication ? (selectedSection ?? section) : "matching";
  const panelStatus = panel?.status;
  const stageItems = [
    {
      id: "group",
      content: (
        <span className="font-medium text-[var(--ink)]">
          {stages.groupFiles}
        </span>
      ),
    },
    {
      id: "match",
      content: (
        <span className="font-medium text-[var(--ink)]">{stages.match}</span>
      ),
    },
    {
      id: "automatic",
      content: (
        <>
          <span className="font-medium text-[var(--ink)]">
            {stages.allConfidentAutomatic}
          </span>{" "}
          <span aria-hidden="true" className="text-[var(--accent)]">
            →
          </span>{" "}
          {stages.automaticDirection}
        </>
      ),
    },
    {
      id: "manual",
      content: (
        <>
          <span className="font-medium text-[var(--ink)]">
            {stages.otherwiseManual}
          </span>{" "}
          <span aria-hidden="true" className="text-[var(--accent)]">
            →
          </span>{" "}
          {stages.manualDirection}
        </>
      ),
    },
  ];
  const matchingContent = (
    <>
      {showProcessingStages ? (
        <ul
          className="mb-5 flex list-none flex-wrap gap-x-8 gap-y-3 p-0"
          aria-label={stages.stages}
        >
          {stageItems.map((item) => (
            <li
              key={item.id}
              className="flex min-w-[15rem] flex-1 items-start gap-2.5"
            >
              <span
                aria-hidden="true"
                className="mt-1.5 size-2 flex-none rounded-full bg-[var(--accent)]"
              />
              <span className="min-w-0 text-sm leading-5 text-[var(--muted)]">
                {item.content}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
      {notice ? (
        <p
          role="status"
          className="mb-5 flex items-start gap-2.5 rounded-lg border border-[var(--accent)] bg-[var(--accent-soft)] px-4 py-3 text-sm leading-5 text-[var(--ink)]"
        >
          <InfoIcon />
          <span className="min-w-0">{notice}</span>
        </p>
      ) : null}
      {loading ? (
        <p
          role="status"
          className="mb-5 flex items-center gap-2.5 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-4 py-3 text-sm text-[var(--muted)]"
        >
          <SpinnerIcon />
          <span className="min-w-0">{loading}</span>
        </p>
      ) : null}
      {error ? (
        <p
          role="alert"
          className="mb-5 rounded-lg border border-[var(--error)] bg-[var(--error-soft)] px-4 py-3 text-sm leading-5 text-[var(--ink)]"
        >
          {error}
        </p>
      ) : null}
      {panel ? (
        <IncomingGroupsPanel
          locale={locale}
          count={panel.rows.length}
          status={panelStatus}
        >
          <IncomingGroupsOverviewView
            labels={panel.labels}
            rows={panel.rows}
            onToggle={panel.onToggle}
            onMove={panel.onMove}
            onSplit={panel.onSplit}
            disclosureRef={panel.disclosureRef}
            disabled={panel.disabled}
            className={panel.className}
          />
        </IncomingGroupsPanel>
      ) : null}
    </>
  );
  return (
    <section
      hidden={hidden}
      aria-labelledby="incoming-title"
      className="min-w-0 w-full px-4 pt-6 pb-8 max-[600px]:px-3 max-[600px]:py-4"
    >
      <IncomingPageHeading
        locale={locale}
        action={activeSection === "matching" ? headingAction : undefined}
        publication={Boolean(publication)}
      />
      {publication && publicationCounts ? (
        <Tabs
          aria-label={sections.label}
          selectedKey={activeSection}
          onSelectionChange={(key) => {
            if (key !== "matching" && key !== "publication") return;
            if (onSectionChange) onSectionChange(key);
            else setSection(key);
          }}
          className="min-w-0"
        >
          <TabList aria-label={sections.label} className={topTabListClass}>
            <Tab
              id="matching"
              aria-label={`${sections.matching}: ${matchingCount}`}
              className={topTabClass}
            >
              <span>{sections.matching}</span>
              <span aria-hidden="true" className={topCountClass}>
                {matchingCount}
              </span>
            </Tab>
            <Tab
              id="publication"
              aria-label={`${sections.publication}: ${publicationCounts.total}`}
              className={topTabClass}
            >
              <span>{sections.publication}</span>
              <span aria-hidden="true" className={topCountClass}>
                {publicationCounts.total}
              </span>
            </Tab>
          </TabList>
          <TabPanel id="matching" className={topTabPanelClass}>
            {matchingContent}
          </TabPanel>
          <TabPanel id="publication" className={topTabPanelClass}>
            <IncomingPublicationView
              locale={locale}
              editions={publication.editions}
              initialStatus={publication.initialStatus}
            />
          </TabPanel>
        </Tabs>
      ) : (
        matchingContent
      )}
    </section>
  );
}
