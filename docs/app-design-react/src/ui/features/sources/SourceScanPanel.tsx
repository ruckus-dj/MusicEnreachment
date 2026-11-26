import { Button } from "react-aria-components";

export type SourceScanState =
	| {
			kind:
				| "idle"
				| "checking"
				| "queued"
				| "running"
				| "traversing"
				| "applying"
				| "succeeded";
	  }
	| { kind: "failed" | "error"; message: string };

const messages = {
	en: {
		title: "Directory scan",
		start: "Scan directory",
		retry: "Retry scan",
		recheck: "Check again",
		manual:
			"The scan reads files without changing them. Inventory updates after a successful scan.",
		checking: "Checking scan status…",
		queued: "Scan queued.",
		running: "Scan in progress…",
		traversing: "Reading the directory…",
		applying: "Updating inventory…",
		succeeded: "Scan completed.",
	},
	ru: {
		title: "Сканирование каталога",
		start: "Сканировать каталог",
		retry: "Повторить сканирование",
		recheck: "Проверить ещё раз",
		manual:
			"Сканирование читает файлы, не изменяя их. Инвентарь обновляется после успешного обхода.",
		checking: "Проверяем состояние сканирования…",
		queued: "Сканирование поставлено в очередь.",
		running: "Сканирование выполняется…",
		traversing: "Читаем каталог…",
		applying: "Обновляем инвентарь…",
		succeeded: "Сканирование завершено.",
	},
};

const action =
	"inline-flex min-h-9 cursor-pointer items-center rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 py-1.5 text-sm font-medium text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] data-[disabled]:cursor-default data-[disabled]:opacity-50 data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";

export function SourceScanPanel({
	locale,
	state,
	onStart,
	onRetry,
	onRecheck,
	startDisabled,
	disabledNote,
	streamNotice,
}: {
	locale: "en" | "ru";
	state: SourceScanState;
	onStart: () => void;
	onRetry: () => void;
	onRecheck?: () => void;
	startDisabled?: boolean;
	disabledNote?: string;
	streamNotice?: string;
}) {
	const copy = messages[locale];
	const active = [
		"checking",
		"queued",
		"running",
		"traversing",
		"applying",
	].includes(state.kind);
	return (
		<div className="grid min-w-0 gap-1">
			<section className="grid min-w-0 gap-x-4 gap-y-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 text-sm wrap-anywhere sm:grid-cols-[minmax(0,1fr)_auto]">
				<div className="min-w-0 space-y-1">
					<h2 className="m-0 text-base font-semibold">{copy.title}</h2>
					<p className="text-[var(--muted)]">{copy.manual}</p>
					{"message" in state ? (
						<p className="text-[var(--error)]" role="alert">
							{state.message}
						</p>
					) : (
						state.kind !== "idle" && (
							<p className="font-medium" role="status">
								{copy[state.kind]}
							</p>
						)
					)}
				</div>
				<div className="flex items-start">
					{state.kind === "failed" ? (
						<Button
							className={action}
							isDisabled={startDisabled}
							onPress={onRetry}
						>
							{copy.retry}
						</Button>
					) : state.kind === "error" && onRecheck ? (
						<Button className={action} onPress={onRecheck}>
							{copy.recheck}
						</Button>
					) : (
						<Button
							className={action}
							isDisabled={startDisabled || active}
							onPress={onStart}
						>
							{copy.start}
						</Button>
					)}
				</div>
			</section>
			{disabledNote && (
				<p className="m-0 text-xs text-[var(--muted)]">{disabledNote}</p>
			)}
			{streamNotice && <p role="status">{streamNotice}</p>}
		</div>
	);
}
