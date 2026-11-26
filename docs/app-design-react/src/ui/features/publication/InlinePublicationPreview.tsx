import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import type { Locale } from "../../i18n/types";
import { PublicationFlowView } from "./PublicationFlowView";

/**
 * Pure inline publication flow data owned by a host. The portable outlet owns
 * the locale, so a host never supplies arbitrary JSX as its payload.
 */
export type InlinePublicationPreviewModel = Omit<
  ComponentProps<typeof PublicationFlowView>,
  "locale"
>;

/**
 * Host-owned inline publication preview controller. It publishes the flow
 * model through {@link InlinePublicationPreviewDataBinder}; the portable tree
 * mounts it only where the inline preview slot is rendered. `key` is the
 * stable preview identity, so a different preview remounts the controller
 * while the same preview keeps its registered read state.
 */
export type InlinePublicationPreviewProvider<Props extends object> = {
  key: string;
  Component: ComponentType<Props & { children: ReactNode }>;
  props: Props;
};

/** Pure flow data, or a mounted host controller that supplies one. */
export type InlinePublicationPreviewSource<
  Props extends object = Record<never, never>,
> = InlinePublicationPreviewModel | InlinePublicationPreviewProvider<Props>;

const InlinePublicationPreviewContext =
  createContext<InlinePublicationPreviewModel | null>(null);

/** Host adapter: binds preview data without rendering the portable view. */
export function InlinePublicationPreviewDataBinder({
  model,
  children,
}: {
  model: InlinePublicationPreviewModel;
  children: ReactNode;
}) {
  return (
    <InlinePublicationPreviewContext.Provider value={model}>
      {children}
    </InlinePublicationPreviewContext.Provider>
  );
}

function BoundInlinePublicationPreview({ locale }: { locale: Locale }) {
  const model = useContext(InlinePublicationPreviewContext);
  if (!model) throw new Error("missing inline publication preview model");
  return <PublicationFlowView locale={locale} {...model} />;
}

/** The inline preview slot: pure flow data or a host-owned controller. */
export function InlinePublicationPreviewOutlet<
  Props extends object = Record<never, never>,
>({
  locale,
  source,
}: {
  locale: Locale;
  source: InlinePublicationPreviewSource<Props>;
}) {
  if ("Component" in source) {
    const { Component, key, props } = source;
    return (
      <Component key={key} {...props}>
        <BoundInlinePublicationPreview locale={locale} />
      </Component>
    );
  }
  return (
    <InlinePublicationPreviewDataBinder model={source}>
      <BoundInlinePublicationPreview locale={locale} />
    </InlinePublicationPreviewDataBinder>
  );
}
