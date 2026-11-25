import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import { SourceArtifactCleanupView } from "./SourceArtifactCleanupView";

export type SourceCleanupModel = ComponentProps<
  typeof SourceArtifactCleanupView
>;

/** Cleanup data, or a host component that binds its own hook to this view. */
export type SourceCleanupSource =
  | SourceCleanupModel
  | { Component: ComponentType<{ children: ReactNode }> };

const CleanupModelContext = createContext<SourceCleanupModel | null>(null);

/** Host adapter: binds cleanup data without rendering the portable view. */
export function SourceCleanupDataBinder({
  model,
  children,
}: {
  model: SourceCleanupModel;
  children: ReactNode;
}) {
  return (
    <CleanupModelContext.Provider value={model}>
      {children}
    </CleanupModelContext.Provider>
  );
}

function BoundCleanupView() {
  const model = useContext(CleanupModelContext);
  if (!model) throw new Error("missing source cleanup view model");
  return <SourceArtifactCleanupView {...model} />;
}

export function SourceCleanupOutlet({
  source,
}: {
  source: SourceCleanupSource;
}) {
  if ("Component" in source) {
    const { Component } = source;
    return (
      <Component>
        <BoundCleanupView />
      </Component>
    );
  }
  return (
    <SourceCleanupDataBinder model={source}>
      <BoundCleanupView />
    </SourceCleanupDataBinder>
  );
}
