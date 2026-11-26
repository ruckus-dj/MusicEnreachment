import { type RefObject, useEffect, useRef } from "react";
import {
  Button,
  type Key,
  Tag,
  TagGroup,
  TagList,
} from "react-aria-components";
import { AppIcon } from "./AppIcon";

/** One active library condition, rendered as a removable tag. */
export type ActiveFilterTag = {
  id: string;
  label: string;
};

export type ActiveFilterTagsProps = {
  locale: "en" | "ru";
  items: readonly ActiveFilterTag[];
  onRemove: (id: string) => void;
  onReset: () => void;
  /** Host search input that receives focus after the last condition is gone. */
  focusTargetRef?: RefObject<HTMLInputElement | null>;
};

const copy = {
  en: {
    label: "Active filters",
    reset: "Reset all",
  },
  ru: {
    label: "Активные фильтры",
    reset: "Сбросить все",
  },
} as const;

const tag =
  "inline-flex h-7 max-w-full min-w-0 items-center gap-1 rounded-full border border-[var(--line)] bg-[var(--raised)] pl-2.5 pr-1 text-xs text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

const removeButton =
  "grid size-5 shrink-0 cursor-pointer place-items-center rounded-full text-[var(--muted)] hover:bg-[var(--surface)] hover:text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

const resetButton =
  "inline-flex min-h-7 cursor-pointer items-center rounded-md px-1.5 text-sm font-medium text-[var(--accent-strong)] underline-offset-2 hover:underline data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

function focusHost(focusTargetRef?: RefObject<HTMLInputElement | null>) {
  (
    focusTargetRef?.current ?? document.querySelector<HTMLElement>("main h1")
  )?.focus();
}

type PendingRemoval = {
  removed: Set<string>;
  previousIds: string[];
};

/**
 * Shared removable filter summary. Hosts own the canonical route/state; this
 * component only reports the removed id or the reset action.
 */
export function ActiveFilterTags({
  locale,
  items,
  onRemove,
  onReset,
  focusTargetRef,
}: ActiveFilterTagsProps) {
  const text = copy[locale];
  const groupRef = useRef<HTMLDivElement>(null);
  const resetRef = useRef<HTMLButtonElement>(null);
  const pendingRemoval = useRef<PendingRemoval | null>(null);
  const pendingReset = useRef(false);

  function remove(keys: Set<Key>) {
    const removed = new Set([...keys].map(String));
    pendingRemoval.current = {
      removed,
      previousIds: items.map((item) => item.id),
    };
    for (const id of removed) onRemove(id);
  }

  // RAC removes the tab stop with the tag. Move focus to the next surviving
  // condition after the removed key, then backwards, then the host input. A
  // reset can clear conditions over one or two renders, so keep its pending
  // flag until the list is empty. Unrelated route updates leave focus alone.
  useEffect(() => {
    if (pendingReset.current) {
      if (items.length === 0) {
        pendingReset.current = false;
        focusHost(focusTargetRef);
      }
      return;
    }
    const pending = pendingRemoval.current;
    if (!pending) return;
    pendingRemoval.current = null;
    if (items.length === 0) {
      focusHost(focusTargetRef);
      return;
    }

    const survivingIds = items.map((item) => item.id);
    const surviving = new Set(survivingIds);
    const firstRemoved = pending.previousIds.findIndex((id) =>
      pending.removed.has(id),
    );
    const ordered =
      firstRemoved >= 0
        ? [
            ...pending.previousIds.slice(firstRemoved + 1),
            ...pending.previousIds.slice(0, firstRemoved).reverse(),
          ]
        : survivingIds;
    const nextId = ordered.find((id) => surviving.has(id));
    const rows =
      groupRef.current?.querySelectorAll<HTMLElement>("[data-filter-id]");
    const next =
      nextId && rows
        ? [...rows].find((row) => row.dataset.filterId === nextId)
        : undefined;
    if (next) {
      next.focus();
      return;
    }
    (
      focusTargetRef?.current ??
      resetRef.current ??
      document.querySelector<HTMLElement>("main h1")
    )?.focus();
  }, [items, focusTargetRef]);

  if (items.length === 0) return null;

  return (
    <div className="mb-3 flex min-w-0 flex-wrap items-center gap-2">
      <TagGroup
        ref={groupRef}
        aria-label={text.label}
        onRemove={remove}
        className="min-w-0"
      >
        <TagList
          items={items}
          className="flex min-w-0 flex-wrap items-center gap-1.5"
        >
          {(item) => (
            <Tag
              id={item.id}
              textValue={item.label}
              data-filter-id={item.id}
              className={tag}
            >
              <span className="min-w-0 truncate">{item.label}</span>
              <Button slot="remove" className={removeButton}>
                <AppIcon name="close" className="size-3" />
              </Button>
            </Tag>
          )}
        </TagList>
      </TagGroup>
      <Button
        ref={resetRef}
        className={resetButton}
        onPress={() => {
          pendingReset.current = true;
          onReset();
        }}
      >
        {text.reset}
      </Button>
    </div>
  );
}
