import { useState, type ReactNode } from "react";
import { useI18n } from "./i18n";
import { Button, Checkbox, Dialog, Heading, Modal } from "./ui";
import {
	PublicationInspector,
	type PublicationInspection,
} from "./PublicationInspector";
import { dialogFooter, objectActions } from "./matchingStyles";
import {
	formatPublicationMessage,
	publicationMessage,
} from "./publicationMessages";

export function PublicationFlow({
	title,
	description,
	returnLabel,
	onBack,
	stale,
	error,
	counts,
	items,
	footer,
	primaryLabel,
	primaryDisabled,
	confirmationTitle,
	confirmationDetails,
	confirmationError,
	confirmLabel,
	onConfirm,
	downgradeCount = 0,
	downgradeConfirmed = false,
	onDowngradeConfirmed,
}: {
	title: string;
	description?: string;
	returnLabel: string;
	onBack: () => void;
	stale: boolean;
	error?: string;
	counts: { label: string; count?: number }[];
	items: PublicationInspection[];
	footer: ReactNode;
	primaryLabel: string;
	primaryDisabled: boolean;
	confirmationTitle: string;
	confirmationDetails: ReactNode;
	confirmationError?: string;
	confirmLabel: string;
	onConfirm: () => void;
	downgradeCount?: number;
	downgradeConfirmed?: boolean;
	onDowngradeConfirmed?: (confirmed: boolean) => void;
}) {
	const { locale } = useI18n();
	const [confirming, setConfirming] = useState(false);
	return (
		<section className="min-w-0 px-3 py-3" aria-label={title}>
			<header className="demo-object-header mb-2 flex flex-wrap items-center justify-between gap-2">
				<div className="min-w-0 [overflow-wrap:anywhere]">
					<h1 className="m-0 text-[20px]">{title}</h1>
					{description && (
						<p className="m-0 mt-1 text-xs text-[var(--muted)]">
							{description}
						</p>
					)}
				</div>
				<Button onPress={onBack}>{returnLabel}</Button>
			</header>
			{(stale || error) && (
				<p role="alert">
					{error || publicationMessage(locale, "inventoryChanged")}
				</p>
			)}
			<div
				className="demo-preview-counts flex flex-wrap gap-1.5 py-2"
				aria-label={publicationMessage(locale, "actionSummary")}
			>
				{counts.map(({ label, count }) => (
					<span
						key={label}
						className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-[11px]"
					>
						{label}
						{count === undefined ? "" : `: ${count}`}
					</span>
				))}
			</div>
			<PublicationInspector items={items} locale={locale} />
			<div className="mt-2 flex flex-wrap items-center justify-between gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--raised)] p-2">
				<div className="min-w-0 text-xs">{footer}</div>
				<div className={objectActions}>
					<Button
						className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
						isDisabled={stale || primaryDisabled}
						onPress={() => setConfirming(true)}
					>
						{primaryLabel}
					</Button>
					<Button onPress={onBack}>
						{publicationMessage(locale, "cancel")}
					</Button>
				</div>
			</div>
			{downgradeCount > 0 && onDowngradeConfirmed && (
				<Checkbox
					isSelected={downgradeConfirmed}
					onChange={onDowngradeConfirmed}
				>
					<span className="checkbox" />
					{formatPublicationMessage(locale, "confirmDowngrade", {
						count: downgradeCount,
					})}
				</Checkbox>
			)}
			<Modal isOpen={confirming} onOpenChange={setConfirming} isDismissable>
				<Dialog
					aria-labelledby="publication-flow-confirm-title"
					className="grid gap-2"
				>
					<Heading slot="title" id="publication-flow-confirm-title">
						{confirmationTitle}
					</Heading>
					<div className="grid gap-2 text-xs text-[var(--muted)]">
						{confirmationDetails}
					</div>
					{(stale || confirmationError) && (
						<p role="alert">
							{confirmationError ||
								publicationMessage(locale, "inventoryChanged")}
						</p>
					)}
					<div className={dialogFooter}>
						<Button onPress={() => setConfirming(false)}>
							{publicationMessage(locale, "cancel")}
						</Button>
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							isDisabled={stale || primaryDisabled}
							onPress={() => {
								onConfirm();
								setConfirming(false);
							}}
						>
							{confirmLabel}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</section>
	);
}
