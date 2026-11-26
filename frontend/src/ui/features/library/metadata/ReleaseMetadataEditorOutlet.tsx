import { createContext, type ReactNode, useContext } from "react";
import {
  ReleaseMetadataEditorView,
  type ReleaseMetadataEditorViewProps,
} from "./ReleaseMetadataEditorView";

/** Release metadata presentation data; the portable shell supplies the frame. */
export type ReleaseMetadataEditorModel = ReleaseMetadataEditorViewProps;

/**
 * Metadata data, or a host binder that mounts its controller around the
 * portable view. The binder supplies a module-level controller element; this
 * outlet owns the child, so a host cannot substitute its own presentation here.
 * The binder also receives the active-surface flag so a retained controller can
 * keep running its hooks while returning null to close its portals.
 */
export type ReleaseMetadataEditorSource =
  | ReleaseMetadataEditorModel
  | { bind: (children: ReactNode, visible: boolean) => ReactNode };

const ReleaseMetadataEditorModelContext =
  createContext<ReleaseMetadataEditorModel | null>(null);

/** Host adapter: binds metadata data without rendering the portable view. */
export function ReleaseMetadataEditorDataBinder({
  model,
  children,
}: {
  model: ReleaseMetadataEditorModel;
  children: ReactNode;
}) {
  return (
    <ReleaseMetadataEditorModelContext.Provider value={model}>
      {children}
    </ReleaseMetadataEditorModelContext.Provider>
  );
}

function BoundReleaseMetadataEditorView() {
  const model = useContext(ReleaseMetadataEditorModelContext);
  if (!model) throw new Error("missing release metadata editor view model");
  return <ReleaseMetadataEditorView {...model} />;
}

export function ReleaseMetadataEditorOutlet({
  source,
  visible = true,
}: {
  source: ReleaseMetadataEditorSource;
  visible?: boolean;
}) {
  const view = <BoundReleaseMetadataEditorView />;
  if ("bind" in source) return <>{source.bind(view, visible)}</>;
  if (!visible) return null;
  return (
    <ReleaseMetadataEditorDataBinder model={source}>
      {view}
    </ReleaseMetadataEditorDataBinder>
  );
}
