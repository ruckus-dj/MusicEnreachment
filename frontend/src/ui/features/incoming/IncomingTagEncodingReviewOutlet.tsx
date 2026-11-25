import { createContext, type ReactNode, useContext } from "react";
import {
  IncomingTagEncodingReviewView,
  type IncomingTagEncodingReviewViewProps,
} from "./IncomingTagEncodingReviewView";

/** Tag encoding presentation data; the portable shell supplies the locale. */
export type IncomingTagEncodingReviewModel = Omit<
  IncomingTagEncodingReviewViewProps,
  "locale"
>;

/**
 * Tag encoding data, or a host binder that mounts its controller around the
 * portable view. The binder supplies a module-level controller element; this
 * outlet owns the child, so a host cannot substitute its own presentation here.
 */
export type IncomingTagEncodingReviewSource =
  | IncomingTagEncodingReviewModel
  | { bind: (children: ReactNode) => ReactNode };

const EncodingModelContext =
  createContext<IncomingTagEncodingReviewModel | null>(null);

/** Host adapter: binds tag encoding data without rendering the portable view. */
export function IncomingTagEncodingReviewDataBinder({
  model,
  children,
}: {
  model: IncomingTagEncodingReviewModel;
  children: ReactNode;
}) {
  return (
    <EncodingModelContext.Provider value={model}>
      {children}
    </EncodingModelContext.Provider>
  );
}

function BoundIncomingTagEncodingReviewView({
  locale,
}: {
  locale: IncomingTagEncodingReviewViewProps["locale"];
}) {
  const model = useContext(EncodingModelContext);
  if (!model) throw new Error("missing incoming tag encoding view model");
  return <IncomingTagEncodingReviewView {...model} locale={locale} />;
}

export function IncomingTagEncodingReviewOutlet({
  source,
  locale,
}: {
  source: IncomingTagEncodingReviewSource;
  locale: IncomingTagEncodingReviewViewProps["locale"];
}) {
  const view = <BoundIncomingTagEncodingReviewView locale={locale} />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <IncomingTagEncodingReviewDataBinder model={source}>
      {view}
    </IncomingTagEncodingReviewDataBinder>
  );
}
