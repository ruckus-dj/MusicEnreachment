import { createContext, type ReactNode, useContext } from "react";
import { ManualReleasePreparationView } from "./ManualReleasePreparationView";
import type { ManualReleasePreparationViewProps } from "./manualReleasePreparationContract";

/** Preparation presentation data; the portable shell supplies the locale. */
export type ManualReleasePreparationModel = Omit<
  ManualReleasePreparationViewProps,
  "locale"
>;

/**
 * Preparation data, or a host binder that mounts its controller around the
 * portable view. The binder supplies a module-level controller element; this
 * outlet owns the child, so a host cannot substitute its own presentation here.
 */
export type ManualReleasePreparationSource =
  | ManualReleasePreparationModel
  | { bind: (children: ReactNode) => ReactNode };

const PreparationModelContext =
  createContext<ManualReleasePreparationModel | null>(null);

/** Host adapter: binds preparation data without rendering the portable view. */
export function ManualReleasePreparationDataBinder({
  model,
  children,
}: {
  model: ManualReleasePreparationModel;
  children: ReactNode;
}) {
  return (
    <PreparationModelContext.Provider value={model}>
      {children}
    </PreparationModelContext.Provider>
  );
}

function BoundManualReleasePreparationView({
  locale,
}: {
  locale: ManualReleasePreparationViewProps["locale"];
}) {
  const model = useContext(PreparationModelContext);
  if (!model) throw new Error("missing manual release preparation view model");
  return <ManualReleasePreparationView {...model} locale={locale} />;
}

export function ManualReleasePreparationOutlet({
  source,
  locale,
}: {
  source: ManualReleasePreparationSource;
  locale: ManualReleasePreparationViewProps["locale"];
}) {
  const view = <BoundManualReleasePreparationView locale={locale} />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <ManualReleasePreparationDataBinder model={source}>
      {view}
    </ManualReleasePreparationDataBinder>
  );
}
