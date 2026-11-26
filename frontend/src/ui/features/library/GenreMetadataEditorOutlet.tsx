import { createContext, type ReactNode, useContext } from "react";
import {
  GenreMetadataEditorView,
  type GenreMetadataEditorViewProps,
} from "./GenreMetadataEditorView";

/** Genre metadata editor presentation data. */
export type GenreMetadataEditorModel = GenreMetadataEditorViewProps;

/**
 * Genre data, or a host binder that mounts its controller around the portable
 * view. The controller owns saved metadata, drafts and catalog requests; this
 * outlet owns the view, so a host cannot substitute its own presentation.
 */
export type GenreMetadataEditorSource =
  | GenreMetadataEditorModel
  | { bind: (children: ReactNode) => ReactNode };

const GenreMetadataEditorModelContext =
  createContext<GenreMetadataEditorModel | null>(null);

/** Host adapter: binds genre data without rendering the portable view. */
export function GenreMetadataEditorDataBinder({
  model,
  children,
}: {
  model: GenreMetadataEditorModel;
  children: ReactNode;
}) {
  return (
    <GenreMetadataEditorModelContext.Provider value={model}>
      {children}
    </GenreMetadataEditorModelContext.Provider>
  );
}

function BoundGenreMetadataEditorView() {
  const model = useContext(GenreMetadataEditorModelContext);
  if (!model) throw new Error("missing genre metadata editor view model");
  return <GenreMetadataEditorView {...model} />;
}

export function GenreMetadataEditorOutlet({
  source,
}: {
  source: GenreMetadataEditorSource;
}) {
  const view = <BoundGenreMetadataEditorView />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <GenreMetadataEditorDataBinder model={source}>
      {view}
    </GenreMetadataEditorDataBinder>
  );
}
