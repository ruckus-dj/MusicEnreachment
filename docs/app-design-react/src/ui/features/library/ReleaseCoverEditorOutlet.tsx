import { createContext, type ReactNode, useContext } from "react";
import {
  ReleaseCoverEditorView,
  type ReleaseCoverEditorViewProps,
} from "./ReleaseCoverEditorView";

/** Release cover presentation data; the portable shell supplies the locale. */
export type ReleaseCoverEditorModel = Omit<
  ReleaseCoverEditorViewProps,
  "locale"
>;

/**
 * Cover data, or a host binder that mounts its controller around the portable
 * view. The binder supplies a module-level controller element; this outlet owns
 * the child, so a host cannot substitute its own presentation here.
 */
export type ReleaseCoverEditorSource =
  | ReleaseCoverEditorModel
  | { bind: (children: ReactNode) => ReactNode };

const ReleaseCoverEditorModelContext =
  createContext<ReleaseCoverEditorModel | null>(null);

/** Host adapter: binds cover data without rendering the portable view. */
export function ReleaseCoverEditorDataBinder({
  model,
  children,
}: {
  model: ReleaseCoverEditorModel;
  children: ReactNode;
}) {
  return (
    <ReleaseCoverEditorModelContext.Provider value={model}>
      {children}
    </ReleaseCoverEditorModelContext.Provider>
  );
}

function BoundReleaseCoverEditorView({
  locale,
}: {
  locale: ReleaseCoverEditorViewProps["locale"];
}) {
  const model = useContext(ReleaseCoverEditorModelContext);
  if (!model) throw new Error("missing release cover editor view model");
  return <ReleaseCoverEditorView {...model} locale={locale} />;
}

export function ReleaseCoverEditorOutlet({
  source,
  locale,
}: {
  source: ReleaseCoverEditorSource;
  locale: ReleaseCoverEditorViewProps["locale"];
}) {
  const view = <BoundReleaseCoverEditorView locale={locale} />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <ReleaseCoverEditorDataBinder model={source}>
      {view}
    </ReleaseCoverEditorDataBinder>
  );
}
