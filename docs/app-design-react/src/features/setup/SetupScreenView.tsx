import type { ComponentProps } from "react";
import { GitHubTokenFormView } from "../tools/GitHubTokenFormView";
import { SetupDirectoryStepBody } from "./SetupDirectoryFields";
import { SetupGenreCatalogView } from "./SetupGenreCatalogView";
import { SetupAcoustIDView, SetupProvidersView } from "./SetupProvidersView";
import { SetupPublicationOptions } from "./SetupPublicationOptions";
import { SetupSummaryView } from "./SetupSummaryView";
import { SetupToolCardsView } from "./SetupToolCardsView";
import { SetupToolConfirmationView } from "./SetupToolConfirmationView";
import { SetupWizard } from "./SetupWizard";

export type SetupScreenWizardView = {
	step: number;
	error?: string;
	notice?: string;
	nextDisabled: boolean;
	onBack: () => void;
	onNext: () => void;
};

export type SetupScreenToolsStepView = {
	description: string;
	cards: ComponentProps<typeof SetupToolCardsView>;
	token: ComponentProps<typeof GitHubTokenFormView>;
};

export type SetupScreenPublicationStepView = {
	directory: ComponentProps<typeof SetupDirectoryStepBody>;
	options: ComponentProps<typeof SetupPublicationOptions>;
	providers: Omit<ComponentProps<typeof SetupProvidersView>, "acoustID">;
	acoustID?: ComponentProps<typeof SetupAcoustIDView>;
	genres: ComponentProps<typeof SetupGenreCatalogView>;
};

export type SetupScreenViewProps = {
	locale: "en" | "ru";
	wizard: SetupScreenWizardView;
	directories: ComponentProps<typeof SetupDirectoryStepBody>;
	tools: SetupScreenToolsStepView;
	publication: SetupScreenPublicationStepView;
	summary: ComponentProps<typeof SetupSummaryView>;
	confirmation?: ComponentProps<typeof SetupToolConfirmationView>;
};

/**
 * Pure Setup screen composition. Step selection, validation, drafts, tool
 * conflicts and directory checks live in the demo provider; this view only
 * renders the supplied models and leaf views in the established order.
 */
export function SetupScreenView({
	locale,
	wizard,
	directories,
	tools,
	publication,
	summary,
	confirmation,
}: SetupScreenViewProps) {
	return (
		<>
			<SetupWizard
				locale={locale}
				step={wizard.step}
				error={wizard.error}
				notice={wizard.notice}
				onBack={wizard.onBack}
				onNext={wizard.onNext}
				nextDisabled={wizard.nextDisabled}
			>
				{wizard.step === 0 && <SetupDirectoryStepBody {...directories} />}
				{wizard.step === 1 && (
					<>
						<p>{tools.description}</p>
						<SetupToolCardsView {...tools.cards} />
						<GitHubTokenFormView {...tools.token} />
					</>
				)}
				{wizard.step === 2 && (
					<>
						<SetupDirectoryStepBody {...publication.directory} />
						<SetupPublicationOptions {...publication.options} />
						<SetupProvidersView
							{...publication.providers}
							acoustID={
								publication.acoustID ? (
									<SetupAcoustIDView {...publication.acoustID} />
								) : undefined
							}
						/>
						<SetupGenreCatalogView {...publication.genres} />
					</>
				)}
				{wizard.step === 3 && <SetupSummaryView {...summary} />}
			</SetupWizard>
			{confirmation && <SetupToolConfirmationView {...confirmation} />}
		</>
	);
}
