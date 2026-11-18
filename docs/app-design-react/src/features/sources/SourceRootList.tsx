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
const panel =
	"min-w-0 rounded-md border border-[var(--line)] bg-[var(--surface)]";
const link =
	"inline-block max-w-full rounded-sm font-semibold text-[var(--accent)] wrap-anywhere hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]";
const sub = "block text-[var(--muted)] wrap-anywhere";
const stateTones = {
	available: "bg-[var(--accent-soft)] text-[var(--accent)]",
	unavailable: "bg-[var(--raised)] text-[var(--error)]",
	unknown: "bg-[var(--raised)] text-[var(--muted)]",
};

function directory(row: SourceRootListRow) {
	return (
		<>
			<Link href={row.href} className={link}>
				{row.name}
			</Link>
			<code className={`${sub} mt-1 text-[11px]`}>{row.path}</code>
		</>
	);
}

function facts(row: SourceRootListRow, copy: Copy) {
	return [
		{ key: "mode", label: copy.mode, value: copy.modes[row.mode] },
		{
			key: "availability",
			label: copy.availability,
			value: (
				<span
					className={`inline-block rounded-sm px-1.5 py-0.5 font-medium ${stateTones[row.availability]}`}
				>
					{copy.status[row.availability]}
				</span>
			),
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
						<span className={`${sub} mt-1 text-[11px]`} key={note}>
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
			className="min-h-8 max-w-full rounded border border-[var(--control)] px-2 py-1 text-[var(--ink)] wrap-anywhere data-[hovered]:bg-[var(--raised)] data-[disabled]:opacity-60 data-[focus-visible]:outline-2 data-[focus-visible]:outline-offset-2 data-[focus-visible]:outline-[var(--focus)] pointer-coarse:min-h-10"
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
		{ id: "mode", label: copy.mode, width: "w-[16%]" },
		{ id: "availability", label: copy.availability, width: "w-[14%]" },
		{ id: "lastScan", label: copy.lastScan, width: "w-[18%]" },
		{ id: "inventory", label: copy.inventory, width: "w-[14%]" },
		{ id: "actions", label: copy.actions, width: "w-[114px]" },
	];
	return (
		<section className="min-w-0 space-y-2">
			<h2 className="text-sm font-semibold">{copy.title}</h2>
			{state?.kind === "loading" ? (
				<p className={`${panel} p-3 text-xs text-[var(--muted)]`} role="status">
					{copy.loading}
				</p>
			) : state?.kind === "error" ? (
				<div
					role="alert"
					className={`${panel} flex flex-wrap items-center gap-2 p-3 text-xs wrap-anywhere`}
				>
					<p className="text-[var(--error)]">
						{state.message || copy.loadError}
					</p>
					<Button
						onPress={state.onRetry}
						className="min-h-8 rounded border border-[var(--control)] px-2 text-[var(--ink)] data-[hovered]:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]"
					>
						{copy.retry}
					</Button>
				</div>
			) : rows.length === 0 ? (
				<div className={`${panel} space-y-1 p-3 text-xs`}>
					<p className="font-semibold">{copy.empty}</p>
					<p className="text-[var(--muted)]">{copy.emptyHint}</p>
				</div>
			) : (
				<>
					<div className={`${panel} max-[800px]:hidden`}>
						<Table
							aria-label={copy.title}
							className="w-full table-fixed border-separate border-spacing-0 text-xs wrap-anywhere [font-variant-numeric:tabular-nums]"
						>
							<TableHeader>
								{columns.map(({ id, label, width }) => (
									<Column
										id={id}
										key={id}
										isRowHeader={id === "directory"}
										className={`${width} border-b border-[var(--line)] bg-[var(--raised)] px-3 py-2 text-left font-semibold text-[var(--muted)]`}
									>
										{label}
									</Column>
								))}
							</TableHeader>
							<TableBody items={rows} dependencies={[copy, onScan]}>
								{(row) => (
									<Row
										id={row.id}
										className="[&:not(:last-child)>td]:border-b [&>td]:border-[var(--line)]"
									>
										<Cell className="px-3 py-2.5 align-top">
											{directory(row)}
										</Cell>
										{facts(row, copy).map(({ key, value }) => (
											<Cell key={key} className="px-3 py-2.5 align-top">
												{value}
											</Cell>
										))}
										<Cell className="px-3 py-2.5 align-top">
											{scanAction(row, copy, onScan)}
										</Cell>
									</Row>
								)}
							</TableBody>
						</Table>
					</div>
					<ul
						className="hidden min-w-0 list-none gap-2 p-0 text-xs max-[800px]:grid"
						aria-label={copy.title}
					>
						{rows.map((row) => (
							<li key={row.id} className={`${panel} p-3`}>
								{directory(row)}
								<dl className="mt-2 grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-3 gap-y-1 wrap-anywhere">
									{facts(row, copy).map(({ key, label, value }) => (
										<div className="contents" key={key}>
											<dt className="text-[var(--muted)]">{label}</dt>
											<dd className="min-w-0">{value}</dd>
										</div>
									))}
								</dl>
								<div className="mt-2">{scanAction(row, copy, onScan)}</div>
							</li>
						))}
					</ul>
				</>
			)}
		</section>
	);
}
