import type { Locale } from "../../i18n";
import {
	Button,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
} from "../../ui";

const messages = {
	en: {
		label: "Local edition",
		placeholder: "Select an edition",
		empty: "The Local catalog has no editions with a tracklist.",
	},
	ru: {
		label: "Локальное издание",
		placeholder: "Выберите издание",
		empty: "В Local-каталоге пока нет изданий с треклистом.",
	},
} satisfies Record<Locale, Record<"label" | "placeholder" | "empty", string>>;

export function MatchingLocalReleasePicker({
	locale,
	releases,
	selectedReleaseId,
	onSelect,
}: {
	locale: Locale;
	releases: readonly { id: string; title: string; artist: string }[];
	selectedReleaseId?: string;
	onSelect: (id: string) => void;
}) {
	const copy = messages[locale];
	const selected = releases.find((release) => release.id === selectedReleaseId);
	if (releases.length === 0) {
		return (
			<div className="grid min-w-0 content-start gap-1 text-[11px]">
				<span className="text-[10px] uppercase leading-4 tracking-[0.03em] text-[var(--muted)]">
					{copy.label}
				</span>
				<p className="m-0 text-[var(--muted)]">{copy.empty}</p>
			</div>
		);
	}

	return (
		<Select
			className="grid min-w-0 content-start gap-1"
			selectedKey={selected?.id ?? null}
			onSelectionChange={(key) => {
				if (key !== null) onSelect(String(key));
			}}
		>
			<Label className="text-[10px] uppercase leading-4 tracking-[0.03em] text-[var(--muted)]">
				{copy.label}
			</Label>
			<Button className="w-full justify-between text-[11px]">
				<SelectValue className="min-w-0">
					{selected?.title ?? copy.placeholder}
				</SelectValue>
				<svg
					aria-hidden="true"
					viewBox="0 0 16 16"
					className="size-3 shrink-0"
					fill="none"
					stroke="currentColor"
					strokeWidth="1.5"
				>
					<path d="m4 6 4 4 4-4" />
				</svg>
			</Button>
			<Popover>
				<ListBox className="text-[11px]">
					{releases.map((release) => (
						<ListBoxItem
							key={release.id}
							id={release.id}
							textValue={release.title}
						>
							<span className="block truncate">{release.title}</span>
							{release.artist && (
								<span className="block truncate text-[10px] text-[var(--muted)]">
									{release.artist}
								</span>
							)}
						</ListBoxItem>
					))}
				</ListBox>
			</Popover>
		</Select>
	);
}
