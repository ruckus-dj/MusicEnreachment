import { useState } from "react";
import type { Locale } from "../../i18n/types";
import { AggregatePublicationPreviewView } from "../library/AggregatePublicationPreviewView";
import {
  type PublicationPreviewDetailsState,
  PublicationPreviewDetailsView,
} from "../library/PublicationPreviewDetailsView";
import {
  publicationFieldLabel as publicationInspectorFieldLabel,
  publicationGroupLabel as publicationInspectorGroupLabel,
  publicationMessage as publicationInspectorMessage,
} from "./publicationMessages";

export type MetadataChange = {
  key: string;
  label: string;
  before: string[];
  after: string[];
};

export type MetadataChangeGroup = {
  key: string;
  label: string;
  changes: MetadataChange[];
};

export type PublicationInspection = {
  id: string;
  position: string;
  title: string;
  context?: string;
  action?: string;
  reason?: string;
  source?: string;
  outputPath?: string;
  container?: string;
  groups: MetadataChangeGroup[];
  details?: PublicationPreviewDetailsState;
};

function shownValue(values: string[], locale: Locale) {
  return values.length
    ? values
    : [publicationInspectorMessage(locale, "noValue")];
}

function changeValue(
  change: MetadataChangeGroup["changes"][number],
  values: string[],
  locale: Locale,
) {
  if (change.key !== "cover") return shownValue(values, locale);
  if (!values.length) return [publicationInspectorMessage(locale, "noValue")];
  const artworkState: Record<
    string,
    "artworkPresent" | "artworkCurrent" | "artworkNew"
  > = {
    Есть: "artworkPresent",
    "Есть · текущая": "artworkCurrent",
    "Есть · новая": "artworkNew",
  };
  return values.length
    ? values.map((value) => {
        const key = artworkState[value];
        return key ? publicationInspectorMessage(locale, key) : value;
      })
    : [publicationInspectorMessage(locale, "noValue")];
}

function ValueList({ values }: { values: string[] }) {
  const occurrences = new Map<string, number>();
  return (
    <ol className="m-0 grid list-none gap-1 p-0">
      {values.map((value, index) => {
        const occurrence = (occurrences.get(value) ?? 0) + 1;
        occurrences.set(value, occurrence);
        return (
          <li
            key={JSON.stringify([value, occurrence])}
            className="grid grid-cols-[1rem_minmax(0,1fr)] gap-1"
          >
            <span className="text-[var(--muted)]">{index + 1}.</span>
            <div className="flex min-w-0 items-start">
              <span aria-hidden="true">“</span>
              <pre className="m-0 min-w-0 flex-1 whitespace-pre-wrap break-words font-sans">
                {value}
              </pre>
              <span aria-hidden="true">”</span>
            </div>
          </li>
        );
      })}
    </ol>
  );
}

export function PublicationMetadataChanges({
  groups,
  locale,
  compact,
}: {
  groups: MetadataChangeGroup[];
  locale: Locale;
  compact?: boolean;
}) {
  if (!groups.length)
    return (
      <p className="m-0 text-sm text-[var(--muted)]">
        {publicationInspectorMessage(locale, "noChanges")}
      </p>
    );

  return (
    <section
      className={
        compact
          ? "grid max-h-48 min-w-0 gap-2 overflow-y-auto text-xs"
          : "grid gap-3"
      }
      aria-label={publicationInspectorMessage(locale, "changedFields")}
    >
      {groups.map((group) => (
        <section
          key={group.key}
          aria-label={publicationInspectorGroupLabel(
            locale,
            group.key,
            group.label,
          )}
        >
          <h3 className="mb-1.5 mt-0 border-b border-[var(--line)] pb-1 text-xs font-semibold text-[var(--muted)]">
            {publicationInspectorGroupLabel(locale, group.key, group.label)}
          </h3>
          <table className="w-full table-fixed border-collapse text-left text-xs">
            <colgroup>
              <col className="w-[30%]" />
              <col className="w-[35%]" />
              <col className="w-[35%]" />
            </colgroup>
            <thead>
              <tr className="border-b border-[var(--line)] text-[var(--muted)]">
                <th scope="col" className="px-1 py-2 font-medium">
                  {publicationInspectorMessage(locale, "field")}
                </th>
                <th scope="col" className="px-1 py-2 font-medium">
                  {publicationInspectorMessage(locale, "current")}
                </th>
                <th scope="col" className="px-1 py-2 font-medium">
                  {publicationInspectorMessage(locale, "after")}
                </th>
              </tr>
            </thead>
            <tbody>
              {group.changes.map((change) => {
                const isLyrics = change.key === "lyrics";
                return (
                  <tr
                    key={change.key}
                    className="border-b border-[var(--line)] align-top last:border-0"
                  >
                    <th
                      scope="row"
                      className="break-words px-1 py-2 font-medium"
                      title={publicationInspectorFieldLabel(
                        locale,
                        change.key,
                        change.label,
                      )}
                    >
                      {publicationInspectorFieldLabel(
                        locale,
                        change.key,
                        change.label,
                      )}
                    </th>
                    {isLyrics ? (
                      <td colSpan={2} className="px-1 py-2">
                        <div className="grid grid-cols-2 gap-2">
                          <div className="min-w-0">
                            <span className="block text-[10px] text-[var(--muted)]">
                              {publicationInspectorMessage(locale, "current")}
                            </span>
                            <div className="max-h-36 overflow-auto rounded-sm bg-[var(--raised)] p-2 text-xs">
                              <ValueList
                                values={changeValue(
                                  change,
                                  change.before,
                                  locale,
                                )}
                              />
                            </div>
                          </div>
                          <div className="min-w-0">
                            <span className="block text-[10px] text-[var(--muted)]">
                              {publicationInspectorMessage(locale, "after")}
                            </span>
                            <div className="max-h-36 overflow-auto rounded-sm bg-[var(--raised)] p-2 text-xs">
                              <ValueList
                                values={changeValue(
                                  change,
                                  change.after,
                                  locale,
                                )}
                              />
                            </div>
                          </div>
                        </div>
                      </td>
                    ) : (
                      <>
                        <td className="break-words px-1 py-2">
                          <ValueList
                            values={changeValue(change, change.before, locale)}
                          />
                        </td>
                        <td className="break-words px-1 py-2">
                          <ValueList
                            values={changeValue(change, change.after, locale)}
                          />
                        </td>
                      </>
                    )}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      ))}
    </section>
  );
}

function InspectorBody({
  item,
  locale,
}: {
  item: PublicationInspection;
  locale: Locale;
}) {
  const willBeWritten = [item.outputPath, item.container]
    .filter((value): value is string => Boolean(value))
    .join(" · ");
  return (
    <>
      <div className="mb-3 border-b border-[var(--line)] pb-2">
        <p className="m-0 text-[11px] text-[var(--muted)]">
          {publicationInspectorMessage(locale, "changedFields")}:{" "}
          {item.groups.reduce((n, group) => n + group.changes.length, 0)}
        </p>
        {item.context && (
          <p
            className="mb-0 mt-1 truncate text-xs text-[var(--muted)]"
            title={item.context}
          >
            {item.context}
          </p>
        )}
        <p className="mb-1 mt-2 text-xs font-semibold">{item.action}</p>
        <p className="m-0 break-words text-xs text-[var(--muted)]">
          {item.reason ?? ""}
        </p>
        <dl className="mb-0 mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-1 text-[11px]">
          <dt className="text-[var(--muted)]">
            {publicationInspectorMessage(locale, "source")}
          </dt>
          <dd className="m-0 min-w-0 break-words">{item.source ?? ""}</dd>
          {willBeWritten && (
            <>
              <dt className="text-[var(--muted)]">
                {publicationInspectorMessage(locale, "willBeWritten")}
              </dt>
              <dd className="m-0 min-w-0 break-words">{willBeWritten}</dd>
            </>
          )}
        </dl>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto pr-1">
        {item.details ? (
          <PublicationPreviewDetailsView
            locale={locale}
            state={item.details}
            sourceGroupName={item.id}
          />
        ) : (
          <PublicationMetadataChanges groups={item.groups} locale={locale} />
        )}
      </div>
    </>
  );
}

export function PublicationInspector({
  items,
  locale,
}: {
  items: PublicationInspection[];
  locale: Locale;
}) {
  const [selectedID, setSelectedID] = useState(items[0]?.id ?? "");
  const selected = items.find((item) => item.id === selectedID) ?? items[0];
  if (!selected) return null;

  return (
    <AggregatePublicationPreviewView
      locale={locale}
      title={publicationInspectorMessage(locale, "inspect")}
      positions={items.map((item) => ({
        id: item.id,
        label: `${item.position}. ${item.title}`,
        status: {
          kind: "label",
          label: [item.context, item.action].filter(Boolean).join(" · "),
        },
        diagnostic: {
          kind: "text",
          text: `${publicationInspectorMessage(locale, "changedFields")}: ${item.groups.reduce((count, group) => count + group.changes.length, 0)}`,
        },
      }))}
      selectedPositionId={selected.id}
      onSelectPosition={setSelectedID}
      inspector={{
        kind: "summary",
        content: <InspectorBody locale={locale} item={selected} />,
      }}
    />
  );
}
