import {
  type ReactNode,
  type RefObject,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { type Key, Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import type { Locale } from "../../i18n/locale";
import type { LibraryTagValues } from "./LibraryTagComparisonTable";

export type LibraryPositionSourceTagsSource = {
  id: string;
  label: string;
  summary?: string;
  tags?: LibraryTagValues;
};

const copy = {
  en: {
    title: "Source tags",
    modes: "Tag views",
    fileTab: "File tags",
    compareTab: "Compare with publication",
    field: "Field",
    value: "Value",
    close: "Close",
    noSource: "The source is no longer available.",
    noTags: "The saved source snapshot has no tags.",
  },
  ru: {
    title: "Теги источника",
    modes: "Режимы тегов",
    fileTab: "Теги файла",
    compareTab: "Сравнить с публикацией",
    field: "Поле",
    value: "Значение",
    close: "Закрыть",
    noSource: "Источник больше недоступен.",
    noTags: "В сохранённом снимке источника нет тегов.",
  },
} as const;

const panelClass =
  "fixed inset-y-0 right-0 left-auto m-0 h-dvh max-h-none w-full max-w-[560px] flex-col overflow-hidden border-0 border-l border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--ink)] shadow-[0_16px_48px_rgb(0_0_0/0.2)] backdrop:bg-[rgb(0_0_0/0.45)] open:flex max-[599px]:max-w-none max-[599px]:border-0";
const tabListClass =
  "inline-flex w-fit min-w-0 flex-wrap items-center gap-1 rounded-lg bg-[var(--raised)] p-1";
const tabClass =
  "flex h-8 cursor-pointer items-center gap-1.5 rounded-md px-3 text-sm font-medium text-[var(--muted)] transition-colors data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2 pointer-coarse:min-h-10 max-[599px]:min-h-9";
const tabPanelClass =
  "min-w-0 focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const tableWrapClass =
  "min-w-0 overflow-auto rounded-lg border border-[var(--line)] bg-[var(--surface)]";
const headingClass =
  "sticky top-0 z-[1] border-b border-[var(--line)] bg-[var(--raised)] px-3 py-2.5 text-left align-top text-xs font-medium text-[var(--muted)]";
const rowHeadingClass =
  "border-b border-[var(--line)] px-3 py-2.5 text-left align-top text-sm font-medium";
const cellClass =
  "border-b border-[var(--line)] px-3 py-2.5 text-left align-top text-sm wrap-anywhere";

function sourceTagEntries(tags: LibraryTagValues | undefined) {
  if (!tags) return [];
  return Object.entries(tags).sort(([left], [right]) =>
    left.localeCompare(right),
  );
}

/**
 * Right-hand source tag inspector. It stays inside the position tree instead of
 * a portal, so leaving the tab or route unmounts it with the screen. The host
 * owns the open state and supplies the comparison actor for the exact source;
 * this view only keeps the file snapshot and the access mechanics.
 */
export function LibraryPositionSourceTagsDrawer({
  locale,
  source,
  currentReported,
  comparison,
  onClose,
  returnFocusRef,
}: {
  locale: Locale;
  source?: LibraryPositionSourceTagsSource;
  /** The comparison mode exists only for a reported CURRENT snapshot. */
  currentReported: boolean;
  comparison?: ReactNode;
  onClose: () => void;
  returnFocusRef?: RefObject<HTMLElement | null>;
}) {
  const text = copy[locale];
  const titleId = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const [mode, setMode] = useState<"file" | "compare">("file");
  const canCompare = currentReported && comparison !== undefined;

  useEffect(() => {
    if (!canCompare && mode !== "file") setMode("file");
  }, [canCompare, mode]);

  useEffect(() => {
    const node = dialog.current;
    if (!node) return;
    const previous = document.activeElement;
    node.showModal();
    heading.current?.focus();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previousOverflow;
      if (node.open) node.close();
      const target = returnFocusRef
        ? returnFocusRef.current
        : previous instanceof HTMLElement
          ? previous
          : null;
      if (target?.isConnected && !target.closest("[hidden],[inert]"))
        target.focus();
    };
  }, [returnFocusRef]);

  const entries = sourceTagEntries(source?.tags);

  return (
    <dialog
      ref={dialog}
      aria-modal="true"
      aria-labelledby={titleId}
      className={panelClass}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onKeyDown={(event) => {
        if (event.key !== "Escape") return;
        event.preventDefault();
        event.stopPropagation();
        onClose();
      }}
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        const bounds = event.currentTarget.getBoundingClientRect();
        if (event.clientX < bounds.left || event.clientX > bounds.right)
          onClose();
      }}
    >
      <header className="flex items-start justify-between gap-3 border-b border-[var(--line)] p-4">
        <div className="min-w-0">
          <h2
            ref={heading}
            id={titleId}
            tabIndex={-1}
            className="m-0 text-lg font-semibold outline-none"
          >
            {text.title}
          </h2>
          {source && (
            <p className="m-0 mt-1 text-sm text-[var(--muted)] wrap-anywhere">
              <strong className="text-[var(--ink)]">{source.label}</strong>
              {source.summary ? ` · ${source.summary}` : ""}
            </p>
          )}
        </div>
        <AppButton
          type="button"
          variant="icon"
          aria-label={text.close}
          onPress={onClose}
        >
          <AppIcon name="close" />
        </AppButton>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        <Tabs
          selectedKey={mode}
          onSelectionChange={(key: Key) =>
            setMode(key === "compare" && canCompare ? "compare" : "file")
          }
          aria-label={text.modes}
          className="grid min-w-0 gap-3"
        >
          <TabList aria-label={text.modes} className={tabListClass}>
            <Tab id="file" className={tabClass}>
              {text.fileTab}
            </Tab>
            {canCompare && (
              <Tab id="compare" className={tabClass}>
                {text.compareTab}
              </Tab>
            )}
          </TabList>
          <TabPanel id="file" className={tabPanelClass}>
            {!source ? (
              <p className="m-0 text-sm">{text.noSource}</p>
            ) : entries.length ? (
              <div className={tableWrapClass}>
                <table className="w-full border-collapse text-left text-sm">
                  <thead>
                    <tr>
                      <th scope="col" className={headingClass}>
                        {text.field}
                      </th>
                      <th scope="col" className={headingClass}>
                        {text.value}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {entries.map(([key, values]) => (
                      <tr key={key}>
                        <th scope="row" className={rowHeadingClass}>
                          {key}
                        </th>
                        <td className={cellClass}>
                          {values?.some((value) => value !== "") ? (
                            <span className="whitespace-pre-wrap">
                              {values.join("\n")}
                            </span>
                          ) : (
                            "—"
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="m-0 text-sm">{text.noTags}</p>
            )}
          </TabPanel>
          {canCompare && (
            <TabPanel id="compare" className={tabPanelClass}>
              {comparison}
            </TabPanel>
          )}
        </Tabs>
      </div>
    </dialog>
  );
}
