import { useState } from "react";
import { Button, Checkbox, Dialog, Heading, Modal } from "./ui";
import type { Locale } from "./i18n/types";
import type { MetadataChangeGroup } from "./publicationMetadataDiff";
import {
	publicationFieldLabel,
	publicationGroupLabel,
	publicationMessage,
} from "./publicationMessages";

export type PublicationInspection = {
	id: string;
	position: string;
	title: string;
	context?: string;
	action?: string;
	reason?: string;
	source?: string;
	outputPath?: string;
	container?: string;
	groups: MetadataChangeGroup[];
	included?: boolean;
	includeDisabled?: boolean;
	onInclude?: (included: boolean) => void;
	includeLabel?: string;
};

function shownValue(values: string[], locale: Locale) {
	return values.length ? values : [publicationMessage(locale, "noValue")];
}

function changeValue(
	change: MetadataChangeGroup["changes"][number],
	values: string[],
	locale: Locale,
) {
	if (change.key !== "cover") return shownValue(values, locale);
	if (!values.length) return [publicationMessage(locale, "noValue")];
	const artworkState: Record<
		string,
		"artworkPresent" | "artworkCurrent" | "artworkNew"
	> = {
		Есть: "artworkPresent",
		"Есть · текущая": "artworkCurrent",
		"Есть · новая": "artworkNew",
	};
	return values.length
		? values.map((value) => {
				const key = artworkState[value];
				return key ? publicationMessage(locale, key) : value;
			})
		: [publicationMessage(locale, "noValue")];
}

function ValueList({ values }: { values: string[] }) {
	return (
		<ol className="m-0 grid list-none gap-1 p-0">
			{values.map((value, index) => (
				<li
					key={`${index}-${value}`}
					className="grid grid-cols-[1rem_minmax(0,1fr)] gap-1"
				>
					<span className="text-[var(--muted)]">{index + 1}.</span>
					<div className="flex min-w-0 items-start">
						<span aria-hidden="true">“</span>
						<pre className="m-0 min-w-0 flex-1 whitespace-pre-wrap break-words font-sans">
							{value}
						</pre>
						<span aria-hidden="true">”</span>
					</div>
				</li>
			))}
		</ol>
	);
}

function ChangeRows({
	groups,
	locale,
}: {
	groups: MetadataChangeGroup[];
	locale: Locale;
}) {
	if (!groups.length)
		return (
			<p className="m-0 text-sm text-[var(--muted)]">
				{publicationMessage(locale, "noChanges")}
			</p>
		);

	return (
		<div className="grid gap-3">
			{groups.map((group) => (
				<section
					key={group.key}
					aria-label={publicationGroupLabel(locale, group.key, group.label)}
				>
					<h3 className="mb-1.5 mt-0 border-b border-[var(--line)] pb-1 text-xs font-semibold text-[var(--muted)]">
						{publicationGroupLabel(locale, group.key, group.label)}
					</h3>
					<table className="w-full table-fixed border-collapse text-left text-xs">
						<colgroup>
							<col className="w-[30%]" />
							<col className="w-[35%]" />
							<col className="w-[35%]" />
						</colgroup>
						<thead>
							<tr className="border-b border-[var(--line)] text-[var(--muted)]">
								<th scope="col" className="px-1 py-2 font-medium">
									{publicationMessage(locale, "field")}
								</th>
								<th scope="col" className="px-1 py-2 font-medium">
									{publicationMessage(locale, "current")}
								</th>
								<th scope="col" className="px-1 py-2 font-medium">
									{publicationMessage(locale, "after")}
								</th>
							</tr>
						</thead>
						<tbody>
							{group.changes.map((change) => {
								const isLyrics = change.key === "lyrics";
								return (
									<tr
										key={change.key}
										className="border-b border-[var(--line)] align-top last:border-0"
									>
										<th
											scope="row"
											className="break-words px-1 py-2 font-medium"
											title={publicationFieldLabel(
												locale,
												change.key,
												change.label,
											)}
										>
											{publicationFieldLabel(locale, change.key, change.label)}
										</th>
										{isLyrics ? (
											<>
												<td colSpan={2} className="px-1 py-2">
													<div className="grid grid-cols-2 gap-2">
														<div className="min-w-0">
															<span className="block text-[10px] text-[var(--muted)]">
																{publicationMessage(locale, "current")}
															</span>
															<div className="max-h-36 overflow-auto rounded-sm bg-[var(--raised)] p-2 text-xs">
																<ValueList
																	values={changeValue(
																		change,
																		change.before,
																		locale,
																	)}
																/>
															</div>
														</div>
														<div className="min-w-0">
															<span className="block text-[10px] text-[var(--muted)]">
																{publicationMessage(locale, "after")}
															</span>
															<div className="max-h-36 overflow-auto rounded-sm bg-[var(--raised)] p-2 text-xs">
																<ValueList
																	values={changeValue(
																		change,
																		change.after,
																		locale,
																	)}
																/>
															</div>
														</div>
													</div>
												</td>
											</>
										) : (
											<>
												<td className="break-words px-1 py-2">
													<ValueList
														values={changeValue(change, change.before, locale)}
													/>
												</td>
												<td className="break-words px-1 py-2">
													<ValueList
														values={changeValue(change, change.after, locale)}
													/>
												</td>
											</>
										)}
									</tr>
								);
							})}
						</tbody>
					</table>
				</section>
			))}
		</div>
	);
}

function InspectorBody({
	item,
	locale,
}: {
	item: PublicationInspection | null;
	locale: Locale;
}) {
	if (!item)
		return (
			<div className="grid min-h-44 place-content-center gap-2 text-center">
				<strong>{publicationMessage(locale, "noPosition")}</strong>
				<p className="m-0 text-sm text-[var(--muted)]">
					{publicationMessage(locale, "selectHint")}
				</p>
			</div>
		);
	const willBeWritten = [item.outputPath, item.container]
		.filter((value): value is string => Boolean(value))
		.join(" · ");
	return (
		<>
			<div className="mb-3 border-b border-[var(--line)] pb-2">
				<p className="m-0 text-[11px] text-[var(--muted)]">
					{publicationMessage(locale, "changedFields")}:{" "}
					{item.groups.reduce((n, group) => n + group.changes.length, 0)}
				</p>
				<h3
					className="mb-0 mt-1 truncate text-base font-semibold"
					title={`${item.position}. ${item.title}`}
				>
					{item.position}. {item.title}
				</h3>
				{item.context && (
					<p
						className="mb-0 mt-1 truncate text-xs text-[var(--muted)]"
						title={item.context}
					>
						{item.context}
					</p>
				)}
				<p className="mb-1 mt-2 text-xs font-semibold">{item.action}</p>
				<p className="m-0 break-words text-xs text-[var(--muted)]">
					{item.reason ?? ""}
				</p>
				<dl className="mb-0 mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-1 text-[11px]">
					<dt className="text-[var(--muted)]">
						{publicationMessage(locale, "source")}
					</dt>
					<dd className="m-0 min-w-0 break-words">{item.source ?? ""}</dd>
					{willBeWritten && (
						<>
							<dt className="text-[var(--muted)]">
								{publicationMessage(locale, "willBeWritten")}
							</dt>
							<dd className="m-0 min-w-0 break-words">{willBeWritten}</dd>
						</>
					)}
				</dl>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto pr-1">
				<ChangeRows groups={item.groups} locale={locale} />
			</div>
		</>
	);
}

export function PublicationInspector({
	items,
	locale,
}: {
	items: PublicationInspection[];
	locale: Locale;
}) {
	const [selectedID, setSelectedID] = useState(items[0]?.id ?? "");
	const [dialogOpen, setDialogOpen] = useState(false);
	const selected =
		items.find((item) => item.id === selectedID) ?? items[0] ?? null;
	const count = (item: PublicationInspection) =>
		item.groups.reduce((total, group) => total + group.changes.length, 0);

	if (!items.length) return null;

	return (
		<section
			className="mt-4 h-[540px] min-w-0 overflow-hidden rounded-[5px] border border-[var(--line)] bg-[var(--surface)] max-[800px]:h-auto"
			aria-label={publicationMessage(locale, "inspect")}
		>
			<div className="grid h-full grid-cols-[minmax(190px,260px)_minmax(0,1fr)] max-[800px]:h-auto max-[800px]:grid-cols-1">
				<div className="min-w-0 border-r border-[var(--line)] bg-[var(--raised)] max-[800px]:border-r-0">
					<h2 className="m-0 border-b border-[var(--line)] px-3 py-2 text-sm font-semibold">
						{publicationMessage(locale, "inspect")}
					</h2>
					<ol className="m-0 h-[calc(540px-40px)] list-none overflow-y-auto p-1.5 max-[800px]:h-[320px]">
						{items.map((item) => {
							const isSelected = item.id === selected?.id;
							return (
								<li key={item.id} className="min-w-0">
									<div
										className={`grid h-11 grid-cols-[minmax(0,1fr)_auto] items-center gap-1 rounded-sm px-1 text-xs ${isSelected ? "bg-[var(--accent-soft)]" : ""}`}
									>
										<button
											data-position-id={item.id}
											type="button"
											aria-pressed={isSelected}
											className="grid min-w-0 grid-rows-2 items-center rounded-sm px-1 text-left text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
											onClick={() => {
												setSelectedID(item.id);
												if (window.matchMedia?.("(max-width: 800px)").matches)
													setDialogOpen(true);
											}}
										>
											<span className="min-w-0 truncate">
												{item.position}. {item.title}
											</span>
											<span
												className="truncate text-[10px] leading-3 text-[var(--muted)]"
												title={item.context}
											>
												{item.context ? `${item.context} · ` : ""}
												{item.action}
											</span>
										</button>
										{item.onInclude ? (
											<Checkbox
												aria-label={item.includeLabel}
												isSelected={Boolean(item.included)}
												isDisabled={item.includeDisabled}
												onChange={(included) => item.onInclude?.(included)}
											>
												<span className="checkbox" />
												{count(item)}
											</Checkbox>
										) : (
											<span className="tabular-nums text-[10px] text-[var(--muted)]">
												{count(item)}
											</span>
										)}
									</div>
								</li>
							);
						})}
					</ol>
				</div>
				<div className="flex min-h-0 min-w-0 flex-col p-3 max-[800px]:hidden">
					<InspectorBody item={selected} locale={locale} />
				</div>
			</div>
			<Modal isOpen={dialogOpen} onOpenChange={setDialogOpen} isDismissable>
				<Dialog
					aria-labelledby="publication-inspector-dialog-title"
					className="grid max-h-[85dvh] gap-2 overflow-y-auto"
				>
					<Heading slot="title" id="publication-inspector-dialog-title">
						{publicationMessage(locale, "inspect")}
					</Heading>
					<InspectorBody item={selected} locale={locale} />
					<Button onPress={() => setDialogOpen(false)}>
						{publicationMessage(locale, "close")}
					</Button>
				</Dialog>
			</Modal>
		</section>
	);
}
