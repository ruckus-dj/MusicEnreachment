import { Label, Radio, RadioGroup } from "react-aria-components";

export const libraryFilterOptions = [
  "all",
  "missing",
  "available",
  "published",
] as const;
export type LibraryStateFilter = (typeof libraryFilterOptions)[number];

const labels = {
  en: {
    title: "Status",
    all: "All",
    missing: "Missing audio",
    available: "Has audio",
    published: "Published",
  },
  ru: {
    title: "Состояние",
    all: "Все",
    missing: "Не хватает",
    available: "В наличии",
    published: "Опубликовано",
  },
} as const;

const segment =
  "flex min-w-[82px] flex-[1_1_82px] cursor-pointer items-center justify-center px-[7px] py-[5px] text-center text-[11px] leading-[1.2] text-[var(--muted)] wrap-anywhere [&+*]:border-l [&+*]:border-[var(--line)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:font-bold data-[selected]:text-[var(--ink)] data-[selected]:shadow-[inset_0_-2px_var(--accent)] data-[hovered]:not-data-[selected]:bg-[var(--raised)] data-[focus-visible]:relative data-[focus-visible]:z-[1] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:-outline-offset-[3px]";

export function LibraryCatalogFilterView({
  locale,
  value,
  onChange,
}: {
  locale: "en" | "ru";
  value: LibraryStateFilter;
  onChange: (value: LibraryStateFilter) => void;
}) {
  const copy = labels[locale];
  return (
    <RadioGroup
      orientation="horizontal"
      value={value}
      onChange={(key) => {
        const next = libraryFilterOptions.find((option) => option === key);
        if (next) onChange(next);
      }}
      className="min-w-0"
    >
      <Label className="mb-1 block text-[11px] font-semibold text-[var(--muted)]">
        {copy.title}
      </Label>
      <div className="flex min-h-[34px] flex-wrap rounded-md border border-[var(--control)] bg-[var(--surface)]">
        {libraryFilterOptions.map((option) => (
          <Radio key={option} value={option} className={segment}>
            {copy[option]}
          </Radio>
        ))}
      </div>
    </RadioGroup>
  );
}
