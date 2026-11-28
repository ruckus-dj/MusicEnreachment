import { createContext, type ReactNode, useContext } from "react";
import {
  TrackMetadataEditorsView,
  type TrackMetadataEditorsViewProps,
} from "./TrackMetadataEditorsView";

/** Recording/position editor group presentation data. */
export type TrackMetadataEditorsModel = Omit<
  TrackMetadataEditorsViewProps,
  "recordingSupplement" | "positionSupplement"
>;

/**
 * Editor-group data, or a host binder that mounts its controller around the
 * portable group. A host may bind the whole group when it owns one controller
 * for both editors; this outlet still owns the group presentation.
 */
export type TrackMetadataEditorsSource =
  | TrackMetadataEditorsModel
  | { bind: (children: ReactNode) => ReactNode };

const TrackMetadataEditorsModelContext =
  createContext<TrackMetadataEditorsModel | null>(null);

/** Host adapter: binds editor-group data without rendering the portable view. */
export function TrackMetadataEditorsDataBinder({
  model,
  children,
}: {
  model: TrackMetadataEditorsModel;
  children: ReactNode;
}) {
  return (
    <TrackMetadataEditorsModelContext.Provider value={model}>
      {children}
    </TrackMetadataEditorsModelContext.Provider>
  );
}

type ViewOptions = Pick<
  TrackMetadataEditorsViewProps,
  "locale" | "recordingSupplement" | "positionSupplement"
>;

function BoundTrackMetadataEditorsView({
  locale,
  ...supplements
}: ViewOptions) {
  const model = useContext(TrackMetadataEditorsModelContext);
  if (!model) throw new Error("missing track metadata editors view model");
  return (
    <TrackMetadataEditorsView
      {...model}
      {...supplements}
      locale={locale ?? model.locale}
    />
  );
}

export function TrackMetadataEditorsOutlet({
  source,
  ...options
}: ViewOptions & { source: TrackMetadataEditorsSource }) {
  const view = <BoundTrackMetadataEditorsView {...options} />;
  if ("bind" in source) return <>{source.bind(view)}</>;
  return (
    <TrackMetadataEditorsDataBinder model={source}>
      {view}
    </TrackMetadataEditorsDataBinder>
  );
}
