import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import { ContextPublicationPreviewView } from "./ContextPublicationPreviewView";

/**
 * Pure publication preview panel model owned by a host controller. The model is
 * exactly the existing view contract, so a host never supplies arbitrary JSX
 * as its payload.
 */
export type ContextPublicationPreviewModel = ComponentProps<
  typeof ContextPublicationPreviewView
>;

/** Workflow modes stay owned by the view module; hosts import them here. */
export type {
  ContextPublicationPreviewMode,
  ContextPublicationPreviewWorkflow,
} from "./ContextPublicationPreviewView";

/**
 * Host-owned live publication preview controller. It publishes the model
 * through {@link ContextPublicationPreviewDataBinder}; the portable tree mounts
 * it only where the contextual preview slot is rendered. `key` is the stable
 * position identity, so a different position remounts the controller while the
 * same position keeps its registered controller and read state.
 */
export type ContextPublicationPreviewProvider<Props extends object> = {
  key: string;
  Component: ComponentType<Props & { children: ReactNode }>;
  props: Props;
};

/** Pure panel data, or a mounted host controller that supplies one. */
export type ContextPublicationPreviewSource<
  Props extends object = Record<never, never>,
> = ContextPublicationPreviewModel | ContextPublicationPreviewProvider<Props>;

const ContextPublicationPreviewContext =
  createContext<ContextPublicationPreviewModel | null>(null);

/** Host adapter: binds preview data without rendering the portable view. */
export function ContextPublicationPreviewDataBinder({
  model,
  children,
}: {
  model: ContextPublicationPreviewModel;
  children: ReactNode;
}) {
  return (
    <ContextPublicationPreviewContext.Provider value={model}>
      {children}
    </ContextPublicationPreviewContext.Provider>
  );
}

function BoundContextPublicationPreview() {
  const model = useContext(ContextPublicationPreviewContext);
  if (!model) throw new Error("missing context publication preview model");
  return <ContextPublicationPreviewView {...model} />;
}

/** The contextual preview slot: pure model data or a host-owned controller. */
export function ContextPublicationPreviewOutlet<
  Props extends object = Record<never, never>,
>({ source }: { source: ContextPublicationPreviewSource<Props> }) {
  if ("Component" in source) {
    const { Component, key, props } = source;
    return (
      <Component key={key} {...props}>
        <BoundContextPublicationPreview />
      </Component>
    );
  }
  return (
    <ContextPublicationPreviewDataBinder model={source}>
      <BoundContextPublicationPreview />
    </ContextPublicationPreviewDataBinder>
  );
}
