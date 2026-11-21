import { Checkbox } from "react-aria-components";

type Locale = "en" | "ru";

const copy = {
  en: {
    releases: ["release", "releases", "releases"],
    positions: ["position", "positions", "positions"],
    withAudio: "with audio",
    published: "published",
    missing: "releases without an edition",
    performer: "Performer",
    summary: "Includes known cached groups; the discography may be incomplete.",
    select: (name: string) => `Select all positions by ${name}`,
  },
  ru: {
    releases: ["релиз", "релиза", "релизов"],
    positions: ["позиция", "позиции", "позиций"],
    withAudio: "с аудио",
    published: "опубликовано",
    missing: "альбомов без издания",
    performer: "Исполнитель",
    summary:
      "Включая известные группы из кэша; дискография может быть неполной.",
    select: (name: string) => `Выбрать все позиции артиста ${name}`,
  },
} as const;

export function LibraryArtistHeadingView({
  locale,
  name,
  disambiguation,
  genres,
  palette = "clay",
  count,
  includesCachedGroups,
  selection,
}: {
  locale: Locale;
  name: string;
  disambiguation?: string | null;
  genres: readonly string[];
  palette?: string;
  count: {
    releases: number;
    positions: number;
    published: number;
    available?: number;
    missing?: number;
  };
  includesCachedGroups?: boolean;
  selection?: {
    total: number;
    selected: number;
    onChange?: (value: boolean) => void;
  };
}) {
  const text = copy[locale];
  const formatter = new Intl.NumberFormat(locale);
  const pluralRules = new Intl.PluralRules(locale);
  const quantity = (
    value: number,
    forms: readonly [string, string, string],
  ) => {
    const plural = pluralRules.select(value);
    return `${formatter.format(value)} ${forms[plural === "one" ? 0 : plural === "few" ? 1 : 2]}`;
  };
  const checked = Boolean(
    selection?.total && selection.selected === selection.total,
  );
  const mixed = Boolean(selection?.selected && !checked);

  return (
    <header className="grid min-w-0 grid-cols-[76px_minmax(0,1fr)] gap-x-[13px] gap-y-2.5 pb-4 sm:grid-cols-[clamp(96px,14vw,168px)_minmax(0,1fr)] sm:gap-x-[clamp(16px,2.5vw,30px)]">
      <div
        data-palette={palette}
        className="relative isolate grid aspect-square min-w-0 place-items-center overflow-hidden rounded-xl border border-[color-mix(in_srgb,var(--artist-color)_48%,var(--line))] bg-[linear-gradient(145deg,color-mix(in_srgb,var(--artist-color)_80%,#111),color-mix(in_srgb,var(--artist-color)_42%,#101820))] text-white [--artist-color:#ac7059] data-[palette=forest]:[--artist-color:#486d59] data-[palette=dusk]:[--artist-color:#676e95] data-[palette=sand]:[--artist-color:#aa905f] data-[palette=slate]:[--artist-color:#4c717e] sm:rounded-[18px]"
      >
        <span
          aria-hidden="true"
          className="font-[Georgia,serif] text-[38px] font-medium [text-shadow:0_4px_22px_#0007] sm:text-[clamp(42px,6vw,76px)]"
        >
          {name.trim().slice(0, 1).toLocaleUpperCase(locale)}
        </span>
        {selection && (
          <Checkbox
            aria-label={text.select(name)}
            className="group absolute top-2 left-2 grid size-6 cursor-pointer place-items-center"
            isSelected={checked}
            isIndeterminate={mixed}
            isDisabled={!selection.total || !selection.onChange}
            onChange={selection.onChange}
          >
            <span
              aria-hidden="true"
              className="grid size-4 place-items-center rounded border border-white bg-black/45 text-[11px] leading-none text-white group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-offset-2 group-data-[disabled]:opacity-45"
            >
              {checked ? "✓" : mixed ? "−" : ""}
            </span>
          </Checkbox>
        )}
      </div>
      <div className="grid min-w-0 content-start gap-1">
        <h1
          id="artist-title"
          className="m-0 text-xl leading-tight font-semibold wrap-anywhere"
        >
          {name}
        </h1>
        {disambiguation && (
          <p className="m-0 text-sm text-[var(--muted)] wrap-anywhere">
            {disambiguation}
          </p>
        )}
        <p className="m-0 text-sm font-semibold text-[var(--ink)] wrap-anywhere">
          {genres.length ? genres.join(", ") : text.performer}
        </p>
        <p className="m-0 text-sm text-[var(--muted)] max-sm:col-span-full">
          {quantity(count.releases, text.releases)} ·{" "}
          {quantity(count.positions, text.positions)}
          {count.available !== undefined &&
            ` · ${text.withAudio}: ${formatter.format(count.available)}`}
          {` · ${formatter.format(count.published)} ${text.published}`}
          {count.missing !== undefined &&
            ` · ${formatter.format(count.missing)} ${text.missing}`}
        </p>
        {includesCachedGroups && (
          <p className="m-0 text-sm text-[var(--muted)]">{text.summary}</p>
        )}
      </div>
    </header>
  );
}
