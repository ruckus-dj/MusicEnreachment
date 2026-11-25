import {
	Button,
	Dialog,
	Heading,
	Modal,
	ModalOverlay,
} from "react-aria-components";
import type {
	LocalReleaseArtist,
	LocalReleaseRecording,
} from "./localReleaseFormModel";
import type { LocalReleaseCopy } from "./localReleaseMessages";
import { actions, muted, panel, primary, secondary } from "./localReleaseUi";

export function LocalReleaseCancelDialog({
	copy,
	isOpen,
	onOpenChange,
	onDiscard,
	savedRecordings,
	savedArtists,
}: {
	copy: LocalReleaseCopy;
	isOpen: boolean;
	onOpenChange: (open: boolean) => void;
	onDiscard: () => void;
	savedRecordings: readonly LocalReleaseRecording[];
	savedArtists: readonly LocalReleaseArtist[];
}) {
	return (
		<ModalOverlay
			isOpen={isOpen}
			onOpenChange={onOpenChange}
			className="fixed inset-0 z-50 grid place-items-center overflow-auto overscroll-contain bg-black/40 p-4"
		>
			<Modal className="w-full max-w-[420px]">
				<Dialog className={`${panel} grid gap-3 outline-none`}>
					<Heading slot="title" className="m-0 text-base font-semibold">
						{copy.cancelQuestion}
					</Heading>
					<p className="m-0 text-sm">{copy.cancelHelp}</p>
					{savedRecordings.length > 0 && (
						<p className={muted}>
							{copy.savedHelp}{" "}
							{savedRecordings.map((item) => item.title).join(", ")}.
						</p>
					)}
					{savedArtists.length > 0 && (
						<p className={muted}>
							{copy.savedArtistHelp}{" "}
							{savedArtists
								.map((item) => item.name?.trim() || item.id)
								.join(", ")}
							.
						</p>
					)}
					<div className={actions}>
						<Button className={primary} onPress={() => onOpenChange(false)}>
							{copy.keep}
						</Button>
						<Button className={secondary} onPress={onDiscard}>
							{copy.discard}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</ModalOverlay>
	);
}
