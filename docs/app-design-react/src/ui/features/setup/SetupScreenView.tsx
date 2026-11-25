import type { ComponentProps, ReactNode, RefObject } from "react";
import { AppButton } from "../../components/AppButton";
import { GitHubTokenFormView } from "../tools/GitHubTokenFormView";
import { SetupDirectoryStepBody } from "./SetupDirectoryFields";
import { SetupFeedbackView } from "./SetupFeedbackView";
import { SetupGenreCatalogView } from "./SetupGenreCatalogView";
import { SetupAcoustIDView, SetupProvidersView } from "./SetupProvidersView";
import { SetupPublicationOptions } from "./SetupPublicationOptions";
import { SetupSummaryView } from "./SetupSummaryView";
import { SetupToolCardsView } from "./SetupToolCardsView";
import { SetupToolConfirmationView } from "./SetupToolConfirmationView";
import {
  SetupPlatformStep,
  type SetupPlatformStepProps,
  SetupWizard,
} from "./SetupWizard";

export type SetupScreenWizardView = {
  step: number;
  help?: string;
  error?: string;
  notice?: string;
  pending?: boolean;
  nextDisabled: boolean;
  backDisabled?: boolean;
  onBack: () => void;
  onNext: () => void;
};

export type SetupScreenCompletedOperationView = {
  key: string;
  text: string;
};

export type SetupScreenToolsStepView = {
  description?: string;
  refresh?: {
    label: string;
    disabled?: boolean;
    onPress: () => void;
  };
  cards?: ComponentProps<typeof SetupToolCardsView>;
  confirmation?: ComponentProps<typeof SetupToolConfirmationView>;
  completed?: readonly SetupScreenCompletedOperationView[];
  /** Mounted operation followers; the controller owns their lifecycle. */
  operations?: ReactNode;
  error?: {
    text: string;
    errorRef?: RefObject<HTMLParagraphElement | null>;
  };
  operationError?: {
    text: string;
    errorRef?: RefObject<HTMLParagraphElement | null>;
    retryLabel: string;
    discovering?: boolean;
    onRetry: () => void;
  };
  loadingOperations?: string;
  requestPending?: string;
  /** Demo provider form model; mounted controllers may pass `tokenMounted`. */
  token?: ComponentProps<typeof GitHubTokenFormView>;
  tokenMounted?: ReactNode;
};

export type SetupScreenMetadataStepView = {
  providers: Omit<ComponentProps<typeof SetupProvidersView>, "acoustID">;
  /** Demo provider form model; mounted controllers may pass `acoustIDMounted`. */
  acoustID?: ComponentProps<typeof SetupAcoustIDView>;
  acoustIDMounted?: ReactNode;
  error?: {
    text: string;
    errorRef?: RefObject<HTMLParagraphElement | null>;
  };
};

export type SetupScreenPublicationStepView = {
  directory: ComponentProps<typeof SetupDirectoryStepBody>;
  options: ComponentProps<typeof SetupPublicationOptions>;
  genres: ComponentProps<typeof SetupGenreCatalogView>;
  /** Demo provider form model; mounted controllers may pass `metadata`. */
  providers?: Omit<ComponentProps<typeof SetupProvidersView>, "acoustID">;
  acoustID?: ComponentProps<typeof SetupAcoustIDView>;
  /** Mounted metadata controller; replaces the pure metadata body. */
  metadata?: ReactNode;
};

export type SetupScreenSummaryStepView = ComponentProps<
  typeof SetupSummaryView
>;

export type SetupScreenViewProps = {
  locale: "en" | "ru";
  wizard: SetupScreenWizardView;
  platform?: SetupPlatformStepProps;
  directories: ComponentProps<typeof SetupDirectoryStepBody>;
  tools?: SetupScreenToolsStepView;
  /** Mounted tools controller; replaces the pure tools body. */
  toolsMounted?: ReactNode;
  publication: SetupScreenPublicationStepView;
  summary?: SetupScreenSummaryStepView;
  /** Mounted summary controller; replaces the pure summary body. */
  summaryMounted?: ReactNode;
  summaryRecheck?: {
    label: string;
    disabled?: boolean;
    onPress: () => void;
  };
};

/** Pure tools step content shared by the demo provider and the app controller. */
export function SetupToolsStepBody({
  description,
  refresh,
  cards,
  confirmation,
  completed,
  operations,
  error,
  operationError,
  loadingOperations,
  requestPending,
  token,
  tokenMounted,
}: SetupScreenToolsStepView) {
  return (
    <div>
      {description && <p>{description}</p>}
      {refresh && (
        <AppButton isDisabled={refresh.disabled} onPress={refresh.onPress}>
          {refresh.label}
        </AppButton>
      )}
      {cards && <SetupToolCardsView {...cards} />}
      {confirmation && <SetupToolConfirmationView {...confirmation} />}
      {completed?.map((item) => (
        <p
          key={item.key}
          role="status"
          className="wrap-anywhere bg-[var(--accent-soft)] p-2 my-2"
        >
          {item.text}
        </p>
      ))}
      {operations}
      {error && (
        <SetupFeedbackView error={error.text} errorRef={error.errorRef} />
      )}
      {operationError && (
        <div>
          <SetupFeedbackView
            error={operationError.text}
            errorRef={operationError.errorRef}
          />
          <AppButton
            isDisabled={operationError.discovering}
            onPress={operationError.onRetry}
          >
            {operationError.retryLabel}
          </AppButton>
        </div>
      )}
      <SetupFeedbackView pending={loadingOperations ?? null} />
      <SetupFeedbackView pending={requestPending ?? null} />
      {tokenMounted ?? (token ? <GitHubTokenFormView {...token} /> : null)}
    </div>
  );
}

/** Pure provider forms shared by the demo provider and the app controller. */
export function SetupMetadataStepBody({
  providers,
  acoustID,
  acoustIDMounted,
  error,
}: SetupScreenMetadataStepView) {
  return (
    <>
      <SetupProvidersView
        {...providers}
        acoustID={
          acoustIDMounted ??
          (acoustID ? <SetupAcoustIDView {...acoustID} /> : undefined)
        }
      />
      {error && (
        <p role="alert" ref={error.errorRef} tabIndex={-1}>
          {error.text}
        </p>
      )}
    </>
  );
}

/** Pure publication step content shared by both trees. */
export function SetupPublicationStepBody({
  directory,
  options,
  genres,
  providers,
  acoustID,
  metadata,
}: SetupScreenPublicationStepView) {
  return (
    <div className="grid gap-3">
      <SetupDirectoryStepBody {...directory} />
      <SetupPublicationOptions {...options} />
      {metadata ??
        (providers && (
          <SetupMetadataStepBody providers={providers} acoustID={acoustID} />
        ))}
      <SetupGenreCatalogView {...genres} />
    </div>
  );
}

/** Pure summary content shared by the demo provider and the app controller. */
export function SetupSummaryStepBody({
  locale,
  status,
  values,
}: SetupScreenSummaryStepView) {
  return <SetupSummaryView locale={locale} status={status} values={values} />;
}

/**
 * Pure Setup screen composition. Step selection, validation, drafts, tool
 * conflicts and directory checks live in the demo provider or the application
 * controllers; this view renders the supplied models and mounted controllers
 * in the established order.
 */
export function SetupScreenView({
  locale,
  wizard,
  platform,
  directories,
  tools,
  toolsMounted,
  publication,
  summary,
  summaryMounted,
  summaryRecheck,
}: SetupScreenViewProps) {
  return (
    <SetupWizard
      locale={locale}
      step={wizard.step}
      help={wizard.help}
      error={wizard.error}
      notice={wizard.notice}
      pending={wizard.pending}
      onBack={wizard.onBack}
      onNext={wizard.onNext}
      nextDisabled={wizard.nextDisabled}
      backDisabled={wizard.backDisabled}
    >
      {wizard.step === 0 && (
        <>
          {platform && <SetupPlatformStep {...platform} />}
          <SetupDirectoryStepBody {...directories} />
        </>
      )}
      {wizard.step === 1 &&
        (toolsMounted ?? (tools ? <SetupToolsStepBody {...tools} /> : null))}
      {wizard.step === 2 && <SetupPublicationStepBody {...publication} />}
      {wizard.step === 3 && (
        <div>
          {summaryMounted ??
            (summary ? <SetupSummaryStepBody {...summary} /> : null)}
          {summaryRecheck && (
            <AppButton
              isDisabled={summaryRecheck.disabled}
              onPress={summaryRecheck.onPress}
            >
              {summaryRecheck.label}
            </AppButton>
          )}
        </div>
      )}
    </SetupWizard>
  );
}
