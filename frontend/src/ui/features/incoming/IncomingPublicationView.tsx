/** biome-ignore-all lint/a11y/noRedundantRoles: Keep table semantics in WebKit when narrow layouts change display to block. */
import { Fragment, useId, useState } from "react";
import { Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { StatusBadge } from "../../components/StatusBadge";
import type { Locale } from "../../i18n/types";
import { incomingPublicationMessages } from "./incomingPublicationMessages";
import {
  type IncomingPublicationEdition,
  type IncomingPublicationModel,
  incomingPublicationCounts,
  incomingPublicationNeedsAttention,
} from "./incomingPublicationModel";

const tabListClass =
  "inline-flex w-fit min-w-0 flex-wrap items-center gap-1 rounded-lg bg-[var(--raised)] p-1";
const tabClass =
  "flex h-8 cursor-pointer items-center gap-1.5 rounded-md px-3 text-sm font-medium text-[var(--muted)] transition-colors data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";
const countClass =
  "inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-[var(--surface)] px-1.5 text-xs leading-none font-medium text-[var(--muted)] tabular-nums ring-1 ring-[var(--line)]";
const tabPanelClass =
  "min-w-0 focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const headCell =
  "border-b border-[var(--line)] bg-[var(--raised)] px-4! py-2.5! text-left align-middle! text-xs font-semibold text-[var(--muted)]";
const cell =
  "border-b border-[var(--line)] px-4! py-3! align-middle! wrap-anywhere";
const innerHeadCell =
  "border-b border-[var(--line)] bg-[var(--raised)] px-3! py-2! text-left align-middle! text-xs font-semibold text-[var(--muted)]";
const innerCell =
  "border-b border-[var(--line)] px-3! py-2.5! align-top! wrap-anywhere";

export type IncomingPublicationViewProps = IncomingPublicationModel & {
  locale: Locale;
};

function PublicationTable({
  locale,
  editions,
  kind,
}: {
  locale: Locale;
  editions: readonly IncomingPublicationEdition[];
  kind: "attention" | "ready";
}) {
  const copy = incomingPublicationMessages[locale];
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const empty = kind === "attention" ? copy.emptyAttention : copy.emptyReady;
  const caption =
    kind === "attention" ? copy.attentionCaption : copy.readyCaption;
  const toggle = (id: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  if (editions.length === 0) {
    return (
      <p className="m-0 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-4 py-8 text-center text-sm text-[var(--muted)]">
        {empty}
      </p>
    );
  }
  return (
    <div className="min-w-0 overflow-x-auto overscroll-x-contain">
      <table
        role="table"
        className="w-full table-fixed border-collapse text-left text-sm! max-[1023px]:block"
      >
        <caption className="sr-only">{caption}</caption>
        <colgroup className="max-[1023px]:hidden">
          <col className="w-[36%]" />
          <col className="w-[13%]" />
          <col className="w-[31%]" />
          <col className="w-[20%]" />
        </colgroup>
        <thead className="max-[1023px]:hidden">
          <tr>
            <th scope="col" className={headCell}>
              {copy.edition}
            </th>
            <th scope="col" className={headCell}>
              {copy.tracks}
            </th>
            <th scope="col" className={headCell}>
              {copy.reason}
            </th>
            <th scope="col" className={headCell}>
              {copy.nextStep}
            </th>
          </tr>
        </thead>
        <tbody role="rowgroup" className="max-[1023px]:block">
          {editions.map((edition) => {
            const opened = expanded.has(edition.id);
            const detailsId = `incoming-publication-tracks-${edition.id}`;
            const metadataId = `${detailsId}-metadata`;
            const needsAttention = incomingPublicationNeedsAttention(edition);
            return (
              <Fragment key={edition.id}>
                <tr
                  role="row"
                  className={`max-[1023px]:block max-[1023px]:border-b max-[1023px]:border-[var(--line)] max-[1023px]:pb-2 ${
                    opened
                      ? "bg-[var(--accent-soft)]"
                      : "hover:bg-[var(--raised)]"
                  }`}
                >
                  <td
                    role="cell"
                    className="border-b border-[var(--line)] p-0! align-middle! max-[1023px]:block max-[1023px]:border-0"
                  >
                    <button
                      type="button"
                      aria-expanded={opened}
                      aria-controls={opened ? detailsId : undefined}
                      aria-label={copy.expand(edition.title)}
                      aria-describedby={metadataId}
                      className="flex min-h-14 w-full min-w-0 cursor-pointer items-center gap-3 rounded-none border-0! bg-transparent! px-4 py-3 text-left text-[var(--ink)] outline-none hover:bg-transparent! focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--focus)]"
                      onClick={() => toggle(edition.id)}
                    >
                      <span className="grid size-7 flex-none place-items-center rounded-md border border-[var(--line)] bg-[var(--surface)] text-[var(--muted)]">
                        <svg
                          viewBox="0 0 16 16"
                          className={`size-4 transition-transform motion-safe:duration-150 ${opened ? "rotate-90" : ""}`}
                          fill="none"
                          stroke="currentColor"
                          strokeWidth="1.5"
                          aria-hidden="true"
                          focusable="false"
                        >
                          <path d="m6 4 4 4-4 4" />
                        </svg>
                      </span>
                      <span className="min-w-0">
                        <span className="block text-sm font-semibold wrap-anywhere">
                          {edition.title}
                        </span>
                        <span id={metadataId}>
                          <span className="mt-0.5 block text-[13px] leading-5 text-[var(--muted)] wrap-anywhere">
                            {edition.artist || copy.missingArtist}
                          </span>
                          <span className="mt-0.5 block text-xs leading-4 text-[var(--muted)] tabular-nums">
                            {edition.year}
                            <span className="min-[1024px]:hidden">
                              {edition.year ? " · " : ""}
                              {copy.trackCount(edition.tracks.length)}
                            </span>
                          </span>
                        </span>
                      </span>
                    </button>
                  </td>
                  <td className={`${cell} tabular-nums max-[1023px]:hidden`}>
                    {edition.tracks.length}
                  </td>
                  <td
                    role="cell"
                    className={`${cell} max-[1023px]:block max-[1023px]:border-0 max-[1023px]:py-0!`}
                  >
                    <span
                      className={
                        needsAttention
                          ? "text-[13px] font-medium leading-5 text-[var(--warning)] wrap-anywhere"
                          : "text-[13px] leading-5 text-[var(--muted)] wrap-anywhere"
                      }
                    >
                      {edition.reasonLabel ?? copy.reasonLabel(edition.reason)}
                    </span>
                  </td>
                  <td
                    role="cell"
                    className={`${cell} max-[1023px]:block max-[1023px]:border-0 max-[1023px]:py-0!`}
                  >
                    <a
                      className="inline-flex min-h-9 items-center gap-1.5 font-medium text-[var(--accent-strong)] underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2"
                      href={edition.nextStep.href}
                    >
                      {copy.nextStepLabel(edition.nextStep.kind)}
                      <svg
                        viewBox="0 0 12 12"
                        className="size-3 flex-none"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="1.5"
                        aria-hidden="true"
                        focusable="false"
                      >
                        <path
                          d="M2.5 6h7M6.5 3 9.5 6l-3 3"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        />
                      </svg>
                    </a>
                  </td>
                </tr>
                {opened && (
                  <tr
                    id={detailsId}
                    role="row"
                    className="bg-[var(--raised)] max-[1023px]:block"
                  >
                    <td
                      role="cell"
                      className="p-0! max-[1023px]:block"
                      colSpan={4}
                    >
                      <div className="px-4 py-3">
                        <div className="overflow-x-auto overscroll-x-contain rounded-md border border-[var(--line)] bg-[var(--surface)]">
                          <table
                            className="w-full min-w-[32rem] table-fixed border-collapse text-left text-sm!"
                            aria-label={copy.expand(edition.title)}
                          >
                            <colgroup>
                              <col className="w-[8%]" />
                              <col className="w-[40%]" />
                              <col className="w-[28%]" />
                              <col className="w-[24%]" />
                            </colgroup>
                            <thead>
                              <tr>
                                <th scope="col" className={innerHeadCell}>
                                  {copy.number}
                                </th>
                                <th scope="col" className={innerHeadCell}>
                                  {copy.title}
                                </th>
                                <th scope="col" className={innerHeadCell}>
                                  {copy.source}
                                </th>
                                <th scope="col" className={innerHeadCell}>
                                  {copy.state}
                                </th>
                              </tr>
                            </thead>
                            <tbody>
                              {edition.tracks.map((track) => (
                                <tr key={track.id}>
                                  <td
                                    className={`${innerCell} tabular-nums text-[var(--muted)]`}
                                  >
                                    {track.number}
                                  </td>
                                  <td className={innerCell}>
                                    {track.href ? (
                                      <a
                                        className="font-medium text-[var(--ink)] underline-offset-2 hover:text-[var(--accent-strong)] hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2"
                                        href={track.href}
                                      >
                                        {track.title}
                                      </a>
                                    ) : (
                                      <span className="font-medium text-[var(--ink)]">
                                        {track.title}
                                      </span>
                                    )}
                                  </td>
                                  <td
                                    className={`${innerCell} text-[13px] leading-5 text-[var(--muted)]`}
                                  >
                                    {track.source || "—"}
                                  </td>
                                  <td className={innerCell}>
                                    <div className="flex flex-wrap items-center gap-2">
                                      <StatusBadge
                                        tone={
                                          track.attention
                                            ? "warning"
                                            : "neutral"
                                        }
                                      >
                                        {copy.trackAttentionLabel(
                                          track.attention,
                                          edition.reason,
                                        )}
                                      </StatusBadge>
                                      {track.attention ? (
                                        <a
                                          className="text-xs font-medium text-[var(--accent-strong)] underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2"
                                          href={
                                            track.nextStep?.href ??
                                            edition.nextStep.href
                                          }
                                        >
                                          {copy.nextStepLabel(
                                            track.nextStep?.kind ??
                                              edition.nextStep.kind,
                                          )}
                                        </a>
                                      ) : null}
                                    </div>
                                  </td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      </div>
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/**
 * Read-only publication queue shown beside the incoming groups.
 *
 * Attention and ready editions stay inside their own nested tab, so changing
 * status never turns a missing artist or filename into a publication action.
 */
export function IncomingPublicationView({
  locale,
  editions,
  initialStatus,
  description,
  badge,
  status,
}: IncomingPublicationViewProps) {
  const copy = incomingPublicationMessages[locale];
  const headingId = useId();
  const counts = incomingPublicationCounts(editions);
  const attention = editions.filter(incomingPublicationNeedsAttention);
  const ready = editions.filter(
    (edition) => !incomingPublicationNeedsAttention(edition),
  );
  return (
    <section
      aria-labelledby={headingId}
      className="min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[0_1px_2px_#0000000a]"
    >
      <header className="flex min-w-0 flex-wrap items-start justify-between gap-x-4 gap-y-2 border-b border-[var(--line)] px-4 py-3">
        <div className="min-w-0">
          <h2
            id={headingId}
            className="m-0 text-sm font-semibold text-[var(--ink)]"
          >
            {copy.sectionTitle}
          </h2>
          <p className="mt-1 mb-0 max-w-[72ch] text-[13px] leading-5 text-[var(--muted)]">
            {description ?? copy.sectionDescription}
          </p>
        </div>
        {badge ? <StatusBadge tone="metadata">{badge}</StatusBadge> : null}
      </header>
      {status?.kind === "loading" ? (
        <p role="status" className="m-0 p-4 text-sm text-[var(--muted)]">
          {copy.loading}
        </p>
      ) : status?.kind === "error" ? (
        <div className="flex flex-wrap items-center gap-3 p-4">
          <p role="alert" className="m-0 text-sm text-[var(--error)]">
            {copy.loadFailed}
          </p>
          <AppButton size="sm" onPress={status.retry}>
            {copy.retry}
          </AppButton>
        </div>
      ) : (
        <Tabs
          key={initialStatus}
          defaultSelectedKey={initialStatus ?? "attention"}
          aria-label={copy.sectionTabList}
          className="grid min-w-0 gap-3 p-4"
        >
          <TabList aria-label={copy.sectionTabList} className={tabListClass}>
            <Tab
              id="attention"
              aria-label={copy.attentionTab(counts.attention)}
              className={tabClass}
            >
              <span>{copy.attention}</span>
              <span aria-hidden="true" className={countClass}>
                {counts.attention}
              </span>
            </Tab>
            <Tab
              id="ready"
              aria-label={copy.readyTab(counts.ready)}
              className={tabClass}
            >
              <span>{copy.ready}</span>
              <span aria-hidden="true" className={countClass}>
                {counts.ready}
              </span>
            </Tab>
          </TabList>
          <TabPanel id="attention" className={tabPanelClass}>
            <PublicationTable
              locale={locale}
              editions={attention}
              kind="attention"
            />
          </TabPanel>
          <TabPanel id="ready" className={tabPanelClass}>
            <PublicationTable locale={locale} editions={ready} kind="ready" />
          </TabPanel>
        </Tabs>
      )}
    </section>
  );
}
