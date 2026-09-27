import { Button, type ButtonProps } from "react-aria-components";

export function AppButton(props: ButtonProps) {
  return (
    <Button
      {...props}
      className="rounded border border-stone-400 px-3 py-1 text-sm hover:bg-stone-200 focus:outline-2 focus:outline-offset-2 focus:outline-stone-700"
    />
  );
}
