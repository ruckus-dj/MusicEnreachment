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

type Props<T extends ElementType> = ComponentProps<T> & { className?: string };
const withClass = (base: string) => (className?: string) =>
	[className, base].filter(Boolean).join(" ");

export function Button({ className, ...props }: Props<typeof AriaButton>) {
	return (
		<AriaButton
			{...props}
			className={withClass("react-aria-Button ui-button")(className)}
		/>
	);
}
export function Link({ className, ...props }: Props<typeof AriaLink>) {
	return (
		<AriaLink
			{...props}
			className={withClass("react-aria-Link ui-link")(className)}
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
			className={withClass("react-aria-TextField ui-field")(className)}
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
			className={withClass("react-aria-SearchField ui-search")(className)}
		/>
	);
}
export function Label({ className, ...props }: Props<typeof AriaLabel>) {
	return (
		<AriaLabel
			{...props}
			className={withClass("react-aria-Label ui-label")(className)}
		/>
	);
}
export function Input({ className, ...props }: Props<typeof AriaInput>) {
	return (
		<AriaInput
			{...props}
			className={withClass("react-aria-Input ui-input")(className)}
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
			className={withClass("react-aria-FieldError ui-field-error")(className)}
		/>
	);
}
export function Select({ className, ...props }: Props<typeof AriaSelect>) {
	return (
		<AriaSelect
			{...props}
			className={withClass("react-aria-Select ui-select")(className)}
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
			className={withClass("react-aria-SelectValue ui-select-value")(className)}
		/>
	);
}
export function Popover({ className, ...props }: Props<typeof AriaPopover>) {
	return (
		<AriaPopover
			{...props}
			className={withClass("react-aria-Popover ui-popover")(className)}
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
			className={withClass("react-aria-ListBox ui-listbox")(className)}
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
			className={withClass("react-aria-ListBoxItem ui-listbox-item")(className)}
		/>
	);
}
export function Tabs({ className, ...props }: Props<typeof AriaTabs>) {
	return (
		<AriaTabs
			{...props}
			className={withClass("react-aria-Tabs ui-tabs")(className)}
		/>
	);
}
export function TabList({ className, ...props }: Props<typeof AriaTabList>) {
	return (
		<AriaTabList
			{...props}
			className={withClass("react-aria-TabList ui-tab-list")(className)}
		/>
	);
}
export function Tab({ className, ...props }: Props<typeof AriaTab>) {
	return (
		<AriaTab
			{...props}
			className={withClass("react-aria-Tab ui-tab")(className)}
		/>
	);
}
export function TabPanel({ className, ...props }: Props<typeof AriaTabPanel>) {
	return (
		<AriaTabPanel
			{...props}
			className={withClass("react-aria-TabPanel ui-tab-panel")(className)}
		/>
	);
}
export function Checkbox({ className, ...props }: Props<typeof AriaCheckbox>) {
	return (
		<AriaCheckbox
			{...props}
			className={withClass("react-aria-Checkbox ui-checkbox")(className)}
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
			className={withClass("react-aria-RadioGroup ui-radio-group")(className)}
		/>
	);
}
export function Radio({ className, ...props }: Props<typeof AriaRadio>) {
	return (
		<AriaRadio
			{...props}
			className={withClass("react-aria-Radio ui-radio")(className)}
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
			className={withClass("react-aria-DisclosureGroup ui-disclosure-group")(
				className,
			)}
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
			className={withClass("react-aria-Disclosure ui-disclosure")(className)}
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
			className={withClass("react-aria-DisclosurePanel ui-disclosure-panel")(
				className,
			)}
		/>
	);
}
export function Toolbar({ className, ...props }: Props<typeof AriaToolbar>) {
	return (
		<AriaToolbar
			{...props}
			className={withClass("react-aria-Toolbar ui-toolbar")(className)}
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
			className={withClass("react-aria-Tooltip ui-tooltip")(className)}
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
			className={withClass("react-aria-ModalOverlay ui-modal-overlay")(
				className,
			)}
		/>
	);
}
export function Modal({ className, ...props }: Props<typeof AriaModal>) {
	return (
		<AriaModal
			{...props}
			className={withClass("react-aria-Modal ui-modal")(className)}
		/>
	);
}
export function Dialog({ className, ...props }: Props<typeof AriaDialog>) {
	return (
		<AriaDialog
			{...props}
			className={withClass("react-aria-Dialog ui-dialog")(className)}
		/>
	);
}
export { DialogTrigger } from "react-aria-components";
export function Panel({ className, ...props }: HTMLAttributes<HTMLElement>) {
	return <section {...props} className={withClass("ui-panel")(className)} />;
}
export function PageHeader({
	className,
	...props
}: HTMLAttributes<HTMLElement>) {
	return (
		<header {...props} className={withClass("ui-page-header")(className)} />
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
			className={withClass(`ui-status${tone ? ` ${tone}` : ""}`)(className)}
		/>
	);
}
export function Heading({ className, ...props }: Props<typeof AriaHeading>) {
	return (
		<AriaHeading
			{...props}
			className={withClass("react-aria-Heading ui-heading")(className)}
		/>
	);
}
export function Table({ className, ...props }: Props<typeof AriaTable>) {
	return (
		<AriaTable
			{...props}
			className={withClass("react-aria-Table ui-table")(className)}
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
			className={withClass("react-aria-TableHeader ui-table-header")(className)}
		/>
	);
}
export function Column({ className, ...props }: Props<typeof AriaColumn>) {
	return (
		<AriaColumn
			{...props}
			className={withClass("react-aria-Column ui-column")(className)}
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
			className={withClass("react-aria-TableBody ui-table-body")(className)}
		/>
	);
}
export function Row({ className, ...props }: Props<typeof AriaRow>) {
	return (
		<AriaRow
			{...props}
			className={withClass("react-aria-Row ui-row")(className)}
		/>
	);
}
export function Cell({ className, ...props }: Props<typeof AriaCell>) {
	return (
		<AriaCell
			{...props}
			className={withClass("react-aria-Cell ui-cell")(className)}
		/>
	);
}
