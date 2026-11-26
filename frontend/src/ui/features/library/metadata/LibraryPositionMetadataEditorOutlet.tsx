import { createContext, type ReactNode, useContext } from "react";
import {
  MetadataEditorView,
  type MetadataEditorViewProps,
} from "../MetadataEditorView";

/** Recording or position metadata editor presentation data. */
export type LibraryPositionMetadataEditorModel = MetadataEditorViewProps;

/**
 * Editor data, or a host binder that mounts its controller around the portable
 * view. The controller publishes the model and owns draft authority; this
 * outlet owns the view, so a host cannot substitute its own presentation.
 */
export type LibraryPositionMetadataEditorSource =
  | LibraryPositionMetadataEditorModel
  | { bind: (children: ReactNode) => ReactNode; key?: string };

const LibraryPositionMetadataEditorModelContext =
  createContext<LibraryPositionMetadataEditorModel | null>(null);

/** Host adapter: binds editor data without rendering the portable view. */
export function LibraryPositionMetadataEditorDataBinder({
  model,
  children,
}: {
  model: LibraryPositionMetadataEditorModel;
  children: ReactNode;
}) {
  return (
    <LibraryPositionMetadataEditorModelContext.Provider value={model}>
      {children}
    </LibraryPositionMetadataEditorModelContext.Provider>
  );
}

function BoundLibraryPositionMetadataEditorView() {
  const model = useContext(LibraryPositionMetadataEditorModelContext);
  if (!model)
    throw new Error("missing library position metadata editor view model");
  return <MetadataEditorView {...model} />;
}

export function LibraryPositionMetadataEditorOutlet({
  source,
}: {
  source: LibraryPositionMetadataEditorSource;
}) {
  const view = <BoundLibraryPositionMetadataEditorView />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <LibraryPositionMetadataEditorDataBinder model={source}>
      {view}
    </LibraryPositionMetadataEditorDataBinder>
  );
}
