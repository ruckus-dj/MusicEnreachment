import {
  Label,
  ListBox,
  ListBoxItem,
  Popover,
  Select,
  SelectValue,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";

const copy = {
  en: { version: "Version", placeholder: "Select a version" },
  ru: { version: "Версия", placeholder: "Выберите версию" },
} as const;

interface SetupToolRelease {
  identity: string;
  source?: string;
}

function caption(release: SetupToolRelease) {
  return release.source
    ? `${release.identity} (${release.source})`
    : release.identity;
}

export function SetupToolReleasePicker({
  locale,
  kind,
  releases,
  selected,
  pending = false,
  onChange,
}: {
  locale: "en" | "ru";
  kind: "ffmpeg" | "fpcalc";
  releases: readonly SetupToolRelease[];
  selected: string | null;
  pending?: boolean;
  onChange: (identity: string | null) => void;
}) {
  const text = copy[locale];
  return (
    <Select
      value={selected}
      placeholder={text.placeholder}
      isDisabled={pending || releases.length === 0}
      onChange={(key) => onChange(key === null ? null : String(key))}
      className="grid min-w-0 justify-items-start gap-1"
    >
      <Label className="font-semibold">
        {text.version} · {kind}
      </Label>
      <AppButton className="w-max max-w-full min-w-[min(148px,100%)] justify-between!">
        <SelectValue className="min-w-0 text-left wrap-anywhere" />
        <svg
          aria-hidden="true"
          className="shrink-0"
          width="12"
          height="12"
          viewBox="0 0 12 12"
        >
          <path
            d="m2 4 4 4 4-4"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
          />
        </svg>
      </AppButton>
      <Popover
        maxHeight={240}
        className="w-[var(--trigger-width)] overflow-auto overscroll-contain rounded border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_5px_16px_#0002]"
      >
        <ListBox items={releases} className="outline-none">
          {(release) => (
            <ListBoxItem
              id={release.identity}
              textValue={caption(release)}
              className="min-h-8 cursor-pointer rounded px-2 py-1.5 wrap-anywhere data-[focused]:bg-[var(--accent-soft)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:-outline-offset-2"
            >
              {caption(release)}
            </ListBoxItem>
          )}
        </ListBox>
      </Popover>
    </Select>
  );
}
