import { Checkbox } from "react-aria-components";
import { AppIcon } from "../../components/AppIcon";

type Locale = "en" | "ru";

const copy = {
  en: {
    releases: ["release", "releases", "releases"],
    positions: ["position", "positions", "positions"],
    withAudio: "with audio",
    published: "published",
    missing: "releases without an edition",
    local: "in the library",
    complete: "Complete MusicBrainz discography saved locally.",
    performer: "Performer",
    select: (name: string) => `Select all positions by ${name}`,
  },
  ru: {
    releases: ["релиз", "релиза", "релизов"],
    positions: ["позиция", "позиции", "позиций"],
    withAudio: "с аудио",
    published: "опубликовано",
    missing: "альбомов без издания",
    local: "в медиатеке",
    complete: "Полная дискография MusicBrainz сохранена локально.",
    performer: "Исполнитель",
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
  discographyComplete,
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
    local?: number;
  };
  discographyComplete?: boolean;
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
    <header className="mb-5 grid min-w-0 grid-cols-[72px_minmax(0,1fr)] gap-4 border-b border-[var(--line)] pb-5 sm:grid-cols-[96px_minmax(0,1fr)]">
      <div
        data-palette={palette}
        className="relative isolate grid aspect-square min-w-0 place-items-center overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--raised)] text-[var(--muted)] data-[palette=forest]:bg-[color-mix(in_srgb,#486d59_16%,var(--raised))] data-[palette=dusk]:bg-[color-mix(in_srgb,#676e95_16%,var(--raised))] data-[palette=sand]:bg-[color-mix(in_srgb,#aa905f_18%,var(--raised))] data-[palette=slate]:bg-[color-mix(in_srgb,#4c717e_16%,var(--raised))]"
      >
        <span
          aria-hidden="true"
          className="text-2xl font-semibold uppercase sm:text-3xl"
        >
          {name.trim().slice(0, 1).toLocaleUpperCase(locale)}
        </span>
        {selection && (
          <Checkbox
            aria-label={text.select(name)}
            className="group absolute top-1.5 left-1.5 grid size-7 cursor-pointer place-items-center rounded-md bg-[var(--surface)]/90 data-[disabled]:cursor-default data-[disabled]:opacity-45 data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2"
            isSelected={checked}
            isIndeterminate={mixed}
            isDisabled={!selection.total || !selection.onChange}
            onChange={selection.onChange}
          >
            <span
              aria-hidden="true"
              className="grid size-4 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)]"
            >
              {checked ? (
                <AppIcon name="check" className="size-3" />
              ) : mixed ? (
                <span className="h-0.5 w-2 rounded bg-[var(--surface)]" />
              ) : null}
            </span>
          </Checkbox>
        )}
      </div>
      <div className="grid min-w-0 content-start gap-1.5">
        <h1
          id="artist-title"
          className="m-0 text-[26px]/[1.2] font-semibold tracking-[-.02em] wrap-anywhere"
        >
          {name}
        </h1>
        {disambiguation && (
          <p className="m-0 text-sm text-[var(--muted)] wrap-anywhere">
            {disambiguation}
          </p>
        )}
        <p className="m-0 text-sm font-medium text-[var(--ink)] wrap-anywhere">
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
          {count.local !== undefined &&
            ` · ${formatter.format(count.local)} ${text.local}`}
        </p>
        {discographyComplete && (
          <p className="m-0 text-xs text-[var(--muted)]">{text.complete}</p>
        )}
      </div>
    </header>
  );
}
