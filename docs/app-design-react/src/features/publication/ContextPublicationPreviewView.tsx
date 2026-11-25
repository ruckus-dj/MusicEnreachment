import { Checkbox } from "react-aria-components";
import { firstPublicationPreviewMessages } from "../library/firstPublicationPreviewMessages";
import { LibraryPositionConfirmButton } from "../library/LibraryPositionWorkspace";
import {
	type PublicationPreviewDetailsState,
	PublicationPreviewDetailsView,
} from "../library/PublicationPreviewDetailsView";

export interface ContextPublicationPreviewViewProps {
	locale: "en" | "ru";
	error: string;
	readOnly: boolean;
	reason: string;
	notices: readonly string[];
	details?: PublicationPreviewDetailsState;
	sourceGroupName: string;
	downgrade: boolean;
	downgradeLabel: string;
	confirmDowngrade: boolean;
	canConfirm: boolean;
	hasCurrentPublication: boolean;
	onDowngradeChange: (checked: boolean) => void;
	onConfirm: () => void;
}

export function ContextPublicationPreviewView({
	locale,
	error,
	readOnly,
	reason,
	notices,
	details,
	sourceGroupName,
	downgrade,
	downgradeLabel,
	confirmDowngrade,
	canConfirm,
	hasCurrentPublication,
	onDowngradeChange,
	onConfirm,
}: ContextPublicationPreviewViewProps) {
	const copy = firstPublicationPreviewMessages[locale];

	return (
		<section className="min-w-0 space-y-3" aria-label={copy.section}>
			{error && (
				<p role="alert" className="text-[var(--error)]">
					{error}
				</p>
			)}
			{readOnly && <p role="note">{copy.comparisonReadOnly}</p>}
			<p>{[reason, ...notices].join(" · ")}</p>
			{details && (
				<PublicationPreviewDetailsView
					locale={locale}
					state={details}
					sourceGroupName={sourceGroupName}
				/>
			)}
			{!readOnly && (
				<>
					{downgrade && (
						<Checkbox
							className="react-aria-Checkbox ui-checkbox inline-flex min-h-8 pointer-coarse:min-h-[40px] cursor-pointer items-center gap-[7px] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_.checkbox]:grid [&_.checkbox]:size-4 [&_.checkbox]:place-items-center [&_.checkbox]:border [&_.checkbox]:border-[var(--control)] [&_.checkbox]:bg-[var(--surface)] [&_.checkbox]:text-[var(--button-text)] data-[selected]:[&_.checkbox]:border-[var(--accent)] data-[selected]:[&_.checkbox]:bg-[var(--accent)]"
							isSelected={confirmDowngrade}
							isDisabled={Boolean(error)}
							onChange={onDowngradeChange}
						>
							<span className="checkbox" />
							{downgradeLabel}
						</Checkbox>
					)}
					<LibraryPositionConfirmButton
						locale={locale}
						label={hasCurrentPublication ? "retainedRepublish" : "publish"}
						disabled={!canConfirm}
						onConfirm={onConfirm}
					/>
				</>
			)}
		</section>
	);
}
