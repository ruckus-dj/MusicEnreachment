import type { ComponentProps } from "react";
import { MatchingReceiptPublicationFrame } from "../../../ui/features/matching/MatchingReceiptPublicationFrame";
import { MatchingReceiptPublicationOutcomeView } from "../../../ui/features/matching/MatchingReceiptPublicationOutcomeView";

export type MatchingReceiptPublicationOutcomeModel = Omit<
	ComponentProps<typeof MatchingReceiptPublicationOutcomeView>,
	"locale"
>;

export type MatchingReceiptPublicationReady = Omit<
	ComponentProps<typeof MatchingReceiptPublicationFrame>,
	"children"
> & { outcome: MatchingReceiptPublicationOutcomeModel | null };

export function MatchingReceiptPublicationReadyView({
	model,
}: {
	model: MatchingReceiptPublicationReady;
}) {
	const { outcome, ...frame } = model;
	return (
		<MatchingReceiptPublicationFrame {...frame}>
			{outcome ? (
				<MatchingReceiptPublicationOutcomeView
					locale={frame.locale}
					{...outcome}
				/>
			) : null}
		</MatchingReceiptPublicationFrame>
	);
}
