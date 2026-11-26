import {
	Button,
	Cell,
	Column,
	Link,
	Row,
	Table,
	TableBody,
	TableHeader,
} from "react-aria-components";
import { StatusBadge } from "../../components/StatusBadge";
import type { UiBadgeTone } from "../../components/uiStyles";
import { ui } from "../../components/uiStyles";

export interface SourceRootListRow {
	id: string;
	name: string;
	path: string;
	mode: "in_place" | "staged";
	availability: "available" | "unavailable" | "unknown";
	lastSuccessfulScan?: { label: string; dateTime?: string };
	inventory: { label: string; notes?: string[] };
	scanning?: boolean;
	href: string;
}

type LoadState =
	| { kind: "loading" }
	| { kind: "error"; message: string; onRetry: () => void };

const messages = {
	en: {
		title: "Connected directories",
		directory: "Directory",
		mode: "Processing mode",
		availability: "Availability",
		lastScan: "Last successful scan",
		inventory: "Inventory",
		actions: "Actions",
		scan: "Scan",
		scanning: "Scanning…",
		modes: { in_place: "In place", staged: "Via working copy" },
		status: {
			available: "Available",
			unavailable: "Unavailable",
			unknown: "Not checked",
		},
		neverScanned: "Not scanned yet",
		empty: "No directories registered.",
		emptyHint: "Add a server directory to scan its files.",
		loading: "Loading directories…",
		loadError: "Could not load directories.",
		retry: "Retry",
	},
	ru: {
		title: "Подключённые каталоги",
		directory: "Каталог и путь",
		mode: "Режим обработки",
		availability: "Доступность",
		lastScan: "Последнее успешное сканирование",
		inventory: "Инвентарь",
		actions: "Действия",
		scan: "Сканировать",
		scanning: "Сканируется…",
		modes: { in_place: "На месте", staged: "Через рабочую копию" },
		status: {
			available: "Доступен",
			unavailable: "Недоступен",
			unknown: "Не проверен",
		},
		neverScanned: "Ещё не сканировался",
		empty: "Ни один каталог не зарегистрирован.",
		emptyHint: "Добавьте каталог на сервере, чтобы затем просканировать его.",
		loading: "Загружаем каталоги…",
		loadError: "Не удалось загрузить каталоги.",
		retry: "Повторить",
	},
} as const;

type Copy = (typeof messages)[keyof typeof messages];
const link =
	"inline-block max-w-full rounded-sm text-sm font-medium text-[var(--accent-strong)] wrap-anywhere underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";
const sub = "block text-xs text-[var(--muted)] wrap-anywhere";
const stateTones: Record<SourceRootListRow["availability"], UiBadgeTone> = {
	available: "success",
	unavailable: "error",
	unknown: "neutral",
};

function directory(row: SourceRootListRow) {
	return (
		<>
			<Link href={row.href} className={link}>
				{row.name}
			</Link>
			<code className={`${sub} mt-1 font-mono`}>{row.path}</code>
		</>
	);
}

function availability(row: SourceRootListRow, copy: Copy) {
	return (
		<StatusBadge tone={stateTones[row.availability]}>
			{copy.status[row.availability]}
		</StatusBadge>
	);
}

function facts(row: SourceRootListRow, copy: Copy) {
	return [
		{ key: "mode", label: copy.mode, value: copy.modes[row.mode] },
		{
			key: "availability",
			label: copy.availability,
			value: availability(row, copy),
		},
		{
			key: "lastScan",
			label: copy.lastScan,
			value: row.lastSuccessfulScan ? (
				<time dateTime={row.lastSuccessfulScan.dateTime}>
					{row.lastSuccessfulScan.label}
				</time>
			) : (
				<span className={sub}>{copy.neverScanned}</span>
			),
		},
		{
			key: "inventory",
			label: copy.inventory,
			value: (
				<>
					{row.inventory.label}
					{row.inventory.notes?.map((note) => (
						<span className={`${sub} mt-1`} key={note}>
							{note}
						</span>
					))}
				</>
			),
		},
	];
}

function scanAction(
	row: SourceRootListRow,
	copy: Copy,
	onScan: (id: string) => void,
) {
	const label = row.scanning ? copy.scanning : copy.scan;
	return (
		<Button
			aria-label={`${label}: ${row.name}`}
			isDisabled={row.scanning || row.availability === "unavailable"}
			onPress={() => onScan(row.id)}
			className="inline-flex min-h-9 max-w-full cursor-pointer items-center justify-center whitespace-nowrap rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-sm font-medium text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2 pointer-coarse:min-h-10"
		>
			{label}
		</Button>
	);
}

export function SourceRootList({
	rows,
	locale,
	state,
	onScan,
}: {
	rows: readonly SourceRootListRow[];
	locale: "en" | "ru";
	state?: LoadState;
	onScan: (id: string) => void;
}) {
	const copy = messages[locale];
	const columns = [
		{ id: "directory", label: copy.directory, width: "" },
		{ id: "mode", label: copy.mode, width: "w-[14%]" },
		{ id: "availability", label: copy.availability, width: "w-[12%]" },
		{ id: "lastScan", label: copy.lastScan, width: "w-[18%]" },
		{ id: "inventory", label: copy.inventory, width: "w-[13%]" },
		{ id: "actions", label: copy.actions, width: "w-28" },
	];
	return (
		<section className="min-w-0 space-y-3">
			<h2 className="m-0 text-base font-semibold">{copy.title}</h2>
			{state?.kind === "loading" ? (
				<p
					className={`${ui.tableFrame} p-5 text-sm text-[var(--muted)]`}
					role="status"
				>
					{copy.loading}
				</p>
			) : state?.kind === "error" ? (
				<div
					role="alert"
					className={`${ui.tableFrame} flex flex-wrap items-center gap-3 p-5 text-sm wrap-anywhere`}
				>
					<p className={ui.fieldError}>{state.message || copy.loadError}</p>
					<Button
						onPress={state.onRetry}
						className="min-h-9 rounded-lg border border-[var(--line)] px-3 text-sm font-medium text-[var(--ink)] hover:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)]"
					>
						{copy.retry}
					</Button>
				</div>
			) : rows.length === 0 ? (
				<div className={`${ui.tableFrame} space-y-1 p-6 text-sm`}>
					<p className="m-0 font-medium">{copy.empty}</p>
					<p className="m-0 text-[var(--muted)]">{copy.emptyHint}</p>
				</div>
			) : (
				<>
					<div className={`${ui.tableFrame} max-[800px]:hidden`}>
						<Table aria-label={copy.title} className={ui.racTable}>
							<TableHeader>
								{columns.map(({ id, label, width }) => (
									<Column
										id={id}
										key={id}
										isRowHeader={id === "directory"}
										className={`react-aria-Column ${width}`}
									>
										{label}
									</Column>
								))}
							</TableHeader>
							<TableBody items={rows} dependencies={[copy, onScan]}>
								{(row) => (
									<Row
										id={row.id}
										className="react-aria-Row [&>.react-aria-Cell]:py-3.5"
									>
										<Cell>{directory(row)}</Cell>
										{facts(row, copy).map(({ key, value }) => (
											<Cell key={key}>{value}</Cell>
										))}
										<Cell>{scanAction(row, copy, onScan)}</Cell>
									</Row>
								)}
							</TableBody>
						</Table>
					</div>
					<ul
						className="hidden min-w-0 list-none gap-3 p-0 text-sm max-[800px]:grid"
						aria-label={copy.title}
					>
						{rows.map((row) => (
							<li key={row.id} className={`${ui.tableFrame} p-4`}>
								{directory(row)}
								<dl className="mt-3 grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-4 gap-y-2 wrap-anywhere">
									{facts(row, copy).map(({ key, label, value }) => (
										<div className="contents" key={key}>
											<dt className="text-xs text-[var(--muted)]">{label}</dt>
											<dd className="m-0 min-w-0">{value}</dd>
										</div>
									))}
								</dl>
								<div className="mt-3">{scanAction(row, copy, onScan)}</div>
							</li>
						))}
					</ul>
				</>
			)}
		</section>
	);
}
