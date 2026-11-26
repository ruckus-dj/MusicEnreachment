import {
  IncomingCreationOutlet,
  type IncomingCreationSource,
} from "./IncomingCreationOutlet";
import {
  IncomingMatchingOutlet,
  type IncomingMatchingSource,
} from "./IncomingMatchingOutlet";
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

export interface IncomingWorkspaceViewProps<
  Props extends object = Record<never, never>,
> {
  creation?: IncomingCreationSource<Props> | null;
  list?: IncomingScreenViewProps | null;
  matching?: IncomingMatchingSource | null;
}

export function IncomingWorkspaceView<
  Props extends object = Record<never, never>,
>({ creation, list, matching }: IncomingWorkspaceViewProps<Props>) {
  return (
    <section className={incomingScreenClass}>
      {creation ? <IncomingCreationOutlet source={creation} /> : null}
      {list ? <IncomingScreenView {...list} /> : null}
      <IncomingMatchingOutlet source={matching} />
    </section>
  );
}
