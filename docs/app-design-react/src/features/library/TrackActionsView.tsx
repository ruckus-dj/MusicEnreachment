import type { ReactElement } from "react";
import { objectActions } from "../../components/styles/matchingStyles";
import {
	Button,
	Dialog,
	Disclosure,
	DisclosureGroup,
	DisclosurePanel,
	Heading,
	Input,
	Label,
	Modal,
	TextField,
} from "../../components/ui";
import type { Locale } from "../../i18n/types";
import { operationMessage } from "../operations/operationMessages";

export type TrackActionsViewProps = {
	locale: Locale;
	notice: string;
	rename: {
		open: boolean;
		title: string;
		onOpenChange: (open: boolean) => void;
		onTitleChange: (title: string) => void;
		onStart: () => void;
		onSubmit: () => void;
	};
	onRefresh: () => void;
	onAudioUpgrade?: () => void;
	onRepublish?: () => void;
	deletion?: ReactElement;
};

export function TrackActionsView({
	locale,
	notice,
	rename,
	onRefresh,
	onAudioUpgrade,
	onRepublish,
	deletion,
}: TrackActionsViewProps) {
	const m = (key: Parameters<typeof operationMessage>[1]) =>
		operationMessage(locale, key);
	return (
		<>
			<DisclosureGroup>
				<Disclosure id="track-actions">
					<Button slot="trigger">{m("otherTrackActions")}</Button>
					<DisclosurePanel>
						<div className={objectActions}>
							<Button onPress={rename.onStart}>{m("editLocalTitle")}</Button>
							<Button onPress={onRefresh}>{m("refreshProviderCache")}</Button>
							{onAudioUpgrade && (
								<Button onPress={onAudioUpgrade}>{m("upgradeAudio")}</Button>
							)}
							{onRepublish && (
								<Button onPress={onRepublish}>{m("republishExplicit")}</Button>
							)}
						</div>
						{deletion}
					</DisclosurePanel>
				</Disclosure>
			</DisclosureGroup>
			{notice && <p role="status">{notice}</p>}
			<Modal
				isOpen={rename.open}
				onOpenChange={rename.onOpenChange}
				isDismissable
			>
				<Dialog aria-labelledby="track-action-title" className="grid gap-2.5">
					<form
						onSubmit={(event) => {
							event.preventDefault();
							rename.onSubmit();
						}}
					>
						<Heading slot="title" id="track-action-title">
							{m("editTagsOnly")}
						</Heading>
						<TextField
							aria-label={m("trackTitle")}
							value={rename.title}
							onChange={rename.onTitleChange}
							isRequired
						>
							<Label>{m("trackTitle")}</Label>
							<Input />
						</TextField>
						<p>{m("renamePreserves")}</p>
						<div className={objectActions}>
							<Button type="button" onPress={() => rename.onOpenChange(false)}>
								{m("cancelAction")}
							</Button>
							<Button type="submit">{m("confirmAction")}</Button>
						</div>
					</form>
				</Dialog>
			</Modal>
		</>
	);
}
