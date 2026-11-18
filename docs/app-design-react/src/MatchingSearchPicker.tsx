import { useState } from "react";
import type { FormEvent } from "react";
import { Button, Input, Label, SearchField } from "./ui";
import type { CatalogArtist, OriginalRelease } from "./catalogData";
import {
	DEFAULT_MATCHING_PAGE_SIZE,
	matchingArtistCredit,
	matchingEditionDetails,
	matchingPage,
	searchMatchingReleases,
} from "./matchingSearch";
import { matchingSearchResult } from "./matchingStyles";
import { useI18n } from "./i18n/I18nProvider";
import { matchingSearchPickerMessages } from "./matchingSearchPickerMessages";

type Props = {
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	selectedReleaseId: string | null;
	onSelectRelease: (id: string) => void;
	pageSize?: number;
};

export function MatchingSearchPicker({
	artists,
	releases,
	selectedReleaseId,
	onSelectRelease,
	pageSize = DEFAULT_MATCHING_PAGE_SIZE,
}: Props) {
	const { locale } = useI18n();
	const m = (
		key: keyof typeof matchingSearchPickerMessages.en,
		values: Record<string, string | number> = {},
	) =>
		matchingSearchPickerMessages[locale][key].replace(
			/\{([^}]+)\}/g,
			(_, name: string) => String(values[name] ?? `{${name}}`),
		);
	const [query, setQuery] = useState("");
	const [submittedQuery, setSubmittedQuery] = useState("");
	const [page, setPage] = useState(0);
	const [status, setStatus] = useState<"idle" | "loading" | "ready" | "error">(
		"idle",
	);
	const [error, setError] = useState("");

	const results =
		status === "ready"
			? searchMatchingReleases(submittedQuery, releases, artists)
			: [];
	const pageResult = matchingPage(results, page, pageSize);

	const submit = async (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		const value = query.trim();
		setSubmittedQuery(value);
		setPage(0);
		setError("");
		setStatus("loading");
		await Promise.resolve();
		try {
			searchMatchingReleases(value, releases, artists);
			setStatus("ready");
		} catch {
			setError(m("searchError"));
			setStatus("error");
		}
	};

	const renderRelease = (release: OriginalRelease) => {
		const artist = matchingArtistCredit(release, artists);
		const details = matchingEditionDetails(release);
		const labels = details.labels
			.map((label) =>
				label.name && label.catalogNumber
					? `${label.name} (${label.catalogNumber})`
					: (label.name ?? label.catalogNumber ?? ""),
			)
			.filter(Boolean);
		const formats = details.formats.join(" / ");
		const media =
			details.mediaCount > 1
				? [formats, m("discCount", { count: details.mediaCount })]
						.filter(Boolean)
						.join(" · ")
				: formats;
		const editionDetails = [
			details.date ? m("releaseDate", { date: details.date }) : "",
			details.country ? m("releaseCountry", { country: details.country }) : "",
			details.trackCount
				? m("releaseTracks", { count: details.trackCount })
				: "",
			media ? m("releaseFormats", { formats: media }) : "",
			labels.length ? m("releaseLabels", { labels: labels.join(", ") }) : "",
		].filter(Boolean);
		return (
			<Button
				key={release.id}
				className={`candidate-button grid min-h-0 w-full justify-start rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-2.5 py-[9px] text-left [white-space:normal] hover:border-[var(--accent)] hover:bg-[var(--raised)] ${selectedReleaseId === release.id ? "selected border-[var(--accent)] bg-[var(--accent-soft)]" : ""}`}
				aria-pressed={selectedReleaseId === release.id}
				onPress={() => onSelectRelease(release.id)}
			>
				<strong className="text-xs leading-[1.35]">{release.title}</strong>
				{artist && (
					<span className="[overflow-wrap:anywhere] text-[10px] text-[var(--muted)]">
						{artist}
					</span>
				)}
				{editionDetails.length > 0 && (
					<span className="[overflow-wrap:anywhere] text-[10px] text-[var(--muted)]">
						{editionDetails.join(" · ")}
					</span>
				)}
			</Button>
		);
	};

	return (
		<div className="matching-picker">
			<form onSubmit={submit}>
				<Label htmlFor="matching-provider-query">
					{m("releaseSearchLabel")}
				</Label>
				<div className="matching-search my-1.5 mb-2 flex gap-1.5">
					<SearchField
						aria-label={m("releaseSearchAriaLabel")}
						value={query}
						onChange={setQuery}
					>
						<Input
							id="matching-provider-query"
							placeholder={m("releasePlaceholder")}
						/>
					</SearchField>
					<Button type="submit">{m("search")}</Button>
				</div>
			</form>
			<p className="text-[11px] text-[var(--muted)]">
				{m("availableRecords", { pageSize })}
			</p>
			{status === "loading" && <p role="status">{m("searching")}</p>}
			{status === "error" && <p role="alert">{error}</p>}
			{status === "ready" && !pageResult.items.length && (
				<p className={matchingSearchResult} role="status">
					{submittedQuery ? m("noMatches") : m("enterQuery")}
				</p>
			)}
			{status === "ready" && pageResult.items.length > 0 && (
				<>
					<div className="candidate-list my-2.5 grid gap-1.5">
						{pageResult.items.map(renderRelease)}
					</div>
					<div className="flex items-center justify-between gap-2 text-xs">
						<Button
							type="button"
							isDisabled={pageResult.page <= 0}
							onPress={() => setPage(pageResult.page - 1)}
						>
							{m("previous")}
						</Button>
						<span aria-live="polite">
							{m("page", {
								current: pageResult.page + 1,
								total: pageResult.pageCount,
							})}
						</span>
						<Button
							type="button"
							isDisabled={pageResult.page + 1 >= pageResult.pageCount}
							onPress={() => setPage(pageResult.page + 1)}
						>
							{m("next")}
						</Button>
					</div>
				</>
			)}
		</div>
	);
}
