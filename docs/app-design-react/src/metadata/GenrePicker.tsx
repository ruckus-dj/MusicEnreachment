import { useMemo, useState } from "react";
import {
	Button,
	Dialog,
	Heading,
	Input,
	Link,
	ListBox,
	ListBoxItem,
	Modal,
	SearchField,
} from "../ui";
import { demoGenres } from "../GenreLibrary";

export type GenreReference = { id?: string; name: string };
type GenreOption = { id: string; name: string };

export function GenrePicker({
	value,
	onChange,
	readOnly = false,
	loaded,
	onLoad,
	displayValue,
	label = "Жанры",
	options = demoGenres,
}: {
	value: GenreReference[];
	onChange: (next: GenreReference[]) => void;
	readOnly?: boolean;
	loaded: boolean;
	onLoad?: () => void | Promise<void>;
	displayValue?: string;
	label?: string;
	options?: readonly GenreOption[];
}) {
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [loading, setLoading] = useState(false);
	const [loadError, setLoadError] = useState("");
	const catalog = useMemo(
		() => new Map(options.map((item) => [item.id, item])),
		[options],
	);
	const selected = useMemo(
		() => new Set(value.map((item) => item.id ?? item.name)),
		[value],
	);
	const filtered = options.filter((item) =>
		item.name.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()),
	);
	const visibleKeys = new Set(filtered.map((item) => item.id));

	const remove = (key: string) =>
		onChange(value.filter((item) => (item.id ?? item.name) !== key));
	const load = async () => {
		if (!onLoad || loading) return;
		setLoading(true);
		setLoadError("");
		try {
			await onLoad();
		} catch {
			setLoadError("Не удалось загрузить каталог жанров.");
		} finally {
			setLoading(false);
		}
	};

	return (
		<div className="grid min-w-0 content-start gap-1 text-xs text-[var(--muted)]">
			<span>{label}</span>
			<div className="min-w-0">
				<button
					type="button"
					aria-label={label}
					aria-haspopup={readOnly ? undefined : "dialog"}
					aria-expanded={readOnly ? undefined : open}
					onClick={() => !readOnly && setOpen(true)}
					className={`h-8 w-full truncate rounded-[var(--control-radius)] border px-2 text-left text-sm text-[var(--ink)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--focus)] ${readOnly ? "border-transparent bg-transparent" : "border-b-[var(--control)] border-x-transparent border-t-transparent bg-transparent"}`}
				>
					{readOnly
						? displayValue || value.map((item) => item.name).join(", ") || "—"
						: value.map((item) => item.name).join(", ") || "Выбрать жанры…"}
				</button>
			</div>
			{!readOnly && (
				<Modal
					isOpen={open}
					onOpenChange={(nextOpen) => {
						setOpen(nextOpen);
						if (!nextOpen) setQuery("");
					}}
					isDismissable
					className="max-h-[min(80vh,560px)] overflow-y-auto"
				>
					<div
						onKeyDownCapture={(event) => {
							if (event.key === "Escape") {
								event.stopPropagation();
								setOpen(false);
								setQuery("");
							}
						}}
					>
						<Dialog aria-label={`${label} — выбор`}>
							<Heading slot="title">{label}</Heading>
							{value.length > 0 && (
								<div
									aria-label="Выбранные жанры"
									className="mb-2 flex flex-wrap gap-1"
								>
									{value.map((item) => {
										const key = item.id ?? item.name;
										return (
											<span
												key={key}
												className="inline-flex items-center gap-1 rounded-full border border-[var(--line)] px-2 py-1 text-sm"
											>
												{item.name}
												<Button
													aria-label={`Убрать ${item.name}`}
													onPress={() => remove(key)}
													className="min-h-6 px-1 py-0"
												>
													×
												</Button>
											</span>
										);
									})}
								</div>
							)}
							{!loaded ? (
								<div className="grid gap-2 text-sm">
									<p className="m-0">
										Каталог жанров не загружен. Сохранённый выбор останется без
										изменений.
									</p>
									{onLoad ? (
										<Button onPress={load} isDisabled={loading}>
											{loading
												? "Загрузка…"
												: loadError
													? "Повторить загрузку"
													: "Загрузить каталог"}
										</Button>
									) : (
										<Link href="#settings">Перейти к настройкам каталога</Link>
									)}
									{loadError && (
										<p role="alert" className="m-0 text-[var(--error)]">
											{loadError}
										</p>
									)}
								</div>
							) : (
								<>
									<SearchField
										aria-label="Поиск жанров"
										value={query}
										onChange={setQuery}
										autoFocus
									>
										<Input placeholder="Поиск жанров…" />
									</SearchField>
									{options.length === 0 ? (
										<p role="status" className="text-sm">
											Каталог жанров пуст.
										</p>
									) : filtered.length === 0 ? (
										<p role="status" className="text-sm">
											Совпадений не найдено.
										</p>
									) : (
										<ListBox
											aria-label="Доступные жанры"
											selectionMode="multiple"
											escapeKeyBehavior="none"
											selectedKeys={
												new Set(
													Array.from(selected).filter((key) =>
														visibleKeys.has(key),
													),
												)
											}
											onSelectionChange={(keys) => {
												const nextKeys =
													keys === "all"
														? filtered.map((item) => item.id)
														: Array.from(keys, String);
												const retained = value.filter((item) => {
													const key = item.id ?? item.name;
													return !catalog.has(key) || !visibleKeys.has(key);
												});
												const next = filtered
													.filter((item) => nextKeys.includes(item.id))
													.map((item) => ({ id: item.id, name: item.name }));
												onChange([...retained, ...next]);
											}}
											className="mt-2 max-h-56 overflow-y-auto"
										>
											{filtered.map((item) => (
												<ListBoxItem
													key={item.id}
													id={item.id}
													textValue={item.name}
												>
													{item.name}
												</ListBoxItem>
											))}
										</ListBox>
									)}
								</>
							)}
							<div className="mt-3 flex justify-end">
								<Button onPress={() => setOpen(false)}>Готово</Button>
							</div>
						</Dialog>
					</div>
				</Modal>
			)}
		</div>
	);
}
