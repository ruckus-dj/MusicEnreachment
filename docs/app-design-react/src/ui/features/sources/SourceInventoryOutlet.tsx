import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import { SourceInventoryPanel } from "./SourceInventoryPanel";

export type SourceInventoryModel = Omit<
  ComponentProps<typeof SourceInventoryPanel>,
  "locale"
>;

/** Host-owned inventory controller props, opaque to this portable tree. */
type SourceInventoryHost<P extends object> = {
  Component: ComponentType<P & { children: ReactNode }>;
  props: P;
};

/** Inventory data, or a host component that binds its own controller here. */
export type SourceInventorySource<P extends object> =
  | SourceInventoryModel
  | SourceInventoryHost<P>;

const InventoryModelContext = createContext<SourceInventoryModel | null>(null);

/** Host adapter: binds inventory panel data without rendering the portable view. */
export function SourceInventoryDataBinder({
  model,
  children,
}: {
  model: SourceInventoryModel;
  children: ReactNode;
}) {
  return (
    <InventoryModelContext.Provider value={model}>
      {children}
    </InventoryModelContext.Provider>
  );
}

function BoundInventoryView({ locale }: { locale: "en" | "ru" }) {
  const model = useContext(InventoryModelContext);
  if (!model) throw new Error("missing source inventory view model");
  return <SourceInventoryPanel {...model} locale={locale} />;
}

// A host controller keeps its own hook mounted and rebindable: changing the
// opaque props updates this component in place instead of remounting it.
export function SourceInventoryOutlet<P extends object>({
  source,
  locale,
}: {
  source: SourceInventorySource<P>;
  locale: "en" | "ru";
}) {
  if ("Component" in source) {
    const { Component, props } = source;
    return (
      <Component {...props}>
        <BoundInventoryView locale={locale} />
      </Component>
    );
  }
  return (
    <SourceInventoryDataBinder model={source}>
      <BoundInventoryView locale={locale} />
    </SourceInventoryDataBinder>
  );
}
