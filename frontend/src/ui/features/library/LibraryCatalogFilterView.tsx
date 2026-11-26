import { Label, Radio, RadioGroup } from "react-aria-components";
import type {
  LibraryAvailabilityFilter,
  LibraryPublicationFilter,
} from "./libraryDetailRoute";

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

const axisLabels = {
  en: {
    publication: {
      all: "All",
      published: "Published",
      unpublished: "Unpublished",
    },
    availability: {
      all: "All",
      available: "Has audio",
      missing: "Missing audio",
    },
  },
  ru: {
    publication: {
      all: "Все",
      published: "Опубликовано",
      unpublished: "Не опубликовано",
    },
    availability: {
      all: "Все",
      available: "В наличии",
      missing: "Не хватает",
    },
  },
} as const;

/** Localized label for one visible state filter. */
export function libraryFilterLabel(
  locale: "en" | "ru",
  filter: LibraryStateFilter,
) {
  return labels[locale][filter];
}

/** Localized label for an independent publication axis value. */
export function libraryPublicationFilterLabel(
  locale: "en" | "ru",
  value: LibraryPublicationFilter,
) {
  return axisLabels[locale].publication[value];
}

/** Localized label for an independent availability axis value. */
export function libraryAvailabilityFilterLabel(
  locale: "en" | "ru",
  value: LibraryAvailabilityFilter,
) {
  return axisLabels[locale].availability[value];
}

const segment =
  "flex h-8 min-w-0 cursor-pointer items-center justify-center rounded-md px-3 text-center text-sm text-[var(--muted)] wrap-anywhere transition-colors data-[selected]:bg-[var(--surface)] data-[selected]:font-medium data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[hovered]:not-data-[selected]:text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

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
      <Label className="mb-1.5 block text-xs font-medium text-[var(--muted)]">
        {copy.title}
      </Label>
      <div className="flex w-fit min-w-0 flex-wrap rounded-lg bg-[var(--raised)] p-1">
        {libraryFilterOptions.map((option) => (
          <Radio key={option} value={option} className={segment}>
            {copy[option]}
          </Radio>
        ))}
      </div>
    </RadioGroup>
  );
}
