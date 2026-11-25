import { Button as AriaButton, type ButtonProps } from "react-aria-components";
import {
	type ManualSearchMessageKey,
	type ManualSearchSelection,
	type ManualSearchViewItem,
	type ManualSearchViewProps,
	manualSearchMessage,
} from "./manualSearchContract";

function Button({ className, ...props }: ButtonProps) {
	return (
		<AriaButton
			{...props}
			className={`inline-flex min-h-[30px] cursor-pointer items-center justify-center gap-1.5 rounded-[var(--control-radius,4px)] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-[var(--ink)] hover:border-[var(--accent)] hover:bg-[var(--raised)] active:bg-[var(--accent-soft)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 pointer-coarse:min-h-[40px] ${className ?? ""}`}
		/>
	);
}

function editionDetails(
	item: ManualSearchViewItem,
	message: (
		key: ManualSearchMessageKey,
		values?: Record<string, string | number>,
	) => string,
) {
	const formats = item.formats.join(" / ");
	const labels = item.labels.flatMap(({ name, catalogNumber }) =>
		name ? [catalogNumber ? `${name} (${catalogNumber})` : name] : [],
	);
	const unpairedNumbers = item.labels.flatMap(({ name, catalogNumber }) =>
		!name && catalogNumber ? [catalogNumber] : [],
	);
	return [
		item.releaseGroupTitle
			? message("releaseGroup", { title: item.releaseGroupTitle })
			: undefined,
		item.date ? message("releaseDate", { date: item.date }) : undefined,
		item.country
			? message("releaseCountry", { country: item.country })
			: undefined,
		item.trackCount !== undefined
			? message("releaseTracks", { count: item.trackCount })
			: undefined,
		formats ? message("releaseFormats", { formats }) : undefined,
		item.mediaCount > 1
			? message("discCount", { count: item.mediaCount })
			: undefined,
		labels.length
			? message("releaseLabels", { labels: labels.join(", ") })
			: undefined,
		unpairedNumbers.length
			? message("catalogNumbers", { numbers: unpairedNumbers.join(", ") })
			: undefined,
		item.score !== undefined
			? message("score", { score: item.score })
			: undefined,
	].filter((detail): detail is string => Boolean(detail));
}

function chooseLabel(
	item: ManualSearchViewItem,
	selected: boolean,
	showProviders: boolean,
	message: (
		key: ManualSearchMessageKey,
		values?: Record<string, string | number>,
	) => string,
) {
	const details = [
		showProviders ? item.provider : undefined,
		item.disambiguation,
		item.artistCredit
			? message("releaseArtist", { artist: item.artistCredit })
			: undefined,
		...editionDetails(item, message),
	]
		.filter(Boolean)
		.join(", ");
	return details
		? message(
				selected ? "selectedReleaseDescribed" : "selectReleaseDescribed",
				{ title: item.title, details },
			)
		: message(selected ? "selectedReleaseTitled" : "selectReleaseTitled", {
				title: item.title,
			});
}

function normalizedSelection(value: ManualSearchSelection | null | undefined) {
	if (!value) return undefined;
	const { provider } = value;
	const id = typeof value.id === "string" ? value.id.trim() : "";
	if (!id || (provider !== "Local" && provider !== "MusicBrainz"))
		return undefined;
	return { provider, id };
}

function isItemSelected(
	item: ManualSearchViewItem,
	selection: ManualSearchSelection | undefined,
) {
	return (
		selection !== undefined &&
		item.provider === selection.provider &&
		item.id === selection.id
	);
}

function pageLimitValue(limit: string) {
	const value = Number(limit);
	return Number.isInteger(value) && value >= 1 && value <= 100 ? value : 0;
}

export function ManualSearchView({
	locale,
	variant = "editions",
	query,
	limit,
	offset,
	disabled = false,
	pending = false,
	results,
	errors = [],
	selectedItem,
	onQueryChange,
	onLimitChange,
	onSearch,
	onSelectItem,
	onPrevious,
	onNext,
}: ManualSearchViewProps) {
	const message = (
		key: ManualSearchMessageKey,
		values?: Record<string, string | number>,
	) => manualSearchMessage(locale, key, values);
	const pageLimit = pageLimitValue(limit);
	const canSearch = Boolean(query.trim() && pageLimit && !pending && !disabled);
	const showProviders = variant === "editions";
	const selection = normalizedSelection(selectedItem);
	const safeOffset = Number.isSafeInteger(offset) && offset >= 0 ? offset : 0;

	return (
		<section
			className="rounded border border-[var(--line)] bg-[var(--surface)] p-4"
			aria-labelledby="manual-search-title"
		>
			<h2 id="manual-search-title" className="font-semibold">
				{message(showProviders ? "searchTitleEditions" : "searchTitle")}
			</h2>
			<p className="mt-1 text-sm">
				{message(
					showProviders ? "searchDescriptionEditions" : "searchDescription",
				)}
			</p>
			<form
				className="mt-3 flex flex-wrap gap-2"
				onSubmit={(event) => {
					event.preventDefault();
					if (canSearch) onSearch();
				}}
			>
				<label className="sr-only" htmlFor="manual-search-query">
					{message("searchQuery")}
				</label>
				<input
					id="manual-search-query"
					className="min-w-0 basis-64 flex-1 rounded border border-[var(--control)] bg-[var(--raised)] p-2 text-[var(--ink)] max-[600px]:min-h-10"
					value={query}
					disabled={pending || disabled}
					onChange={(event) => onQueryChange(event.target.value)}
				/>
				<label className="sr-only" htmlFor="manual-search-limit">
					{message("resultLimit")}
				</label>
				<input
					id="manual-search-limit"
					type="number"
					min="1"
					max="100"
					aria-label={message("resultLimit")}
					className="w-28 rounded border border-[var(--control)] bg-[var(--raised)] p-2 text-[var(--ink)] max-[600px]:min-h-10"
					value={limit}
					disabled={pending || disabled}
					onChange={(event) => onLimitChange(event.target.value)}
				/>
				<Button type="submit" isDisabled={!canSearch}>
					{message("search")}
				</Button>
			</form>
			{pending ? (
				<p role="status" className="mt-2">
					{message(showProviders ? "searchingEditions" : "searching")}
				</p>
			) : null}
			{errors.map((error) =>
				error.onRetry ? (
					<div
						role="alert"
						className="mt-2 space-y-2 text-[var(--error)]"
						key={error.id}
					>
						<p>{error.message}</p>
						<Button onPress={error.onRetry} isDisabled={disabled}>
							{message("retryReading")}
						</Button>
					</div>
				) : (
					<p role="alert" className="mt-2 text-[var(--error)]" key={error.id}>
						{error.message}
					</p>
				),
			)}
			{results ? (
				<>
					{results.items.length === 0 ? (
						<p role="status" className="mt-3">
							{message("noMatches")}
						</p>
					) : (
						<ul className="mt-3 max-h-60 overflow-y-auto divide-y divide-[var(--line)] rounded border border-[var(--line)]">
							{results.items.map((item) => {
								const details = editionDetails(item, message);
								const selected = isItemSelected(item, selection);
								return (
									<li
										key={`${item.provider}:${item.id}`}
										className="flex flex-wrap items-start justify-between gap-3 p-3"
									>
										<div className="min-w-0 flex-1">
											<p className="font-medium">
												<span>{item.title}</span>
												{item.disambiguation ? (
													<span className="ml-1 text-sm font-normal text-[var(--muted)]">
														({item.disambiguation})
													</span>
												) : null}
											</p>
											{showProviders ? (
												<p className="mt-1 flex flex-wrap items-center gap-2 text-sm wrap-anywhere">
													<span className="rounded border border-[var(--line)] bg-[var(--raised)] px-1.5 py-0.5 text-xs text-[var(--muted)]">
														{item.provider}
													</span>
													{item.artistCredit ? (
														<span>
															{message("releaseArtist", {
																artist: item.artistCredit,
															})}
														</span>
													) : null}
												</p>
											) : item.artistCredit ? (
												<p className="text-sm">
													{message("releaseArtist", {
														artist: item.artistCredit,
													})}
												</p>
											) : null}
											{details.length ? (
												<p className="mt-1 text-xs text-[var(--muted)] wrap-anywhere">
													{details.join(" · ")}
												</p>
											) : null}
										</div>
										<Button
											aria-label={chooseLabel(
												item,
												selected,
												showProviders,
												message,
											)}
											aria-pressed={selection ? selected : undefined}
											aria-disabled={disabled || selected || undefined}
											onPress={() => {
												if (disabled || selected) return;
												onSelectItem({ provider: item.provider, id: item.id });
											}}
										>
											{message(selected ? "selectedRelease" : "selectRelease")}
										</Button>
									</li>
								);
							})}
						</ul>
					)}
					{pageLimit > 0 && results.count > 0 && safeOffset < results.count ? (
						<div className="mt-3 flex flex-wrap items-center gap-2">
							<span>
								{message("resultRange", {
									start: safeOffset + 1,
									end: Math.min(safeOffset + pageLimit, results.count),
									count: results.count,
								})}
							</span>
							<Button
								onPress={onPrevious}
								isDisabled={disabled || safeOffset === 0 || pending}
							>
								{message("previous")}
							</Button>
							<Button
								onPress={onNext}
								isDisabled={
									disabled || safeOffset + pageLimit >= results.count || pending
								}
							>
								{message("next")}
							</Button>
						</div>
					) : null}
				</>
			) : null}
		</section>
	);
}
