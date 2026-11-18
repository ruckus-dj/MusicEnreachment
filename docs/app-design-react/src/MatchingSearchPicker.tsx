import { useState } from "react";
import type { CatalogArtist, OriginalRelease } from "./catalogData";
import {
	DEFAULT_MATCHING_PAGE_SIZE,
	matchingArtistCredit,
	matchingEditionDetails,
	matchingPage,
	searchMatchingReleases,
} from "./matchingSearch";
import { useI18n } from "./i18n/I18nProvider";
import { ManualSearchView } from "./matching-workspace/ManualSearchView";
import {
	manualSearchMessage,
	type ManualSearchViewItem,
} from "./matching-workspace/manualSearchContract";

type Props = {
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	selectedReleaseId: string | null;
	onSelectRelease: (id: string) => void;
	pageSize?: number;
};

function viewItem(
	release: OriginalRelease,
	artists: CatalogArtist[],
): ManualSearchViewItem {
	const details = matchingEditionDetails(release);
	const artistCredit = matchingArtistCredit(release, artists);
	return {
		id: release.id,
		title: release.title,
		...(artistCredit ? { artistCredit } : {}),
		...(details.date ? { date: details.date } : {}),
		...(details.country ? { country: details.country } : {}),
		trackCount: details.trackCount,
		formats: details.formats,
		mediaCount: details.mediaCount,
		labels: details.labels,
	};
}

function limitValue(limit: string) {
	const value = Number(limit);
	return Number.isInteger(value) && value >= 1 && value <= 100 ? value : 0;
}

export function MatchingSearchPicker({
	artists,
	releases,
	selectedReleaseId,
	onSelectRelease,
	pageSize = DEFAULT_MATCHING_PAGE_SIZE,
}: Props) {
	const { locale } = useI18n();
	const [query, setQuery] = useState("");
	const [submittedQuery, setSubmittedQuery] = useState("");
	const [limit, setLimit] = useState(String(pageSize));
	const [page, setPage] = useState(0);
	const [status, setStatus] = useState<"idle" | "loading" | "ready" | "error">(
		"idle",
	);
	const [error, setError] = useState("");

	const pageLimit = limitValue(limit) || pageSize;
	const results =
		status === "ready"
			? searchMatchingReleases(submittedQuery, releases, artists)
			: undefined;
	const pageResult = matchingPage(results ?? [], page, pageLimit);

	const submit = async () => {
		const value = query.trim();
		if (!value || !limitValue(limit)) return;
		setSubmittedQuery(value);
		setPage(0);
		setError("");
		setStatus("loading");
		await Promise.resolve();
		try {
			searchMatchingReleases(value, releases, artists);
			setStatus("ready");
		} catch {
			setError(manualSearchMessage(locale, "searchFailed"));
			setStatus("error");
		}
	};

	return (
		<ManualSearchView
			locale={locale}
			query={query}
			limit={limit}
			offset={pageResult.page * pageLimit}
			pending={status === "loading"}
			results={
				results
					? {
							count: results.length,
							items: pageResult.items.map((release) =>
								viewItem(release, artists),
							),
						}
					: undefined
			}
			errors={status === "error" ? [{ id: "search", message: error }] : []}
			selectedItemId={selectedReleaseId}
			onQueryChange={(value) => {
				setQuery(value);
				setPage(0);
				setStatus("idle");
			}}
			onLimitChange={(value) => {
				setLimit(value);
				setPage(0);
				setStatus("idle");
			}}
			onSearch={() => void submit()}
			onSelectRelease={onSelectRelease}
			onPrevious={() => setPage(Math.max(0, pageResult.page - 1))}
			onNext={() => setPage(pageResult.page + 1)}
		/>
	);
}
