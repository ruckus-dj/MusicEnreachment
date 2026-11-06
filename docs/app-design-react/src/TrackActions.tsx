import { useState } from "react";
import { DeletionRequest, type DeletionOperation } from "./DeletionRequest";
import {
	Button,
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
} from "./catalogData";
import { refreshProviderHint, renameLocalTrack } from "./trackActionModel";
import { objectActions } from "./matchingStyles";
import { useI18n } from "./i18n";
import { operationMessage as message } from "./operationMessages";

export function TrackActions({
	release,
	track,
	onMutate,
	onQueueDeletion,
	files = originalFiles,
	onRepublish,
	onAudioUpgrade,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
	onRepublish: () => void;
	onAudioUpgrade?: () => void;
}) {
	const { locale } = useI18n();
	const [renaming, setRenaming] = useState(false);
	const [title, setTitle] = useState(track.title);
	const [notice, setNotice] = useState("");
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const open = () => {
		setTitle(track.title);
		setNotice("");
		setRenaming(true);
	};
	return (
		<>
			<DisclosureGroup>
				<Disclosure id="track-actions">
					<Button slot="trigger">{message(locale, "otherTrackActions")}</Button>
					<DisclosurePanel>
						<div className={objectActions}>
							<Button onPress={open}>
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
							{onAudioUpgrade &&
								track.published?.codec === "MP3" &&
								file?.codec === "FLAC" && (
									<Button onPress={onAudioUpgrade}>
										{message(locale, "upgradeAudio")}
									</Button>
								)}
							{track.published && (
								<Button
									onPress={() =>
										file
											? onRepublish()
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
			<Modal isOpen={renaming} onOpenChange={setRenaming} isDismissable>
				<Dialog aria-labelledby="track-action-title" className="grid gap-2.5">
					<form
						onSubmit={(event) => {
							event.preventDefault();
							onMutate((current) => renameLocalTrack(current, title));
							setNotice(message(locale, "renamePreserves"));
							setRenaming(false);
						}}
					>
						<Heading slot="title" id="track-action-title">
							{message(locale, "editTagsOnly")}
						</Heading>
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
						<div className={objectActions}>
							<Button type="button" onPress={() => setRenaming(false)}>
								{message(locale, "cancelAction")}
							</Button>
							<Button type="submit">{message(locale, "confirmAction")}</Button>
						</div>
					</form>
				</Dialog>
			</Modal>
		</>
	);
}
