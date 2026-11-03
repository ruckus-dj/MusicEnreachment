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
	const [exactMessage, setExactMessage] = useState("");

	const availableReleases = selectedArtistId
		? releases.filter((release) => release.artistId === selectedArtistId)
		: releases;
	const textMatches =
		status === "ready"
			? entity === "artist"
				? searchMatchingArtists(submittedQuery, artists)
				: searchMatchingReleases(submittedQuery, availableReleases, artists)
			: [];
	const exactMatches =
		status === "ready" && mode === "identity"
			? findExactMatchingIdentity(entity, submittedQuery, artists, releases)
					.items
			: [];
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
		setExactMessage("");
		setError("");
		setStatus("loading");
		await Promise.resolve();
		try {
			if (mode === "identity") {
				const result = findExactMatchingIdentity(
					entity,
					value,
					artists,
					releases,
				);
				setExactMessage(result.message);
			}
			setStatus("ready");
		} catch {
			setError(
				"Не удалось обработать поиск по fixture. Измените запрос и попробуйте снова.",
			);
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
				{release.tracks.length} позиций · {release.catalog}
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
				aria-label="Тип поиска"
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
					Издания
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
					Артисты
				</Button>
			</div>
			<div
				className={matchingSearchControls}
				role="group"
				aria-label="Способ поиска"
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
					По названию
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
					Точный ID / URL
				</Button>
			</div>
			<form onSubmit={submit}>
				<Label htmlFor="matching-provider-query">
					{mode === "identity"
						? "Идентификатор fixture или MusicBrainz MBID / URL"
						: entity === "artist"
							? "Имя артиста"
							: "Артист, издание, каталог или ID"}
				</Label>
				<div className="matching-search my-1.5 mb-2 flex gap-1.5">
					{mode === "identity" ? (
						<TextField
							aria-label="Идентификатор или MusicBrainz URL"
							value={query}
							onChange={setQuery}
						>
							<Input
								id="matching-provider-query"
								placeholder="MBID, официальный URL или fixture ID"
							/>
						</TextField>
					) : (
						<SearchField
							aria-label={
								entity === "artist" ? "Поиск артистов" : "Поиск изданий"
							}
							value={query}
							onChange={setQuery}
						>
							<Input
								id="matching-provider-query"
								placeholder={
									entity === "artist"
										? "Имя артиста…"
										: "Артист, альбом, каталог…"
								}
							/>
						</SearchField>
					)}
					<Button type="submit">Найти</Button>
				</div>
			</form>
			<p className="text-[11px] text-[var(--muted)]">
				Демо-поиск использует только записи и идентификаторы из неизменяемых
				fixture. Размер страницы: {pageSize}.
			</p>
			{showArtistReleases && (
				<p className="flex flex-wrap items-center gap-2 text-xs">
					<span>
						Издания выбранного артиста:{" "}
						{artistName(selectedArtistId ?? "", artists)}
					</span>
					<Button
						type="button"
						onPress={() => {
							setSelectedArtistId(null);
							setPage(0);
						}}
					>
						К результатам артистов
					</Button>
				</p>
			)}
			{status === "loading" && <p role="status">Поиск по fixture…</p>}
			{status === "error" && <p role="alert">{error}</p>}
			{status === "ready" && !hasResult && (
				<p className={matchingSearchResult} role="status">
					{mode === "identity"
						? exactMessage ||
							"Точная идентичность не найдена. Похожее имя не используется вместо MBID."
						: !submittedQuery
							? "Введите запрос и нажмите «Найти»; список не фильтруется до отправки."
							: showArtistReleases
								? "У выбранного артиста в fixture нет доступных изданий."
								: "Совпадений в fixture нет. Поиск выполняется только по текущим демонстрационным данным."}
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
													"Без уточнения"}{" "}
												· ID fixture: {item.id}
											</span>
											<span className="text-[10px] text-[var(--muted)]">
												Выбрать артиста и показать его издания
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
							Назад
						</Button>
						<span aria-live="polite">
							Страница {currentPage + 1} из {resultPageCount}
						</span>
						<Button
							type="button"
							isDisabled={currentPage + 1 >= resultPageCount}
							onPress={() => setPage(currentPage + 1)}
						>
							Дальше
						</Button>
					</div>
				</>
			)}
		</div>
	);
}
