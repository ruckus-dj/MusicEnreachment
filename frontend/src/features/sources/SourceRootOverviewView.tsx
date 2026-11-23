import type { ReactNode } from "react";
import { AppButton } from "../../components/AppButton";

export type SourceRootOverviewTone = "available" | "unavailable" | "unknown";

type SourceRootOverviewFact = {
	key: string;
	label: string;
	value: ReactNode;
};

export type SourceRootOverviewViewProps = {
	locale: "en" | "ru";
	titleId?: string;
	name: string;
	path: string;
	mode: "in_place" | "staged";
	modeControl?: ReactNode;
	availability: SourceRootOverviewTone;
	availabilityNote?: string;
	lastScan: { label: string; dateTime?: string };
	inventory: {
		label: string;
		notes?: readonly string[];
		path?: string;
		stale?: boolean;
	};
	onRemove: () => void;
};

const labels = {
	en: {
		title: "Source",
		name: "Name",
		path: "Path",
		mode: "Processing mode",
		in_place: "In place",
		staged: "Using a work copy",
		availability: "Availability",
		available: "Available",
		unavailable: "Unavailable",
		unknown: "Not checked",
		lastScan: "Last successful scan",
		inventory: "Inventory",
		inventoryPath: "Inventory path",
		previousPath:
			"This is the previous path. The inventory will be replaced after a successful scan of the current path.",
		remove: "Remove source",
	},
	ru: {
		title: "Источник",
		name: "Имя",
		path: "Путь",
		mode: "Режим обработки",
		in_place: "На месте",
		staged: "Через рабочую копию",
		availability: "Доступность",
		available: "Доступен",
		unavailable: "Недоступен",
		unknown: "Не проверен",
		lastScan: "Последнее успешное сканирование",
		inventory: "Инвентарь",
		inventoryPath: "Путь инвентаря",
		previousPath:
			"Это прежний путь: инвентарь заменится после успешного сканирования текущего пути.",
		remove: "Удалить источник",
	},
} as const;

const panel =
	"min-w-0 mb-[10px] rounded-[8px] border border-[var(--line)] bg-[var(--surface)] p-[10px] [&>h2]:mb-2 [&>h2]:text-sm [&>h2]:font-semibold";
const summary =
	"m-0 grid grid-cols-[minmax(130px,.3fr)_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs max-[600px]:grid-cols-1";
const note = "mt-1 block text-[11px] text-[var(--muted)]";
const stateTones: Record<SourceRootOverviewTone, string> = {
	available: "bg-[var(--accent-soft)] text-[var(--accent)]",
	unavailable: "bg-[var(--raised)] text-[var(--error)]",
	unknown: "bg-[var(--raised)] text-[var(--muted)]",
};

export function SourceRootOverviewView({
	locale,
	titleId = "source-overview-title",
	name,
	path,
	mode,
	modeControl,
	availability,
	availabilityNote,
	lastScan,
	inventory,
	onRemove,
}: SourceRootOverviewViewProps) {
	const text = labels[locale];
	const facts: SourceRootOverviewFact[] = [
		{ key: "name", label: text.name, value: name },
		{
			key: "path",
			label: text.path,
			value: <code className="[overflow-wrap:anywhere]">{path}</code>,
		},
		{ key: "mode", label: text.mode, value: modeControl ?? text[mode] },
		{
			key: "availability",
			label: text.availability,
			value: (
				<>
					<span
						className={`inline-block rounded-sm px-1.5 py-0.5 font-medium ${stateTones[availability]}`}
					>
						{text[availability]}
					</span>
					{availabilityNote && <span className={note}>{availabilityNote}</span>}
				</>
			),
		},
		{
			key: "lastScan",
			label: text.lastScan,
			value: lastScan.dateTime ? (
				<time dateTime={lastScan.dateTime}>{lastScan.label}</time>
			) : (
				<span className="text-[var(--muted)]">{lastScan.label}</span>
			),
		},
		{
			key: "inventory",
			label: text.inventory,
			value: (
				<>
					{inventory.label}
					{inventory.notes?.map((item) => (
						<span key={item} className={note}>
							{item}
						</span>
					))}
				</>
			),
		},
	];
	if (inventory.path) {
		facts.push({
			key: "inventoryPath",
			label: text.inventoryPath,
			value: (
				<>
					<code>{inventory.path}</code>
					{inventory.stale && <span className={note}>{text.previousPath}</span>}
				</>
			),
		});
	}
	return (
		<section aria-labelledby={titleId} className={panel}>
			<h2 id={titleId}>{text.title}</h2>
			<dl className={summary}>
				{facts.map(({ key, label, value }) => (
					<div className="contents" key={key}>
						<dt className="m-0 text-[var(--muted)]">{label}</dt>
						<dd className="m-0 min-w-0 [overflow-wrap:anywhere]">{value}</dd>
					</div>
				))}
			</dl>
			<div className="mt-3 flex flex-wrap items-center gap-2">
				<AppButton aria-label={`${text.remove} ${name}`} onPress={onRemove}>
					{text.remove}
				</AppButton>
			</div>
		</section>
	);
}
