import type { ReactNode } from "react";
import {
  MeloTroveSetupBinder,
  MeloTroveSetupContent,
} from "../../../ui/app/MeloTroveProvider";
import {
  type SetupScreenProps,
  useSetupScreenDemoProvider,
} from "./useSetupScreenDemoProvider";

export type { SetupScreenProps } from "./useSetupScreenDemoProvider";

/**
 * Supplies the demo Setup model without choosing the portable screen
 * composition.
 */
export function DemoMeloTroveSetupBinder({
  children,
  ...props
}: SetupScreenProps & { children: ReactNode }) {
  const model = useSetupScreenDemoProvider(props);
  return <MeloTroveSetupBinder model={model}>{children}</MeloTroveSetupBinder>;
}

/**
 * Public Setup entry point. The demo provider owns state and the portable UI
 * owns the markup; this facade preserves existing imports and prop shape.
 */
export function SetupScreen(props: SetupScreenProps) {
  return (
    <DemoMeloTroveSetupBinder {...props}>
      <MeloTroveSetupContent />
    </DemoMeloTroveSetupBinder>
  );
}
