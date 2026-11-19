import {
	type ManualSearchMessageKey,
	type ManualSearchViewItem,
	type ManualSearchViewProps,
	manualSearchMessage,
} from "./manualSearchContract";

function labelText(label: { name?: string; catalogNumber?: string }) {
	if (label.name && label.catalogNumber)
		return `${label.name} (${label.catalogNumber})`;
	return label.name ?? label.catalogNumber ?? "";
}

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
	const media =
		item.mediaCount > 1
			? [formats, message("discCount", { count: item.mediaCount })]
					.filter(Boolean)
					.join(" · ")
			: formats;
	return [
		item.date ? message("releaseDate", { date: item.date }) : undefined,
		item.country
			? message("releaseCountry", { country: item.country })
			: undefined,
		item.trackCount !== undefined
			? message("releaseTracks", { count: item.trackCount })
			: undefined,
		media ? message("releaseFormats", { formats: media }) : undefined,
		item.labels.length
			? message("releaseLabels", {
					labels: item.labels.map(labelText).join(", "),
				})
			: undefined,
		item.score !== undefined
			? message("score", { score: item.score })
			: undefined,
	].filter((detail): detail is string => Boolean(detail));
}

function chooseLabel(
	item: ManualSearchViewItem,
	selected: boolean,
	message: (
		key: ManualSearchMessageKey,
		values?: Record<string, string | number>,
	) => string,
) {
	const details = [
		item.provider,
		item.artistCredit,
		...editionDetails(item, message),
	]
		.filter(Boolean)
		.join(", ");
	return message(
		selected
			? "selectedReleaseDescribed"
			: item.provider === "MusicBrainz"
				? "prepareAndSelectReleaseDescribed"
				: "selectReleaseDescribed",
		{ title: item.title, details },
	);
}

function pageLimitValue(limit: string) {
	const value = Number(limit);
	return Number.isInteger(value) && value >= 1 && value <= 100 ? value : 0;
}

export function ManualSearchView({
	locale,
	query,
	limit,
	offset,
	disabled = false,
	pending = false,
	results,
	errors = [],
	selectedItemId,
	onQueryChange,
	onLimitChange,
	onSearch,
	onSelectRelease,
	onPrevious,
	onNext,
}: ManualSearchViewProps) {
	const message = (
		key: ManualSearchMessageKey,
		values?: Record<string, string | number>,
	) => manualSearchMessage(locale, key, values);
	const pageLimit = pageLimitValue(limit);
	const canSearch = Boolean(query.trim() && pageLimit && !pending && !disabled);

	return (
		<section
			className="rounded border border-[var(--line)] bg-[var(--surface)] p-4"
			aria-labelledby="manual-search-title"
		>
			<h2 id="manual-search-title" className="font-semibold">
				{message("searchTitle")}
			</h2>
			<p className="mt-1 text-sm">{message("searchDescription")}</p>
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
					{message("searching")}
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
						<Button onPress={error.onRetry}>{message("retryReading")}</Button>
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
						<ul className="mt-3 divide-y divide-[var(--line)] rounded border border-[var(--line)]">
							{results.items.map((item) => {
								const details = editionDetails(item, message);
								const selected = selectedItemId === item.id;
								return (
									<li
										key={item.id}
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
											<p className="text-sm wrap-anywhere">
												{[item.provider, item.artistCredit]
													.filter(Boolean)
													.join(" · ")}
											</p>
											{details.length ? (
												<p className="mt-1 text-xs text-[var(--muted)] wrap-anywhere">
													{details.join(" · ")}
												</p>
											) : null}
										</div>
										<Button
											aria-label={chooseLabel(item, selected, message)}
											aria-pressed={
												selectedItemId === undefined
													? undefined
													: selectedItemId === item.id
											}
											aria-disabled={selected || undefined}
											onPress={() => {
												if (!selected) onSelectRelease(item.id);
											}}
										>
											{message(
												selected
													? "selectedRelease"
													: item.provider === "MusicBrainz"
														? "prepareAndSelectRelease"
														: "selectRelease",
											)}
										</Button>
									</li>
								);
							})}
						</ul>
					)}
					{results.count > 0 && (
						<div className="mt-3 flex flex-wrap items-center gap-2">
							<span>
								{message("resultRange", {
									start: offset + 1,
									end: Math.min(offset + pageLimit, results.count),
									count: results.count,
								})}
							</span>
							<Button onPress={onPrevious} isDisabled={offset === 0 || pending}>
								{message("previous")}
							</Button>
							<Button
								onPress={onNext}
								isDisabled={offset + pageLimit >= results.count || pending}
							>
								{message("next")}
							</Button>
						</div>
					)}
				</>
			) : null}
		</section>
	);
}

import { Button as AriaButton, type ButtonProps } from "react-aria-components";
