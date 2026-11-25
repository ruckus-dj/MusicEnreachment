import { useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import { ManualSearchView } from "../../../ui/features/matching/ManualSearchView";
import {
	type ManualSearchSelection,
	type ManualSearchViewItem,
	manualSearchMessage,
} from "../../../ui/features/matching/manualSearchContract";
import type { CatalogArtist, OriginalRelease } from "../library/catalogData";
import {
	DEFAULT_MATCHING_PAGE_SIZE,
	matchingArtistCredit,
	matchingEditionDetails,
	matchingPage,
	searchMatchingReleases,
} from "./matchingSearch";

type Props = {
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	selectedReleaseId: string | null;
	onSelectRelease: (release: OriginalRelease) => boolean;
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
		provider: searchProvider(release),
		...(artistCredit ? { artistCredit } : {}),
		...(details.disambiguation
			? { disambiguation: details.disambiguation }
			: {}),
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
	const [selectionError, setSelectionError] = useState(false);

	const pageLimit = limitValue(limit) || pageSize;
	const results =
		status === "ready"
			? searchMatchingReleases(submittedQuery, releases, artists)
			: undefined;
	const pageResult = matchingPage(results ?? [], page, pageLimit);
	const selectedRelease = releases.find(
		(release) => release.id === selectedReleaseId,
	);
	const selectedItem: ManualSearchSelection | null = selectedRelease
		? { id: selectedRelease.id, provider: searchProvider(selectedRelease) }
		: null;

	const submit = async () => {
		const value = query.trim();
		if (!value || !limitValue(limit)) return;
		setSubmittedQuery(value);
		setPage(0);
		setError("");
		setSelectionError(false);
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
			errors={[
				...(status === "error" ? [{ id: "search", message: error }] : []),
				...(selectionError
					? [
							{
								id: "prepare",
								message: manualSearchMessage(locale, "prepareFailed"),
							},
						]
					: []),
			]}
			selectedItem={selectedItem}
			onQueryChange={(value) => {
				setQuery(value);
				setPage(0);
				setStatus("idle");
				setSelectionError(false);
			}}
			onLimitChange={(value) => {
				setLimit(value);
				setPage(0);
				setStatus("idle");
				setSelectionError(false);
			}}
			onSearch={() => void submit()}
			onSelectItem={({ id, provider }) => {
				const release = pageResult.items.find(
					(item) => item.id === id && searchProvider(item) === provider,
				);
				if (release) setSelectionError(!onSelectRelease(release));
			}}
			onPrevious={() => setPage(Math.max(0, pageResult.page - 1))}
			onNext={() => setPage(pageResult.page + 1)}
		/>
	);
}

function searchProvider(
	release: OriginalRelease,
): ManualSearchSelection["provider"] {
	return release.provider === "Local" ? "Local" : "MusicBrainz";
}
