import { useState } from "react";
import { DeletionRequest, type DeletionOperation } from "./DeletionRequest";
import {
	Button,
	Checkbox,
	Disclosure,
	DisclosureGroup,
	DisclosurePanel,
	Dialog,
	Heading,
	Input,
	Label,
	Modal,
	TextField,
} from "./ui";
import {
	originalFiles,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
	type CatalogArtist,
} from "./catalogData";
import {
	explicitlyRepublish,
	refreshProviderHint,
	renameLocalTrack,
	upgradeAudioOnly,
} from "./trackActionModel";
import { dialogFooter, objectActions } from "./matchingStyles";
import { useI18n } from "./i18n";
import { operationMessage as message } from "./operationMessages";

export function TrackActions({
	release,
	track,
	onMutate,
	onQueueDeletion,
	files = originalFiles,
	artists,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
	artists?: CatalogArtist[];
}) {
	const { locale } = useI18n();
	const [action, setAction] = useState<
		"rename" | "upgrade" | "republish" | null
	>(null);
	const [title, setTitle] = useState(track.title);
	const [downgrade, setDowngrade] = useState(false);
	const [notice, setNotice] = useState("");
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const downgradeRequired =
		track.published?.codec === "FLAC" && file?.codec === "MP3";
	const open = (next: typeof action) => {
		setTitle(track.title);
		setDowngrade(false);
		setNotice("");
		setAction(next);
	};
	return (
		<>
			<DisclosureGroup>
				<Disclosure id="track-actions">
					<Button slot="trigger">{message(locale, "otherTrackActions")}</Button>
					<DisclosurePanel>
						<div className={objectActions}>
							<Button onPress={() => open("rename")}>
								{message(locale, "editLocalTitle")}
							</Button>
							<Button
								onPress={() => {
									onMutate(refreshProviderHint);
									setNotice(message(locale, "providerCacheRefreshed"));
								}}
							>
								{message(locale, "refreshProviderCache")}
							</Button>
							{track.published?.codec === "MP3" && file?.codec === "FLAC" && (
								<Button onPress={() => open("upgrade")}>
									{message(locale, "upgradeAudio")}
								</Button>
							)}
							{track.published && (
								<Button
									onPress={() =>
										file
											? open("republish")
											: setNotice(message(locale, "chooseSource"))
									}
								>
									{message(locale, "republishExplicit")}
								</Button>
							)}
						</div>
						{track.published && onQueueDeletion && (
							<DeletionRequest
								release={release}
								track={track}
								onQueue={onQueueDeletion}
							/>
						)}
					</DisclosurePanel>
				</Disclosure>
			</DisclosureGroup>
			{notice && <p role="status">{notice}</p>}
			<Modal
				isOpen={action !== null}
				onOpenChange={(isOpen) => {
					if (!isOpen) setAction(null);
				}}
				isDismissable
			>
				<Dialog aria-labelledby="track-action-title" className="grid gap-2.5">
					{notice && <p role="alert">{notice}</p>}
					<form
						onSubmit={(event) => {
							event.preventDefault();
							if (action === "republish" && downgradeRequired && !downgrade) {
								setNotice(message(locale, "downgradeRequired"));
								document
									.querySelector<HTMLElement>(".track-downgrade input")
									?.focus();
								return;
							}
							if (action === "rename") {
								onMutate((current) => renameLocalTrack(current, title));
								setNotice(message(locale, "renamePreserves"));
							} else if (action === "upgrade") {
								onMutate((current) => upgradeAudioOnly(current, files));
								setNotice(message(locale, "audioUpgraded"));
							} else if (action === "republish") {
								onMutate((current) =>
									explicitlyRepublish(
										release,
										current,
										downgrade,
										files,
										artists,
									),
								);
								setNotice(message(locale, "republished"));
							}
							setAction(null);
						}}
					>
						<Heading slot="title" id="track-action-title">
							{action === "rename"
								? message(locale, "editTagsOnly")
								: action === "upgrade"
									? message(locale, "upgradeAudioTitle")
									: message(locale, "republishTitle")}
						</Heading>
						{action === "rename" ? (
							<>
								<TextField
									aria-label={message(locale, "trackTitle")}
									value={title}
									onChange={setTitle}
									isRequired
								>
									<Label>{message(locale, "trackTitle")}</Label>
									<Input />
								</TextField>
								<p>{message(locale, "renamePreserves")}</p>
							</>
						) : (
							<>
								<p>
									{message(locale, "sourceAndFormat", {
										title: track.title,
										path: file?.path ?? "",
										codec: file?.codec ?? "",
									})}
								</p>
								<p>
									{action === "upgrade"
										? message(locale, "publicationPreserves")
										: message(locale, "republishDescription")}
								</p>
							</>
						)}
						{action === "republish" && downgradeRequired && (
							<Checkbox
								isSelected={downgrade}
								onChange={setDowngrade}
								className="track-downgrade"
							>
								<span className="checkbox" aria-hidden="true">
									{downgrade ? "✓" : ""}
								</span>
								{message(locale, "confirmDowngrade")}
							</Checkbox>
						)}
						<div className={dialogFooter}>
							<Button type="button" onPress={() => setAction(null)}>
								{message(locale, "cancelAction")}
							</Button>
							<Button
								type="submit"
								className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							>
								{message(locale, "confirmAction")}
							</Button>
						</div>
					</form>
				</Dialog>
			</Modal>
		</>
	);
}
