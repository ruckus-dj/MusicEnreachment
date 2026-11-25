import { useState } from "react";
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
} from "../components/ui";
import { metadataMessage } from "./messages";

export type GenreReference = { id?: string; name: string };
export type GenreOption = { id: string; name: string };
export type GenrePickerSelection = { key: string; name: string };
export type GenrePickerRow = { id: string; name: string; label: string };

export type GenrePickerViewProps = {
	label?: string;
	displayValue?: string;
	readOnly: boolean;
	loaded: boolean;
	canLoad: boolean;
	loading: boolean;
	loadError: boolean;
	query: string;
	catalogEmpty: boolean;
	rows: readonly GenrePickerRow[];
	selected: readonly GenrePickerSelection[];
	visibleSelectedKeys: ReadonlySet<string>;
	remainingCount: number;
	onQueryChange: (query: string) => void;
	onLoad: () => void;
	onRemove: (key: string) => void;
	onShowMore: () => void;
	onSelectionChange: (keys: "all" | readonly string[]) => void;
	onResetSearch: () => void;
	onResetQuery: () => void;
};

/**
 * Pure genre picker presentation. Catalog defaulting, canonical names, search
 * ranking, retention and load state live in the demo provider; this view only
 * owns the open state and renders the supplied model.
 */
export function GenrePickerView({
	label,
	displayValue,
	readOnly,
	loaded,
	canLoad,
	loading,
	loadError,
	query,
	catalogEmpty,
	rows,
	selected,
	visibleSelectedKeys,
	remainingCount,
	onQueryChange,
	onLoad,
	onRemove,
	onShowMore,
	onSelectionChange,
	onResetSearch,
	onResetQuery,
}: GenrePickerViewProps) {
	const { locale } = useI18n();
	const m = (key: Parameters<typeof metadataMessage>[1]) =>
		metadataMessage(locale, key);
	const pickerLabel = label ?? m("genre.label");
	const [open, setOpen] = useState(false);
	const selectedNames = selected.map((item) => item.name);

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
						? selectedNames.length
							? selectedNames.join(", ")
							: displayValue || "—"
						: selectedNames.join(", ") || m("genre.choose")}
				</button>
			</div>
			{!readOnly && (
				<Modal
					isOpen={open}
					onOpenChange={(nextOpen) => {
						setOpen(nextOpen);
						if (!nextOpen) {
							onResetSearch();
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
								onResetQuery();
							}
						}}
					>
						<Dialog aria-label={`${pickerLabel} — ${m("genre.chooseDialog")}`}>
							<Heading slot="title">{pickerLabel}</Heading>
							{selected.length > 0 && (
								<div
									aria-label={m("genre.selected")}
									className="mb-2 flex flex-wrap gap-1"
								>
									{selected.map((item) => {
										return (
											<span
												key={item.key}
												className="inline-flex items-center gap-1 rounded-full border border-[var(--line)] px-2 py-1 text-sm"
											>
												{item.name}
												<Button
													aria-label={`${m("action.remove")} ${item.name}`}
													onPress={() => onRemove(item.key)}
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
									{canLoad ? (
										<Button onPress={onLoad} isDisabled={loading}>
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
										onChange={onQueryChange}
										autoFocus
									>
										<Input placeholder={m("genre.searchPlaceholder")} />
									</SearchField>
									{catalogEmpty ? (
										<p role="status" className="text-sm">
											{m("genre.empty")}
										</p>
									) : rows.length === 0 ? (
										<p role="status" className="text-sm">
											{m("genre.noMatches")}
										</p>
									) : (
										<ListBox
											aria-label={m("genre.available")}
											selectionMode="multiple"
											escapeKeyBehavior="none"
											selectedKeys={visibleSelectedKeys}
											onSelectionChange={(keys) =>
												onSelectionChange(
													keys === "all" ? "all" : Array.from(keys, String),
												)
											}
											className="mt-2 max-h-56 overflow-y-auto"
										>
											{rows.map((item) => (
												<ListBoxItem
													key={item.id}
													id={item.id}
													textValue={item.name}
												>
													{item.label}
												</ListBoxItem>
											))}
										</ListBox>
									)}
									{remainingCount > 0 && (
										<Button className="mt-2 w-full" onPress={onShowMore}>
											{m("genre.more")} · {remainingCount}
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
