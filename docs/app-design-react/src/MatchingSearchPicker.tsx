import { useState } from "react";
import type { FormEvent } from "react";
import { Button, Input, Label, SearchField, TextField } from "./ui";
import type { CatalogArtist, OriginalRelease } from "./catalogData";
import { artistName } from "./incomingModel";
import {
	DEFAULT_MATCHING_PAGE_SIZE,
	findExactMatchingIdentity,
	matchingPage,
	searchMatchingArtists,
	searchMatchingReleases,
	type MatchingSearchEntity,
} from "./matchingSearch";
import { matchingSearchControls, matchingSearchResult } from "./matchingStyles";
import { useI18n } from "./i18n/I18nProvider";
import { matchingSearchPickerMessages } from "./matchingSearchPickerMessages";

type Props = {
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	selectedReleaseId: string | null;
	onSelectRelease: (id: string) => void;
	pageSize?: number;
};

type SearchMode = "text" | "identity";

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
	const [entity, setEntity] = useState<MatchingSearchEntity>("release");
	const [mode, setMode] = useState<SearchMode>("text");
	const [query, setQuery] = useState("");
	const [submittedQuery, setSubmittedQuery] = useState("");
	const [selectedArtistId, setSelectedArtistId] = useState<string | null>(null);
	const [page, setPage] = useState(0);
	const [status, setStatus] = useState<"idle" | "loading" | "ready" | "error">(
		"idle",
	);
	const [error, setError] = useState("");

	const availableReleases = selectedArtistId
		? releases.filter((release) => release.artistId === selectedArtistId)
		: releases;
	const textMatches =
		status === "ready"
			? entity === "artist"
				? searchMatchingArtists(submittedQuery, artists)
				: searchMatchingReleases(submittedQuery, availableReleases, artists)
			: [];
	const exactResult =
		status === "ready" && mode === "identity"
			? findExactMatchingIdentity(
					entity,
					submittedQuery,
					artists,
					releases,
					locale,
				)
			: null;
	const exactMatches = exactResult?.items ?? [];
	const exactMessage = exactResult?.message ?? "";
	const results = mode === "identity" ? exactMatches : textMatches;
	const pageResult = matchingPage(results, page, pageSize);
	const showArtistReleases = entity === "artist" && selectedArtistId !== null;
	const artistReleases = selectedArtistId
		? matchingPage(availableReleases, page, pageSize)
		: null;

	const submit = async (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		const value = query.trim();
		setSubmittedQuery(value);
		setPage(0);
		setSelectedArtistId(null);
		setError("");
		setStatus("loading");
		await Promise.resolve();
		try {
			if (mode === "identity")
				findExactMatchingIdentity(entity, value, artists, releases, locale);
			setStatus("ready");
		} catch {
			setError(m("searchError"));
			setStatus("error");
		}
	};

	const chooseArtist = (artistId: string) => {
		setSelectedArtistId(artistId);
		setPage(0);
	};

	const renderRelease = (release: OriginalRelease) => (
		<Button
			key={release.id}
			className={`candidate-button grid min-h-0 w-full justify-start rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-2.5 py-[9px] text-left [white-space:normal] hover:border-[var(--accent)] hover:bg-[var(--raised)] ${selectedReleaseId === release.id ? "selected border-[var(--accent)] bg-[var(--accent-soft)]" : ""}`}
			aria-pressed={selectedReleaseId === release.id}
			onPress={() => onSelectRelease(release.id)}
		>
			<strong className="text-xs leading-[1.35]">{release.title}</strong>
			<span className="[overflow-wrap:anywhere] text-[10px] text-[var(--muted)]">
				{artistName(String(release.artistId), artists)} · {release.year} ·{" "}
				{release.country}
			</span>
			<span className="[overflow-wrap:anywhere] text-[10px] text-[var(--muted)]">
				{m("releaseTrackCount", { count: release.tracks.length })} ·{" "}
				{release.catalog}
			</span>
			<code className="[overflow-wrap:anywhere] text-[10px] text-[var(--muted)]">
				{release.id}
			</code>
		</Button>
	);

	const hasResult = showArtistReleases
		? (artistReleases?.items.length ?? 0) > 0
		: pageResult.items.length > 0;
	const resultPageCount = showArtistReleases
		? (artistReleases?.pageCount ?? 1)
		: pageResult.pageCount;
	const currentPage = showArtistReleases
		? (artistReleases?.page ?? 0)
		: pageResult.page;

	return (
		<div className="matching-picker">
			<div
				className={matchingSearchControls}
				role="group"
				aria-label={m("searchType")}
			>
				<Button
					type="button"
					aria-pressed={entity === "release"}
					onPress={() => {
						setEntity("release");
						setStatus("idle");
						setPage(0);
						setSelectedArtistId(null);
					}}
				>
					{m("releases")}
				</Button>
				<Button
					type="button"
					aria-pressed={entity === "artist"}
					onPress={() => {
						setEntity("artist");
						setStatus("idle");
						setPage(0);
						setSelectedArtistId(null);
					}}
				>
					{m("artists")}
				</Button>
			</div>
			<div
				className={matchingSearchControls}
				role="group"
				aria-label={m("searchMethod")}
			>
				<Button
					type="button"
					aria-pressed={mode === "text"}
					onPress={() => {
						setMode("text");
						setStatus("idle");
						setPage(0);
					}}
				>
					{m("byName")}
				</Button>
				<Button
					type="button"
					aria-pressed={mode === "identity"}
					onPress={() => {
						setMode("identity");
						setStatus("idle");
						setPage(0);
					}}
				>
					{m("exactIdUrl")}
				</Button>
			</div>
			<form onSubmit={submit}>
				<Label htmlFor="matching-provider-query">
					{mode === "identity"
						? m("identityLabel")
						: entity === "artist"
							? m("artistNameLabel")
							: m("releaseSearchLabel")}
				</Label>
				<div className="matching-search my-1.5 mb-2 flex gap-1.5">
					{mode === "identity" ? (
						<TextField
							aria-label={m("identityAriaLabel")}
							value={query}
							onChange={setQuery}
						>
							<Input
								id="matching-provider-query"
								placeholder={m("identityPlaceholder")}
							/>
						</TextField>
					) : (
						<SearchField
							aria-label={m(
								entity === "artist"
									? "artistSearchAriaLabel"
									: "releaseSearchAriaLabel",
							)}
							value={query}
							onChange={setQuery}
						>
							<Input
								id="matching-provider-query"
								placeholder={
									entity === "artist"
										? m("artistPlaceholder")
										: m("releasePlaceholder")
								}
							/>
						</SearchField>
					)}
					<Button type="submit">{m("search")}</Button>
				</div>
			</form>
			<p className="text-[11px] text-[var(--muted)]">
				{m("availableRecords", { pageSize })}
			</p>
			{showArtistReleases && (
				<p className="flex flex-wrap items-center gap-2 text-xs">
					<span>
						{m("releasesForArtist", {
							artist: artistName(selectedArtistId ?? "", artists),
						})}
					</span>
					<Button
						type="button"
						onPress={() => {
							setSelectedArtistId(null);
							setPage(0);
						}}
					>
						{m("artistResults")}
					</Button>
				</p>
			)}
			{status === "loading" && <p role="status">{m("searching")}</p>}
			{status === "error" && <p role="alert">{error}</p>}
			{status === "ready" && !hasResult && (
				<p className={matchingSearchResult} role="status">
					{mode === "identity"
						? exactMessage || m("identityNotFound")
						: !submittedQuery
							? m("enterQuery")
							: showArtistReleases
								? m("noArtistReleases")
								: m("noMatches")}
				</p>
			)}
			{status === "ready" && hasResult && (
				<>
					<div className="candidate-list my-2.5 grid gap-1.5">
						{showArtistReleases
							? artistReleases?.items.map(renderRelease)
							: pageResult.items.map((item) =>
									entity === "artist" ? (
										<Button
											key={item.id}
											className="candidate-button grid w-full justify-start rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-2.5 py-[9px] text-left"
											aria-pressed={selectedArtistId === item.id}
											onPress={() => chooseArtist(item.id)}
										>
											<strong>{(item as CatalogArtist).name}</strong>
											<span className="text-[10px] text-[var(--muted)]">
												{(item as CatalogArtist).disambiguation ||
													m("artistDisambiguation")}{" "}
												· {m("artistId", { id: item.id })}
											</span>
											<span className="text-[10px] text-[var(--muted)]">
												{m("showArtistReleases")}
											</span>
										</Button>
									) : (
										renderRelease(item as OriginalRelease)
									),
								)}
					</div>
					<div className="flex items-center justify-between gap-2 text-xs">
						<Button
							type="button"
							isDisabled={currentPage <= 0}
							onPress={() => setPage(currentPage - 1)}
						>
							{m("previous")}
						</Button>
						<span aria-live="polite">
							{m("page", { current: currentPage + 1, total: resultPageCount })}
						</span>
						<Button
							type="button"
							isDisabled={currentPage + 1 >= resultPageCount}
							onPress={() => setPage(currentPage + 1)}
						>
							{m("next")}
						</Button>
					</div>
				</>
			)}
		</div>
	);
}
