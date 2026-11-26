import { useEffect, useRef, useState } from "react";
import {
  Button,
  type Key,
  Menu,
  MenuItem,
  MenuTrigger,
  Popover,
} from "react-aria-components";

/** One secondary action in a row overflow menu. */
export type RowActionMenuItem = {
  id: string;
  label: string;
  /** Navigation action; renders a link item instead of an action item. */
  href?: string;
  /**
   * Receives the persistent menu trigger, not the menu item that unmounts when
   * the menu closes. Use it as the invoker for a confirmation dialog so focus
   * can return to the row afterwards.
   */
  onAction?: (trigger: HTMLElement) => void;
  isDisabled?: boolean;
  isPending?: boolean;
  isDestructive?: boolean;
};

const menuPopoverClass =
  "react-aria-Popover z-[80] min-w-[11rem] max-w-[min(22rem,calc(100vw-1rem))] rounded-xl border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_12px_32px_rgb(0_0_0/0.14)]";

const menuItemClass =
  "flex min-h-9 cursor-pointer items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-[var(--ink)] outline-none data-[focused]:bg-[var(--raised)] data-[disabled]:cursor-not-allowed data-[disabled]:text-[var(--muted)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-[-2px]";

const destructiveItemClass =
  "text-[var(--error)] data-[focused]:bg-[var(--error-soft)] data-[disabled]:text-[var(--muted)]";

const triggerClass =
  "react-aria-Button inline-flex cursor-pointer items-center justify-center rounded-[var(--control-radius)] border border-transparent bg-transparent p-0 text-[var(--muted)] hover:border-[var(--line)] hover:bg-[var(--raised)] hover:text-[var(--ink)] data-[pressed]:bg-[var(--raised)] disabled:cursor-not-allowed disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2 pointer-coarse:size-10 pointer-coarse:min-h-10 pointer-coarse:min-w-10";

const triggerSizeClass = {
  compact: "size-8 min-h-8 min-w-8",
  default: "size-9 min-h-9 min-w-9",
} as const;

function EllipsisIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 16 16"
      className="size-4"
    >
      <circle cx="3.25" cy="8" r="1.15" fill="currentColor" />
      <circle cx="8" cy="8" r="1.15" fill="currentColor" />
      <circle cx="12.75" cy="8" r="1.15" fill="currentColor" />
    </svg>
  );
}

export type RowActionMenuProps = {
  /** Accessible name for the trigger and the menu. */
  label: string;
  actions: readonly RowActionMenuItem[];
  /** Compact matches table rows; default matches ordinary 36 px controls. */
  size?: keyof typeof triggerSizeClass;
  className?: string;
};

/**
 * Overflow trigger for a row's secondary actions.
 *
 * The menu closes before an action callback runs so a confirmation dialog is
 * never opened underneath a retained popover. Callbacks receive the trigger
 * element, which stays mounted while the confirmation is open.
 */
export function RowActionMenu({
  label,
  actions,
  size = "compact",
  className,
}: RowActionMenuProps) {
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [isOpen, setOpen] = useState(false);
  const [pendingActionId, setPendingActionId] = useState<string | null>(null);

  useEffect(() => {
    if (isOpen || pendingActionId === null) return;
    const action = actions.find((item) => item.id === pendingActionId);
    setPendingActionId(null);
    if (!action || action.href || action.isDisabled || action.isPending) return;
    const trigger = triggerRef.current;
    if (trigger) {
      // Enter the confirmation with the persistent row control as the invoker
      // so React Aria restores focus there when the dialog closes.
      trigger.focus({ preventScroll: true });
      action.onAction?.(trigger);
    }
  }, [actions, isOpen, pendingActionId]);

  if (actions.length === 0) return null;

  function select(key: Key) {
    const action = actions.find((item) => item.id === String(key));
    if (!action) return;
    setOpen(false);
    if (action.href) return;
    setPendingActionId(action.id);
  }

  return (
    <MenuTrigger isOpen={isOpen} onOpenChange={setOpen}>
      <Button
        ref={triggerRef}
        aria-label={label}
        className={
          className
            ? `${triggerClass} ${triggerSizeClass[size]} ${className}`
            : `${triggerClass} ${triggerSizeClass[size]}`
        }
      >
        <EllipsisIcon />
      </Button>
      <Popover placement="bottom end" className={menuPopoverClass}>
        <Menu aria-label={label} className="outline-none" onAction={select}>
          {actions.map((action) => (
            <MenuItem
              key={action.id}
              id={action.id}
              href={action.href}
              textValue={action.label}
              isDisabled={action.isDisabled || action.isPending}
              aria-busy={action.isPending || undefined}
              className={`${menuItemClass}${action.isDestructive ? ` ${destructiveItemClass}` : ""}`}
            >
              <span className="min-w-0 wrap-anywhere">{action.label}</span>
            </MenuItem>
          ))}
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}
