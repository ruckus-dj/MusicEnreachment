import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  type RefObject,
  useContext,
} from "react";
import { AppButton } from "../../components/AppButton";
import { GitHubTokenFormView } from "../tools/GitHubTokenFormView";
import {
  OperationProgressView,
  type OperationProgressViewProps,
} from "./OperationProgressView";
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
  operations?: readonly {
    id: string;
    source: SetupStepSource<OperationProgressViewProps, { id: string }>;
  }[];
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
  token?: ComponentProps<typeof GitHubTokenFormView>;
};

export type SetupScreenMetadataStepView = {
  providers: Omit<ComponentProps<typeof SetupProvidersView>, "acoustID">;
  acoustID?: SetupStepSource<
    ComponentProps<typeof SetupAcoustIDView>,
    SetupAcoustIDProviderProps
  >;
  error?: {
    text: string;
    errorRef?: RefObject<HTMLParagraphElement | null>;
  };
};

export type SetupAcoustIDProviderProps = {
  hasKey: boolean;
  onChanged: () => unknown;
  onBusyChange?: (busy: boolean) => void;
  disabled?: boolean;
};

export type SetupScreenPublicationStepView<
  MetadataP extends object = Record<string, never>,
> = {
  directory: ComponentProps<typeof SetupDirectoryStepBody>;
  options: ComponentProps<typeof SetupPublicationOptions>;
  genres: ComponentProps<typeof SetupGenreCatalogView>;
  metadata?: SetupStepSource<SetupScreenMetadataStepView, MetadataP>;
};

export type SetupScreenSummaryStepView = ComponentProps<
  typeof SetupSummaryView
>;

type SetupStepProvider<Props extends object> = {
  Component: ComponentType<Props & { children: ReactNode }>;
  props: Props;
  /** Remount identity for a fresh read on an existing mount boundary. */
  key?: string | number;
};

type SetupStepSource<Model, Props extends object> =
  | Model
  | SetupStepProvider<Props>;

export type SetupScreenViewProps<
  SummaryP extends object = Record<string, never>,
  ToolsP extends object = Record<string, never>,
  MetadataP extends object = Record<string, never>,
> = {
  locale: "en" | "ru";
  wizard: SetupScreenWizardView;
  platform?: SetupPlatformStepProps;
  directories: ComponentProps<typeof SetupDirectoryStepBody>;
  tools?: SetupStepSource<SetupScreenToolsStepView, ToolsP>;
  publication: SetupScreenPublicationStepView<MetadataP>;
  summary?: SetupStepSource<SetupScreenSummaryStepView, SummaryP>;
  summaryRecheck?: {
    label: string;
    disabled?: boolean;
    onPress: () => void;
  };
};

/** Pure tools step content shared by the demo provider and the app controller. */
function SetupToolsStepBody({
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
      {operations?.map(({ id, source }) => (
        <SetupOperationOutlet key={id} source={source} />
      ))}
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
      {token && <GitHubTokenFormView {...token} />}
    </div>
  );
}

/** Pure provider forms shared by the demo provider and the app controller. */
function SetupMetadataStepBody({
  providers,
  acoustID,
  error,
}: SetupScreenMetadataStepView) {
  return (
    <>
      <SetupProvidersView
        {...providers}
        acoustID={
          acoustID ? <SetupAcoustIDOutlet source={acoustID} /> : undefined
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
export function SetupPublicationStepBody<
  MetadataP extends object = Record<string, never>,
>({
  directory,
  options,
  genres,
  metadata,
}: SetupScreenPublicationStepView<MetadataP>) {
  return (
    <div className="grid gap-3">
      <SetupDirectoryStepBody {...directory} />
      <SetupPublicationOptions {...options} />
      {metadata && <SetupMetadataOutlet source={metadata} />}
      <SetupGenreCatalogView {...genres} />
    </div>
  );
}

function isStepProvider<Model extends object, Props extends object>(
  source: SetupStepSource<Model, Props>,
): source is SetupStepProvider<Props> {
  return "Component" in source;
}

function createStepOutlet<Model extends object>(
  name: string,
  View: ComponentType<Model>,
) {
  const ModelContext = createContext<Model | null>(null);

  function DataBinder({
    model,
    children,
  }: {
    model: Model;
    children: ReactNode;
  }) {
    return (
      <ModelContext.Provider value={model}>{children}</ModelContext.Provider>
    );
  }

  function BoundView() {
    const model = useContext(ModelContext);
    if (!model) throw new Error(`missing ${name} view model`);
    return <View {...model} />;
  }

  function Outlet<Props extends object>({
    source,
  }: {
    source: SetupStepSource<Model, Props>;
  }) {
    if (isStepProvider(source)) {
      const { Component, props, key } = source;
      return (
        <Component key={key} {...props}>
          <BoundView />
        </Component>
      );
    }
    return (
      <DataBinder model={source}>
        <BoundView />
      </DataBinder>
    );
  }

  return { DataBinder, Outlet };
}

const { DataBinder: SetupToolsDataBinder, Outlet: SetupToolsOutlet } =
  createStepOutlet("setup tools", SetupToolsStepBody);
const { DataBinder: SetupSummaryDataBinder, Outlet: SetupSummaryOutlet } =
  createStepOutlet("setup summary", SetupSummaryView);
const { DataBinder: SetupMetadataDataBinder, Outlet: SetupMetadataOutlet } =
  createStepOutlet("setup metadata", SetupMetadataStepBody);
const { DataBinder: SetupAcoustIDDataBinder, Outlet: SetupAcoustIDOutlet } =
  createStepOutlet("setup AcoustID", SetupAcoustIDView);
const { DataBinder: SetupOperationDataBinder, Outlet: SetupOperationOutlet } =
  createStepOutlet("setup operation", OperationProgressView);

export {
  SetupAcoustIDDataBinder,
  SetupMetadataDataBinder,
  SetupOperationDataBinder,
  SetupSummaryDataBinder,
  SetupToolsDataBinder,
};

/**
 * Pure Setup screen composition. Step selection, validation, drafts, tool
 * conflicts and directory checks live in the demo provider or the application
 * controllers; this view renders the supplied models and mounted controllers
 * in the established order.
 */
export function SetupScreenView<
  SummaryP extends object = Record<string, never>,
  ToolsP extends object = Record<string, never>,
  MetadataP extends object = Record<string, never>,
>({
  locale,
  wizard,
  platform,
  directories,
  tools,
  publication,
  summary,
  summaryRecheck,
}: SetupScreenViewProps<SummaryP, ToolsP, MetadataP>) {
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
      {wizard.step === 1 && tools && <SetupToolsOutlet source={tools} />}
      {wizard.step === 2 && <SetupPublicationStepBody {...publication} />}
      {wizard.step === 3 && (
        <div>
          {summary && <SetupSummaryOutlet source={summary} />}
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
