import type { Locale } from "../../i18n/types";
import {
  IncomingGroupsOverviewView,
  type IncomingGroupsOverviewViewProps,
} from "./IncomingGroupsOverviewView";
import {
  IncomingGroupsPanel,
  IncomingPageHeading,
} from "./IncomingGroupsPanel";

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
 * the overview table supplied through `panel`.
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
}: IncomingScreenViewProps) {
  const stages = stagesCopy[locale];
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
  return (
    <section
      hidden={hidden}
      aria-labelledby="incoming-title"
      className="min-w-0 w-full px-4 pt-6 pb-8 max-[600px]:px-3 max-[600px]:py-4"
    >
      <IncomingPageHeading locale={locale} action={headingAction} />
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
    </section>
  );
}
