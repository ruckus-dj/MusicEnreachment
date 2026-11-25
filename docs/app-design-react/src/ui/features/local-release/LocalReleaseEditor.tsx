import { type ComponentType, useEffect, useRef, useState } from "react";
import { Button } from "react-aria-components";
import { LocalReleaseCancelDialog } from "./LocalReleaseCancelDialog";
import { LocalReleaseForm } from "./LocalReleaseForm";
import type { LocalReleaseGenreCatalogProviderProps } from "./LocalReleaseGenreCatalogOutlet";
import type {
	FindLocalArtists,
	LocalRecordingDraft,
	LocalReleaseArtist,
	LocalReleaseDraft,
	LocalReleaseRecording,
	SaveLocalArtist,
} from "./localReleaseFormModel";
import { localReleaseMessages } from "./localReleaseMessages";
import {
	Field,
	LookupStatus,
	muted,
	panel,
	primary,
	row,
	rows,
	secondary,
	useLookup,
} from "./localReleaseUi";
import { useLocalReleaseForm } from "./useLocalReleaseForm";

export type ReleaseCheckHit = {
	id: string;
	title: string;
	artist: string;
	provider: string;
	details?: string;
};

export type LocalReleaseEditorProps = {
	locale: "ru" | "en";
	suggestedArtist: string;
	suggestedTitle: string;
	incomingLabel: string;
	artists: readonly LocalReleaseArtist[];
	recordings: readonly LocalReleaseRecording[];
	checkRelease: (
		artist: string,
		title: string,
		signal?: AbortSignal,
	) => Promise<readonly ReleaseCheckHit[]>;
	findRecordings: (
		query: string,
		signal?: AbortSignal,
	) => Promise<readonly LocalReleaseRecording[]>;
	saveRecording: (
		data: LocalRecordingDraft,
	) => LocalReleaseRecording | Promise<LocalReleaseRecording>;
	findArtists: FindLocalArtists;
	saveArtist: SaveLocalArtist;
	/** Stable host provider mounted with the edit form. */
	genreCatalogProvider: ComponentType<LocalReleaseGenreCatalogProviderProps>;
	onUseRelease: (id: string) => void;
	onSave: (data: LocalReleaseDraft) => void | Promise<void>;
	onCancel: () => void;
};

export function LocalReleaseEditor(props: LocalReleaseEditorProps) {
	const { locale, incomingLabel, onCancel } = props;
	const copy = localReleaseMessages[locale];
	const GenreCatalogProvider = props.genreCatalogProvider;
	const lookup = useLookup<ReleaseCheckHit>();
	const [step, setStep] = useState<"check" | "edit">("check");
	const [returningToCheck, setReturningToCheck] = useState(false);
	const [editionToUse, setEditionToUse] = useState<string>();
	const heading = useRef<HTMLHeadingElement>(null);
	const form = useLocalReleaseForm({
		initialValues: {
			artist: props.suggestedArtist,
			title: props.suggestedTitle,
		},
		onSave: props.onSave,
	});
	useEffect(() => heading.current?.focus(), []);

	return (
		<>
			<section className="mx-auto grid w-full min-w-0 max-w-4xl gap-3 py-3 text-sm">
				<header className="min-w-0 border-b border-[var(--line)] pb-3">
					<p className={muted}>
						{copy.from} · {incomingLabel}
					</p>
					<h1
						ref={heading}
						tabIndex={-1}
						className="my-1 text-xl font-semibold"
					>
						{copy.title}
					</h1>
				</header>
				{step === "check" ? (
					<section
						className={`${panel} grid gap-3`}
						aria-labelledby="local-release-check"
					>
						<h2
							id="local-release-check"
							className="m-0 text-base font-semibold"
						>
							{copy.checkStep}
						</h2>
						<p className={muted}>{copy.checkHelp}</p>
						<form
							aria-label={copy.checkStep}
							className="grid items-end gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]"
							onSubmit={(event) => {
								event.preventDefault();
								if (!form.title.trim() || lookup.result.kind === "pending")
									return;
								void lookup.search((signal) =>
									props.checkRelease(
										form.artist.trim(),
										form.title.trim(),
										signal,
									),
								);
							}}
						>
							<Field
								label={copy.artist}
								value={form.artist}
								onChange={(value) => {
									form.setArtist(value);
									lookup.reset();
								}}
							/>
							<Field
								label={copy.release}
								value={form.title}
								required
								onChange={(value) => {
									form.setTitle(value);
									lookup.reset();
								}}
							/>
							<Button
								className={primary}
								type="submit"
								isDisabled={
									!form.title.trim() || lookup.result.kind === "pending"
								}
							>
								{copy.check}
							</Button>
						</form>
						<LookupStatus
							result={lookup.result}
							copy={copy}
							empty={copy.noEditions}
						>
							{(hits) => (
								<>
									{hits.length > 0 && (
										<ul className={rows} aria-label={copy.editionResults}>
											{hits.map((hit) => (
												<li className={row} key={hit.id}>
													<div className="min-w-0 wrap-anywhere">
														<strong>{hit.title}</strong>
														<p className={muted}>
															{[hit.artist, hit.provider, hit.details]
																.filter(Boolean)
																.join(" · ")}
														</p>
													</div>
													<Button
														className={secondary}
														onPress={() => {
															if (returningToCheck && form.hasDraft()) {
																setEditionToUse(hit.id);
																form.setCancel(true);
															} else {
																props.onUseRelease(hit.id);
															}
														}}
													>
														{copy.useEdition}
													</Button>
												</li>
											))}
										</ul>
									)}
									<Button
										className={`${primary} w-fit`}
										onPress={() => setStep("edit")}
									>
										{copy.next}
									</Button>
								</>
							)}
						</LookupStatus>
						<Button
							className={`${secondary} w-fit`}
							onPress={() => {
								if (form.hasDraft()) form.setCancel(true);
								else onCancel();
							}}
						>
							{copy.cancel}
						</Button>
					</section>
				) : (
					<GenreCatalogProvider>
						<LocalReleaseForm
							locale={locale}
							form={form}
							heading={copy.editStep}
							titleReadOnly
							onRecheck={() => {
								setReturningToCheck(true);
								setStep("check");
							}}
							suggestedArtist={props.suggestedArtist}
							artists={props.artists}
							recordings={props.recordings}
							findRecordings={props.findRecordings}
							saveRecording={props.saveRecording}
							findArtists={props.findArtists}
							saveArtist={props.saveArtist}
						/>
					</GenreCatalogProvider>
				)}
			</section>
			<LocalReleaseCancelDialog
				copy={copy}
				isOpen={form.cancel}
				onOpenChange={(open) => {
					if (!open) setEditionToUse(undefined);
					form.setCancel(open);
				}}
				onDiscard={() => {
					if (editionToUse) props.onUseRelease(editionToUse);
					else onCancel();
				}}
				savedRecordings={form.savedRecordings}
				savedArtists={form.savedArtists}
			/>
		</>
	);
}
