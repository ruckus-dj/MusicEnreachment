import {
  type ComponentProps,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import { MatchingReceiptPublicationFrame } from "./MatchingReceiptPublicationFrame";

/** Frame facts; the portable caller supplies locale, release title and back. */
export type MatchingReceiptPublicationModel = Omit<
  ComponentProps<typeof MatchingReceiptPublicationFrame>,
  "locale" | "releaseTitle" | "onBack"
>;

/**
 * Publication facts, or a host binder that mounts its controllers and
 * followers around the portable frame. The binder supplies a module-level
 * bound element; this outlet owns the frame, so a host cannot substitute its
 * own presentation here.
 */
export type MatchingReceiptPublicationSource =
  | MatchingReceiptPublicationModel
  | { bind: (children: ReactNode) => ReactNode };

const MatchingReceiptPublicationModelContext =
  createContext<MatchingReceiptPublicationModel | null>(null);

/** Host adapter: binds publication data without rendering the portable frame. */
export function MatchingReceiptPublicationDataBinder({
  model,
  children,
}: {
  model: MatchingReceiptPublicationModel;
  children: ReactNode;
}) {
  return (
    <MatchingReceiptPublicationModelContext.Provider value={model}>
      {children}
    </MatchingReceiptPublicationModelContext.Provider>
  );
}

type FrameFacts = Pick<
  ComponentProps<typeof MatchingReceiptPublicationFrame>,
  "locale" | "releaseTitle" | "onBack"
>;

function BoundMatchingReceiptPublicationFrame({
  locale,
  releaseTitle,
  onBack,
}: FrameFacts) {
  const model = useContext(MatchingReceiptPublicationModelContext);
  if (!model)
    throw new Error("missing matching receipt publication frame model");
  return (
    <MatchingReceiptPublicationFrame
      {...model}
      locale={locale}
      releaseTitle={releaseTitle}
      onBack={onBack}
    />
  );
}

export function MatchingReceiptPublicationOutlet({
  source,
  locale,
  releaseTitle,
  onBack,
}: {
  source: MatchingReceiptPublicationSource;
} & FrameFacts) {
  const view = (
    <BoundMatchingReceiptPublicationFrame
      locale={locale}
      releaseTitle={releaseTitle}
      onBack={onBack}
    />
  );
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <MatchingReceiptPublicationDataBinder model={source}>
      {view}
    </MatchingReceiptPublicationDataBinder>
  );
}
