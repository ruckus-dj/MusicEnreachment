import { createContext, type ReactNode, useContext } from "react";
import {
  MatchingScreenView,
  type MatchingScreenViewModel,
} from "../matching/MatchingScreenView";

/**
 * Matching data, or a host binder that mounts its controller around the
 * portable view. The binder supplies a module-level controller element; this
 * outlet owns the child, so a host cannot substitute its own presentation here.
 */
export type IncomingMatchingSource =
  | MatchingScreenViewModel
  | { bind: (children: ReactNode) => ReactNode };

const MatchingModelContext = createContext<MatchingScreenViewModel | null>(
  null,
);

/** Host adapter: binds matching data without rendering the portable view. */
export function IncomingMatchingDataBinder({
  model,
  children,
}: {
  model: MatchingScreenViewModel;
  children: ReactNode;
}) {
  return (
    <MatchingModelContext.Provider value={model}>
      {children}
    </MatchingModelContext.Provider>
  );
}

function BoundMatchingScreenView() {
  const model = useContext(MatchingModelContext);
  if (!model) throw new Error("missing incoming matching view model");
  return <MatchingScreenView model={model} />;
}

/** Fixed matching slot; null keeps the slot without mounting a controller. */
export function IncomingMatchingOutlet({
  source,
}: {
  source?: IncomingMatchingSource | null;
}) {
  if (!source) return null;
  const view = <BoundMatchingScreenView />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <IncomingMatchingDataBinder model={source}>
      {view}
    </IncomingMatchingDataBinder>
  );
}
