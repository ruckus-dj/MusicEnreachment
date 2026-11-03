import { useMemo, useState } from "react";
import { useI18n } from "../i18n";
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
import { genreCatalog, resolveCatalogGenreName } from "../genres/catalogue";
import { metadataMessage } from "./messages";

export type GenreReference = { id?: string; name: string };
type GenreOption = { id: string; name: string };

export function GenrePicker({
	value,
	onChange,
	readOnly = false,
	loaded,
	onLoad,
	displayValue,
	label,
	options = genreCatalog,
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
	const { locale } = useI18n();
	const m = (key: Parameters<typeof metadataMessage>[1]) =>
		metadataMessage(locale, key);
	const pickerLabel = label ?? m("genre.label");
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [visibleCount, setVisibleCount] = useState(60);
	const [loading, setLoading] = useState(false);
	const [loadError, setLoadError] = useState("");
	const catalog = useMemo(
		() => new Map(options.map((item) => [item.id, item])),
		[options],
	);
	const catalogId = (item: GenreReference): string | undefined =>
		item.id ??
		options.find(
			(option) =>
				resolveCatalogGenreName(option) === resolveCatalogGenreName(item),
		)?.id;
	const selected = useMemo(() => {
		const keys = value.flatMap((item) => {
			return [item.id ?? item.name, catalogId(item)].filter(
				(key): key is string => Boolean(key),
			);
		});
		return new Set(keys);
	}, [options, value]);
	const search = query.trim().toLocaleLowerCase("en");
	const filtered = options.filter((item) =>
		item.name.toLocaleLowerCase("en").includes(search),
	);
	// Exact and prefix matches must not disappear behind a page of subgenres.
	if (search) {
		const rank = (name: string) => {
			const normalized = name.toLocaleLowerCase("en");
			return normalized === search ? 0 : normalized.startsWith(search) ? 1 : 2;
		};
		filtered.sort((left, right) => rank(left.name) - rank(right.name));
	}
	const shownOptions = filtered.slice(0, visibleCount);
	const visibleKeys = new Set(shownOptions.map((item) => item.id));

	const remove = (key: string) =>
		onChange(
			value
				.filter((item) => (item.id ?? item.name) !== key)
				.map((item) => ({ ...item, name: resolveCatalogGenreName(item) })),
		);
	const load = async () => {
		if (!onLoad || loading) return;
		setLoading(true);
		setLoadError("");
		try {
			await onLoad();
		} catch {
			setLoadError("genre.loadError");
		} finally {
			setLoading(false);
		}
	};

	return (
		<div className="grid min-w-0 content-start gap-1 text-xs text-[var(--muted)]">
			<span>{pickerLabel}</span>
			<div className="min-w-0">
				<button
					type="button"
					aria-label={pickerLabel}
					aria-haspopup={readOnly ? undefined : "dialog"}
					aria-expanded={readOnly ? undefined : open}
					onClick={() => !readOnly && setOpen(true)}
					className={`h-8 w-full truncate rounded-[var(--control-radius)] border px-2 text-left text-sm text-[var(--ink)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--focus)] ${readOnly ? "border-transparent bg-transparent" : "border-b-[var(--control)] border-x-transparent border-t-transparent bg-transparent"}`}
				>
					{readOnly
						? value.length
							? value.map((item) => resolveCatalogGenreName(item)).join(", ")
							: displayValue || "—"
						: value.map((item) => resolveCatalogGenreName(item)).join(", ") ||
							m("genre.choose")}
				</button>
			</div>
			{!readOnly && (
				<Modal
					isOpen={open}
					onOpenChange={(nextOpen) => {
						setOpen(nextOpen);
						if (!nextOpen) {
							setQuery("");
							setVisibleCount(60);
						}
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
						<Dialog aria-label={`${pickerLabel} — ${m("genre.chooseDialog")}`}>
							<Heading slot="title">{pickerLabel}</Heading>
							{value.length > 0 && (
								<div
									aria-label={m("genre.selected")}
									className="mb-2 flex flex-wrap gap-1"
								>
									{value.map((item) => {
										const key = item.id ?? item.name;
										return (
											<span
												key={key}
												className="inline-flex items-center gap-1 rounded-full border border-[var(--line)] px-2 py-1 text-sm"
											>
												{resolveCatalogGenreName(item)}
												<Button
													aria-label={`${m("action.remove")} ${resolveCatalogGenreName(item)}`}
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
									<p className="m-0">{m("genre.notLoaded")}</p>
									{onLoad ? (
										<Button onPress={load} isDisabled={loading}>
											{loading
												? m("state.loading")
												: loadError
													? m("action.retry")
													: m("action.loadCatalog")}
										</Button>
									) : (
										<Link href="#settings">{m("action.settings")}</Link>
									)}
									{loadError && (
										<p role="alert" className="m-0 text-[var(--error)]">
											{m("genre.loadError")}
										</p>
									)}
								</div>
							) : (
								<>
									<SearchField
										aria-label={m("genre.search")}
										value={query}
										onChange={(nextQuery) => {
											setQuery(nextQuery);
											setVisibleCount(60);
										}}
										autoFocus
									>
										<Input placeholder={m("genre.searchPlaceholder")} />
									</SearchField>
									{options.length === 0 ? (
										<p role="status" className="text-sm">
											{m("genre.empty")}
										</p>
									) : filtered.length === 0 ? (
										<p role="status" className="text-sm">
											{m("genre.noMatches")}
										</p>
									) : (
										<ListBox
											aria-label={m("genre.available")}
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
														? shownOptions.map((item) => item.id)
														: Array.from(keys, String);
												const retained = value
													.filter((item) => {
														const key = catalogId(item) ?? item.name;
														return !catalog.has(key) || !visibleKeys.has(key);
													})
													.map((item) => ({
														...item,
														name: resolveCatalogGenreName(item),
													}));
												const next = filtered
													.filter((item) => nextKeys.includes(item.id))
													.map((item) => ({
														id: item.id,
														name: resolveCatalogGenreName(item),
													}));
												onChange([...retained, ...next]);
											}}
											className="mt-2 max-h-56 overflow-y-auto"
										>
											{shownOptions.map((item) => (
												<ListBoxItem
													key={item.id}
													id={item.id}
													textValue={item.name}
												>
													{resolveCatalogGenreName(item)}
												</ListBoxItem>
											))}
										</ListBox>
									)}
									{shownOptions.length < filtered.length && (
										<Button
											className="mt-2 w-full"
											onPress={() => setVisibleCount((count) => count + 60)}
										>
											{m("genre.more")} ·{" "}
											{filtered.length - shownOptions.length}
										</Button>
									)}
								</>
							)}
							<div className="mt-3 flex justify-end">
								<Button onPress={() => setOpen(false)}>
									{m("action.done")}
								</Button>
							</div>
						</Dialog>
					</div>
				</Modal>
			)}
		</div>
	);
}
