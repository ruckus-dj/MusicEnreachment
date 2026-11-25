import {
  MatchingScreenView,
  type MatchingScreenViewModel,
} from "../matching/MatchingScreenView";
import {
  IncomingCreationOutlet,
  type IncomingCreationSource,
} from "./IncomingCreationOutlet";
import {
  IncomingScreenView,
  type IncomingScreenViewProps,
} from "./IncomingScreenView";
import { incomingScreenClass } from "./incomingStyles";

export type {
  IncomingCreationEditorModel,
  IncomingCreationFallbackModel,
  IncomingCreationModel,
  IncomingCreationProvider,
  IncomingCreationSource,
} from "./IncomingCreationOutlet";

export function IncomingWorkspaceView<
  Props extends object = Record<never, never>,
>({
  creation,
  list,
  matching,
}: {
  creation?: IncomingCreationSource<Props> | null;
  list?: IncomingScreenViewProps | null;
  matching: MatchingScreenViewModel | null;
}) {
  return (
    <section className={incomingScreenClass}>
      {creation ? <IncomingCreationOutlet source={creation} /> : null}
      {list ? <IncomingScreenView {...list} /> : null}
      {matching ? <MatchingScreenView model={matching} /> : null}
    </section>
  );
}
