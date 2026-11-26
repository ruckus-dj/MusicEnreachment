import { type ReactNode, useRef } from "react";
import { navigateHash } from "../../../ui/app/hashLocation";
import {
	MeloTroveIncomingBinder,
	MeloTroveIncomingContent,
} from "../../../ui/app/MeloTroveProvider";
import type { IncomingCreationModel } from "../../../ui/features/incoming/IncomingCreationOutlet";
import {
	IncomingMatchingDataBinder,
	type IncomingMatchingSource,
} from "../../../ui/features/incoming/IncomingMatchingOutlet";
import { useMatchingScreenDemoProvider } from "../matching/useMatchingScreenDemoProvider";
import {
	type IncomingScreenProps,
	useIncomingScreenDemoProvider,
} from "./IncomingScreen";
import {
	type IncomingGroupsRoute,
	incomingGroupMatchingHref,
} from "./incomingGroupsRoute";

export type IncomingWorkspaceAnchorProps = {
	route: IncomingGroupsRoute | undefined;
	list: IncomingScreenProps;
	creation: IncomingCreationModel | null;
};

/** The exact matching route selector retained between matching visits. */
type MatchingSelector = {
	groupId: string;
	releaseId?: string;
	localReleaseId?: string;
	prepareReleaseId?: string;
	creatingLocalRelease?: boolean;
};

function selectorFromRoute(
	route: IncomingGroupsRoute,
): MatchingSelector | undefined {
	if (route.view !== "matching") return undefined;
	return {
		groupId: route.groupId,
		releaseId: route.releaseId,
		localReleaseId: route.localReleaseId,
		prepareReleaseId: route.prepareReleaseId,
		creatingLocalRelease: route.creatingLocalRelease,
	};
}

function MatchingScreenController({
	selector,
	list,
	hidden,
	children,
}: {
	selector: MatchingSelector;
	list: IncomingScreenProps;
	hidden: boolean;
	children: ReactNode;
}) {
	const matching = useMatchingScreenDemoProvider({
		destination: list.destination,
		state: list.state,
		onState: list.onState,
		files: list.files,
		providers: list.providers,
		artists: list.artists,
		releases: list.releases,
		onReleases: list.onReleases,
		publication: list.publication,
		groupId: selector.groupId,
		releaseId: selector.releaseId,
		localReleaseId: selector.localReleaseId,
		prepareReleaseId: selector.prepareReleaseId,
		createLocalReleaseHref: incomingGroupMatchingHref(
			selector.groupId,
			selector.releaseId,
			{
				localReleaseId: selector.localReleaseId,
				prepareReleaseId: selector.prepareReleaseId,
				creatingLocalRelease: true,
			},
		),
		onSelectProviderRelease: (releaseId?: string) => {
			navigateHash(incomingGroupMatchingHref(selector.groupId, releaseId));
		},
		onSelectLocalRelease: (localReleaseId: string) => {
			navigateHash(
				incomingGroupMatchingHref(selector.groupId, undefined, {
					localReleaseId,
				}),
			);
		},
		hidden: hidden || selector.creatingLocalRelease === true,
	});
	return (
		<IncomingMatchingDataBinder model={matching}>
			{children}
		</IncomingMatchingDataBinder>
	);
}

/**
 * One stable Incoming workspace for the list, matching and Local-release
 * creation routes. The list stays mounted in its slot. The matching controller
 * is mounted only after the first matching selection and then keeps only that
 * exact selector; its live arrays and callbacks come from the current list
 * props. Hidden expanded list rows collapse so file action portals unmount
 * while matching is active.
 */
export function DemoMeloTroveIncomingBinder({
	route,
	list,
	creation,
	children,
}: IncomingWorkspaceAnchorProps & { children: ReactNode }) {
	const listModel = useIncomingScreenDemoProvider(list);
	const listHidden = route?.view !== "list";
	const listView = listHidden
		? {
				...listModel,
				hidden: true,
				panel: listModel.panel
					? {
							...listModel.panel,
							rows: listModel.panel.rows.map((row) => ({
								...row,
								opened: false,
								files: [],
							})),
						}
					: undefined,
			}
		: listModel;
	const lastSelector = useRef<MatchingSelector | null>(null);
	const currentSelector = route ? selectorFromRoute(route) : undefined;
	if (currentSelector) lastSelector.current = currentSelector;
	const selector = lastSelector.current;
	const matchingSource: IncomingMatchingSource | null = selector
		? {
				bind: (children) => (
					<MatchingScreenController
						selector={selector}
						list={list}
						hidden={route?.view !== "matching"}
					>
						{children}
					</MatchingScreenController>
				),
			}
		: null;
	return (
		<MeloTroveIncomingBinder
			model={{ creation, list: listView, matching: matchingSource }}
		>
			{children}
		</MeloTroveIncomingBinder>
	);
}

/**
 * Module-level Incoming workspace for the prototype host. The demo provider
 * supplies data and the portable UI owns the composition; this facade
 * preserves existing imports and prop shape.
 */
export function IncomingWorkspaceAnchor(props: IncomingWorkspaceAnchorProps) {
	return (
		<DemoMeloTroveIncomingBinder {...props}>
			<MeloTroveIncomingContent />
		</DemoMeloTroveIncomingBinder>
	);
}
