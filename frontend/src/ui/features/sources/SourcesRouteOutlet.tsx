import {
  type ComponentProps,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import { SourceInspectorView } from "./SourceInspectorView";
import {
  SourceListPageView,
  type SourceListPageViewProps,
} from "./SourceListPageView";
import { MissingSourceView } from "./SourcePageFeedbackView";
import {
  SourceRootDetailView,
  type SourceRootDetailViewProps,
} from "./SourceRootDetailView";
import type { SourceInspectorViewProps } from "./sourceInspectorModel";

type MissingSourceViewProps = ComponentProps<typeof MissingSourceView>;

/**
 * One portable page for every Sources address. The host chooses the page and
 * its data; this tree decides which portable view renders it.
 */
export type SourcesPage<I extends object = Record<never, never>> =
  | ({ view: "list" } & SourceListPageViewProps)
  | ({ view: "detail"; detail: "root" } & SourceRootDetailViewProps<I>)
  | ({ view: "detail"; detail: "missing" } & MissingSourceViewProps)
  | ({ view: "location" } & SourceInspectorViewProps);

/**
 * Creates the paired binder and outlet for one host. Call once at module scope
 * so the context identity stays stable across controller renders.
 */
export function createSourcesRouteOutlet<
  I extends object = Record<never, never>,
>() {
  const SourcesPageContext = createContext<SourcesPage<I> | null>(null);

  function SourcesBinder({
    page,
    children,
  }: {
    page: SourcesPage<I>;
    children: ReactNode;
  }) {
    return (
      <SourcesPageContext.Provider value={page}>
        {children}
      </SourcesPageContext.Provider>
    );
  }

  function SourcesOutlet() {
    const page = useContext(SourcesPageContext);
    if (!page) throw new Error("missing sources page model");
    switch (page.view) {
      case "list":
        return <SourceListPageView {...page} />;
      case "detail":
        return page.detail === "root" ? (
          <SourceRootDetailView<I> {...page} />
        ) : (
          <MissingSourceView {...page} />
        );
      case "location":
        return <SourceInspectorView {...page} />;
    }
  }

  return { SourcesBinder, SourcesOutlet };
}
