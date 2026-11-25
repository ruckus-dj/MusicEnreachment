import { createContext, type ReactNode, useContext } from "react";
import { ManualSearchView } from "./ManualSearchView";
import type { ManualSearchViewProps } from "./manualSearchContract";

/** Search presentation data; the portable shell supplies the locale. */
export type ManualSearchModel = Omit<ManualSearchViewProps, "locale">;

/**
 * Search data, or a host binder that mounts its controller around the portable
 * view. The binder supplies a module-level controller element; this outlet owns
 * the child, so a host cannot substitute its own presentation here.
 */
export type ManualSearchSource =
  | ManualSearchModel
  | { bind: (children: ReactNode) => ReactNode };

const ManualSearchModelContext = createContext<ManualSearchModel | null>(null);

/** Host adapter: binds search data without rendering the portable view. */
export function ManualSearchDataBinder({
  model,
  children,
}: {
  model: ManualSearchModel;
  children: ReactNode;
}) {
  return (
    <ManualSearchModelContext.Provider value={model}>
      {children}
    </ManualSearchModelContext.Provider>
  );
}

function BoundManualSearchView({
  locale,
}: {
  locale: ManualSearchViewProps["locale"];
}) {
  const model = useContext(ManualSearchModelContext);
  if (!model) throw new Error("missing manual search view model");
  return <ManualSearchView {...model} locale={locale} />;
}

export function ManualSearchOutlet({
  source,
  locale,
}: {
  source: ManualSearchSource;
  locale: ManualSearchViewProps["locale"];
}) {
  const view = <BoundManualSearchView locale={locale} />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return <ManualSearchDataBinder model={source}>{view}</ManualSearchDataBinder>;
}
