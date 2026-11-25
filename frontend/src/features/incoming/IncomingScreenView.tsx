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
  return (
    <section
      hidden={hidden}
      aria-labelledby="incoming-title"
      className="min-w-0 w-full px-4 pt-[14px] pb-[18px] max-[600px]:p-3"
    >
      <IncomingPageHeading locale={locale} action={headingAction} />
      {showProcessingStages ? (
        <ol
          className="mb-[10px] grid list-none grid-cols-4 gap-0 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--raised)] p-0 max-[900px]:grid-cols-2 max-[600px]:grid-cols-1"
          aria-label={stages.stages}
        >
          <li className="flex min-w-0 items-center gap-2 px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10">
            <b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
              01
            </b>
            <span className="min-w-0">{stages.groupFiles}</span>
          </li>
          <li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t max-[600px]:border-[var(--line)]">
            <b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
              02
            </b>
            <span className="min-w-0">{stages.match}</span>
          </li>
          <li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-l-0 max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
            <b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
              03
            </b>
            <span className="min-w-0">
              {stages.allConfidentAutomatic}{" "}
              <i className="text-[var(--accent)] not-italic" aria-hidden="true">
                →
              </i>{" "}
              {stages.automaticDirection}
            </span>
          </li>
          <li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
            <b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
              04
            </b>
            <span className="min-w-0">
              {stages.otherwiseManual}{" "}
              <i className="text-[var(--accent)] not-italic" aria-hidden="true">
                →
              </i>{" "}
              {stages.manualDirection}
            </span>
          </li>
        </ol>
      ) : null}
      {notice ? (
        <p
          role="status"
          className="mb-[10px] border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-[10px] py-[7px] text-xs text-[var(--ink)]"
        >
          {notice}
        </p>
      ) : null}
      {loading ? <p role="status">{loading}</p> : null}
      {error ? <p role="alert">{error}</p> : null}
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
