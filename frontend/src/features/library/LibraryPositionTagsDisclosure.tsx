import type { ReactNode } from "react";

const copy = {
  en: {
    summary: "Compare source and publication tags",
  },
  ru: {
    summary: "Сравнить теги источника и публикации",
  },
} as const;

export function LibraryPositionTagsDisclosure({
  locale,
  open,
  onOpenChange,
  children,
}: {
  locale: "en" | "ru";
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  children: ReactNode;
}) {
  return (
    <details
      className="grid min-w-0 grid-cols-1 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
      open={open}
      onToggle={(event) => onOpenChange?.(event.currentTarget.open)}
    >
      <summary className="cursor-pointer font-semibold">
        {copy[locale].summary}
      </summary>
      {children}
    </details>
  );
}
