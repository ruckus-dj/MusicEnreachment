import type { ComponentProps, ElementType, HTMLAttributes } from "react";
import {
	Button as AriaButton,
	Cell as AriaCell,
	Checkbox as AriaCheckbox,
	Column as AriaColumn,
	Dialog as AriaDialog,
	Disclosure as AriaDisclosure,
	DisclosureGroup as AriaDisclosureGroup,
	DisclosurePanel as AriaDisclosurePanel,
	FieldError as AriaFieldError,
	Heading as AriaHeading,
	Input as AriaInput,
	Label as AriaLabel,
	Link as AriaLink,
	ListBox as AriaListBox,
	ListBoxItem as AriaListBoxItem,
	Menu as AriaMenu,
	MenuItem as AriaMenuItem,
	MenuTrigger as AriaMenuTrigger,
	Modal as AriaModal,
	ModalOverlay as AriaModalOverlay,
	Popover as AriaPopover,
	Radio as AriaRadio,
	RadioGroup as AriaRadioGroup,
	Row as AriaRow,
	SearchField as AriaSearchField,
	Select as AriaSelect,
	SelectValue as AriaSelectValue,
	Tab as AriaTab,
	TabList as AriaTabList,
	Table as AriaTable,
	TableBody as AriaTableBody,
	TableHeader as AriaTableHeader,
	TabPanel as AriaTabPanel,
	Tabs as AriaTabs,
	TextField as AriaTextField,
	Toolbar as AriaToolbar,
	Tooltip as AriaTooltip,
	TooltipTrigger as AriaTooltipTrigger,
	type TableBodyProps,
} from "react-aria-components";

import { twMerge } from "tailwind-merge";

type Props<T extends ElementType> = ComponentProps<T> & { className?: string };
const withClass = (base: string) => (className?: string) =>
	twMerge(base, className);

export function Button({ className, ...props }: Props<typeof AriaButton>) {
	return (
		<AriaButton
			{...props}
			className={withClass(
				"react-aria-Button ui-button inline-flex min-h-[30px] pointer-coarse:min-h-[40px] cursor-pointer items-center justify-center gap-1.5 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-[var(--ink)] no-underline motion-safe:transition-[background-color,border-color,color] motion-safe:duration-[120ms] motion-reduce:transition-none hover:border-[var(--accent)] hover:bg-[var(--raised)] active:bg-[var(--accent-soft)] aria-disabled:cursor-not-allowed aria-disabled:border-[var(--line)] aria-disabled:bg-[var(--raised)] aria-disabled:text-[var(--muted)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
			)(className)}
		/>
	);
}
export function Link({ className, ...props }: Props<typeof AriaLink>) {
	return (
		<AriaLink
			{...props}
			className={withClass(
				"react-aria-Link ui-link text-[var(--accent)] no-underline decoration-1 underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
			)(className)}
		/>
	);
}
export function TextField({
	className,
	...props
}: Props<typeof AriaTextField>) {
	return (
		<AriaTextField
			{...props}
			className={withClass("react-aria-TextField ui-field min-w-0")(className)}
		/>
	);
}
export function SearchField({
	className,
	...props
}: Props<typeof AriaSearchField>) {
	return (
		<AriaSearchField
			{...props}
			className={withClass(
				"react-aria-SearchField ui-search flex min-w-0 items-center gap-1 [&_.react-aria-Input]:flex-1 [&_.react-aria-Button]:min-h-[30px] [&_.react-aria-Button]:px-2 [&_.react-aria-Button]:py-1",
			)(className)}
		/>
	);
}
export function Label({ className, ...props }: Props<typeof AriaLabel>) {
	return (
		<AriaLabel
			{...props}
			className={withClass(
				"react-aria-Label ui-label mb-1 block text-[11px] font-semibold text-[var(--muted)]",
			)(className)}
		/>
	);
}
export function Input({ className, ...props }: Props<typeof AriaInput>) {
	return (
		<AriaInput
			{...props}
			className={withClass(
				"react-aria-Input ui-input min-h-8 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-[5px] text-[var(--ink)] aria-[invalid=true]:border-[var(--error)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
			)(className)}
		/>
	);
}
export function FieldError({
	className,
	...props
}: Props<typeof AriaFieldError>) {
	return (
		<AriaFieldError
			{...props}
			className={withClass(
				"react-aria-FieldError ui-field-error mt-1 block text-[11px] text-[var(--error)]",
			)(className)}
		/>
	);
}
export function Select({ className, ...props }: Props<typeof AriaSelect>) {
	return (
		<AriaSelect
			{...props}
			className={withClass(
				"react-aria-Select ui-select relative min-w-0 [&>button]:flex [&>button]:min-h-[30px] [&>button]:w-full [&>button]:items-center [&>button]:justify-between [&>button]:gap-1.5 [&>button]:rounded-[var(--control-radius)] [&>button]:border [&>button]:border-[var(--control)] [&>button]:bg-[var(--surface)] [&>button]:px-[9px] [&>button]:py-[5px] [&>button]:text-left [&>button]:text-[var(--ink)] [&>button]:hover:border-[var(--accent)] [&>button]:hover:bg-[var(--raised)]",
			)(className)}
		/>
	);
}
export function SelectValue({
	className,
	...props
}: Props<typeof AriaSelectValue>) {
	return (
		<AriaSelectValue
			{...props}
			className={withClass(
				"react-aria-SelectValue ui-select-value overflow-hidden text-ellipsis whitespace-nowrap",
			)(className)}
		/>
	);
}
export function Popover({ className, ...props }: Props<typeof AriaPopover>) {
	return (
		<AriaPopover
			{...props}
			className={withClass(
				"react-aria-Popover ui-popover z-20 max-h-[280px] min-w-[var(--trigger-width)] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_24px_#0002]",
			)(className)}
		/>
	);
}
export function MenuTrigger(props: Props<typeof AriaMenuTrigger>) {
	return <AriaMenuTrigger {...props} />;
}
export function Menu({ className, ...props }: Props<typeof AriaMenu>) {
	return (
		<AriaMenu
			{...props}
			className={withClass("react-aria-Menu ui-menu")(className)}
		/>
	);
}
export function MenuItem({ className, ...props }: Props<typeof AriaMenuItem>) {
	return (
		<AriaMenuItem
			{...props}
			className={withClass("react-aria-MenuItem ui-menu-item")(className)}
		/>
	);
}
export function ListBox({ className, ...props }: Props<typeof AriaListBox>) {
	return (
		<AriaListBox
			{...props}
			className={withClass(
				"react-aria-ListBox ui-listbox grid gap-0.5 outline-none",
			)(className)}
		/>
	);
}
export function ListBoxItem({
	className,
	...props
}: Props<typeof AriaListBoxItem>) {
	return (
		<AriaListBoxItem
			{...props}
			className={withClass(
				"react-aria-ListBoxItem ui-listbox-item cursor-pointer rounded-sm px-2 py-[7px] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent)] data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]",
			)(className)}
		/>
	);
}
export function Tabs({ className, ...props }: Props<typeof AriaTabs>) {
	return (
		<AriaTabs
			{...props}
			className={withClass("react-aria-Tabs ui-tabs min-w-0")(className)}
		/>
	);
}
export function TabList({ className, ...props }: Props<typeof AriaTabList>) {
	return (
		<AriaTabList
			{...props}
			className={withClass(
				"react-aria-TabList ui-tab-list flex min-w-0 flex-wrap items-center gap-[3px] border-b border-[var(--line)]",
			)(className)}
		/>
	);
}
export function Tab({ className, ...props }: Props<typeof AriaTab>) {
	return (
		<AriaTab
			{...props}
			className={withClass(
				"react-aria-Tab ui-tab min-h-[34px] pointer-coarse:min-h-[40px] cursor-pointer border-0 border-b-2 border-b-transparent bg-transparent px-[10px] py-1.5 text-[var(--muted)] hover:bg-[var(--raised)] hover:text-[var(--ink)] data-[selected]:border-b-[var(--accent)] data-[selected]:font-semibold data-[selected]:text-[var(--accent)]",
			)(className)}
		/>
	);
}
export function TabPanel({ className, ...props }: Props<typeof AriaTabPanel>) {
	return (
		<AriaTabPanel
			{...props}
			className={withClass(
				"react-aria-TabPanel ui-tab-panel focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
			)(className)}
		/>
	);
}
export function Checkbox({ className, ...props }: Props<typeof AriaCheckbox>) {
	return (
		<AriaCheckbox
			{...props}
			className={withClass(
				"react-aria-Checkbox ui-checkbox inline-flex min-h-8 pointer-coarse:min-h-[40px] cursor-pointer items-center gap-[7px] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_.checkbox]:grid [&_.checkbox]:size-4 [&_.checkbox]:place-items-center [&_.checkbox]:border [&_.checkbox]:border-[var(--control)] [&_.checkbox]:bg-[var(--surface)] [&_.checkbox]:text-[var(--button-text)] data-[selected]:[&_.checkbox]:border-[var(--accent)] data-[selected]:[&_.checkbox]:bg-[var(--accent)]",
			)(className)}
		/>
	);
}
export function RadioGroup({
	className,
	...props
}: Props<typeof AriaRadioGroup>) {
	return (
		<AriaRadioGroup
			{...props}
			className={withClass("react-aria-RadioGroup ui-radio-group grid gap-0.5")(
				className,
			)}
		/>
	);
}
export function Radio({ className, ...props }: Props<typeof AriaRadio>) {
	return (
		<AriaRadio
			{...props}
			className={withClass(
				"react-aria-Radio ui-radio inline-flex min-h-8 pointer-coarse:min-h-[40px] cursor-pointer items-center gap-[7px] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_.indicator]:size-4 [&_.indicator]:shrink-0 [&_.indicator]:rounded-full [&_.indicator]:border [&_.indicator]:border-[var(--control)] [&_.indicator]:bg-[var(--surface)] data-[selected]:[&_.indicator]:border-[5px] data-[selected]:[&_.indicator]:border-[var(--accent)]",
			)(className)}
		/>
	);
}
export function DisclosureGroup({
	className,
	...props
}: Props<typeof AriaDisclosureGroup>) {
	return (
		<AriaDisclosureGroup
			{...props}
			className={withClass(
				"react-aria-DisclosureGroup ui-disclosure-group min-w-0",
			)(className)}
		/>
	);
}
export function Disclosure({
	className,
	...props
}: Props<typeof AriaDisclosure>) {
	return (
		<AriaDisclosure
			{...props}
			className={withClass(
				"react-aria-Disclosure ui-disclosure border-b border-[var(--line)] py-[7px] [&_[slot=trigger]]:border-0 [&_[slot=trigger]]:bg-transparent [&_[slot=trigger]]:py-1 [&_[slot=trigger]]:text-[var(--accent)] [&_p]:my-1.5 [&_p]:text-[var(--muted)]",
			)(className)}
		/>
	);
}
export function DisclosurePanel({
	className,
	...props
}: Props<typeof AriaDisclosurePanel>) {
	return (
		<AriaDisclosurePanel
			{...props}
			className={withClass(
				"react-aria-DisclosurePanel ui-disclosure-panel min-w-0",
			)(className)}
		/>
	);
}
export function Toolbar({ className, ...props }: Props<typeof AriaToolbar>) {
	return (
		<AriaToolbar
			{...props}
			className={withClass(
				"react-aria-Toolbar ui-toolbar flex flex-wrap items-center gap-[7px]",
			)(className)}
		/>
	);
}
export function TooltipTrigger(props: Props<typeof AriaTooltipTrigger>) {
	return <AriaTooltipTrigger {...props} />;
}
export function Tooltip({ className, ...props }: Props<typeof AriaTooltip>) {
	return (
		<AriaTooltip
			{...props}
			className={withClass(
				"react-aria-Tooltip ui-tooltip z-50 max-w-[260px] rounded-[5px] border border-[var(--line)] bg-[var(--ink)] px-2 py-1.5 text-[10px] text-[var(--surface)] shadow-[0_8px_24px_#0003]",
			)(className)}
		/>
	);
}
export function ModalOverlay({
	className,
	...props
}: Props<typeof AriaModalOverlay>) {
	return (
		<AriaModalOverlay
			{...props}
			className={withClass(
				"react-aria-ModalOverlay ui-modal-overlay fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[#0008] p-4",
			)(className)}
		/>
	);
}
type ModalProps = Props<typeof AriaModalOverlay> & Props<typeof AriaModal>;

export function Modal({
	className,
	children,
	isOpen,
	defaultOpen,
	onOpenChange,
	isDismissable,
	isKeyboardDismissDisabled,
	shouldCloseOnInteractOutside,
	...props
}: ModalProps) {
	return (
		<ModalOverlay
			isOpen={isOpen}
			defaultOpen={defaultOpen}
			onOpenChange={onOpenChange}
			isDismissable={isDismissable}
			isKeyboardDismissDisabled={isKeyboardDismissDisabled}
			shouldCloseOnInteractOutside={shouldCloseOnInteractOutside}
		>
			<AriaModal
				{...props}
				className={withClass(
					"react-aria-Modal ui-modal w-full max-w-[560px] rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-[18px] text-[var(--ink)] shadow-[0_16px_48px_#0003]",
				)(className)}
			>
				{children}
			</AriaModal>
		</ModalOverlay>
	);
}
export function Dialog({ className, ...props }: Props<typeof AriaDialog>) {
	return (
		<AriaDialog
			{...props}
			className={withClass(
				"react-aria-Dialog ui-dialog wrap-anywhere outline-none [&_[slot=title]]:mb-2 [&_[slot=title]]:mt-0 [&_[slot=title]]:text-base [&_p]:my-3 [&_p]:mb-[14px] [&_button]:mr-2 [&_button]:rounded-[var(--control-radius)] [&_button]:border [&_button]:border-[var(--control)] [&_button]:px-[10px] [&_button]:py-1.5",
			)(className)}
		/>
	);
}
export { DialogTrigger } from "react-aria-components";
export function Panel({ className, ...props }: HTMLAttributes<HTMLElement>) {
	return (
		<section
			{...props}
			className={withClass(
				"ui-panel mb-[10px] min-w-0 overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)]",
			)(className)}
		/>
	);
}
export function PageHeader({
	className,
	...props
}: HTMLAttributes<HTMLElement>) {
	return (
		<header
			{...props}
			className={withClass("ui-page-header min-w-0")(className)}
		/>
	);
}
export function StatusBadge({
	tone,
	className,
	...props
}: HTMLAttributes<HTMLSpanElement> & {
	tone?: "success" | "warning" | "error" | "info";
}) {
	return (
		<span
			{...props}
			className={withClass(
				`ui-status inline-flex w-max max-w-full items-center wrap-anywhere rounded-sm bg-[var(--raised)] px-1.5 py-0.5 text-[11px]/[1.4] text-[var(--muted)] ${tone === "success" ? "bg-[var(--success-soft)] text-[var(--success)]" : ""} ${tone === "warning" ? "bg-[var(--warning-soft)] text-[var(--warning)]" : ""} ${tone === "error" ? "bg-[var(--error-soft)] text-[var(--error)]" : ""} ${tone === "info" ? "bg-[var(--info-soft)] text-[var(--info)]" : ""}`,
			)(className)}
		/>
	);
}
export function Heading({ className, ...props }: Props<typeof AriaHeading>) {
	return (
		<AriaHeading
			{...props}
			className={withClass("react-aria-Heading ui-heading min-w-0")(className)}
		/>
	);
}
export function Table({ className, ...props }: Props<typeof AriaTable>) {
	return (
		<AriaTable
			{...props}
			className={withClass("react-aria-Table ui-table min-w-0")(className)}
		/>
	);
}
export function TableHeader({
	className,
	...props
}: Props<typeof AriaTableHeader>) {
	return (
		<AriaTableHeader
			{...props}
			className={withClass("react-aria-TableHeader ui-table-header min-w-0")(
				className,
			)}
		/>
	);
}
export function Column({ className, ...props }: Props<typeof AriaColumn>) {
	return (
		<AriaColumn
			{...props}
			className={withClass("react-aria-Column ui-column min-w-0")(className)}
		/>
	);
}
export function TableBody<T extends unknown>({
	className,
	...props
}: TableBodyProps<T> & { className?: string }) {
	return (
		<AriaTableBody<T>
			{...props}
			className={withClass("react-aria-TableBody ui-table-body min-w-0")(
				className,
			)}
		/>
	);
}
export function Row({ className, ...props }: Props<typeof AriaRow>) {
	return (
		<AriaRow
			{...props}
			className={withClass("react-aria-Row ui-row min-w-0")(className)}
		/>
	);
}
export function Cell({ className, ...props }: Props<typeof AriaCell>) {
	return (
		<AriaCell
			{...props}
			className={withClass("react-aria-Cell ui-cell min-w-0")(className)}
		/>
	);
}
