import {
	type ReactNode,
	useEffect,
	useLayoutEffect,
	useRef,
	useState,
} from "react";
import { useI18n } from "../demoPreferences";
import { ManualReleasePreparationDataBinder } from "../../../ui/features/matching/ManualReleasePreparationOutlet";
import {
	type ManualReleasePreparationMessageKey,
	manualReleasePreparationMessage,
} from "../../../ui/features/matching/manualReleasePreparationContract";

export type MatchingReleasePreparationProps = {
	/** Explicit selection coming from the release search, mirroring `searchRelease`. */
	releaseId?: string | null;
	selectionKey?: string;
	disabled?: boolean;
	resolveRelease: (identity: string) => string | undefined;
	onPrepared: (releaseId: string) => void;
};

const canonicalMBID =
	/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

/** Mirrors the production MBID/URL normalization without touching the network. */
function canonicalReleaseId(value: string) {
	const segment =
		/^https:\/\/musicbrainz\.org\/release\/([^/]+)$/.exec(value)?.[1] ?? value;
	return canonicalMBID.test(segment) ? segment.toLowerCase() : undefined;
}

/**
 * Prototype-only provider for the shared preparation view. No MusicBrainz
 * request is made: only identities available in the fixture catalog resolve.
 */
export function MatchingReleasePreparation({
	releaseId,
	selectionKey = "",
	disabled = false,
	resolveRelease,
	onPrepared,
	children,
}: MatchingReleasePreparationProps & { children: ReactNode }) {
	const { locale } = useI18n();
	const [value, setValue] = useState(releaseId ?? "");
	const [pending, setPending] = useState(false);
	const [prepared, setPrepared] = useState(false);
	const [errorKey, setErrorKey] =
		useState<ManualReleasePreparationMessageKey>();
	const target = useRef<
		{ selectionKey: string; releaseId: typeof releaseId } | undefined
	>(undefined);

	useLayoutEffect(() => {
		target.current = { selectionKey, releaseId };
		return () => {
			target.current = undefined;
		};
	}, [selectionKey, releaseId]);

	useEffect(() => {
		if (!releaseId) return;
		setValue(releaseId);
		setPrepared(false);
		setErrorKey(undefined);
	}, [releaseId]);

	const message = (
		key: ManualReleasePreparationMessageKey,
		values?: Record<string, string | number>,
	) => manualReleasePreparationMessage(locale, key, values);

	const prepare = async () => {
		const requested = value.trim();
		const canonical = canonicalReleaseId(requested);
		const selected = releaseId && requested === releaseId ? releaseId : null;
		if (!requested || pending || disabled) return;
		const origin = target.current;
		const originHash = window.location.hash;
		setPending(true);
		setPrepared(false);
		setErrorKey(undefined);
		await Promise.resolve();
		if (!target.current) return;
		setPending(false);
		if (target.current !== origin || window.location.hash !== originHash)
			return;
		const identity = selected ?? canonical;
		const preparedId = identity ? resolveRelease(identity) : undefined;
		if (!preparedId) {
			setErrorKey("releaseNotPrepared");
			return;
		}
		setPrepared(true);
		onPrepared(preparedId);
	};

	return (
		<ManualReleasePreparationDataBinder
			model={{
				value,
				disabled,
				pending,
				prepared,
				errors: errorKey ? [{ id: "prepare", message: message(errorKey) }] : [],
				onValueChange: (next) => {
					setValue(next);
					setPrepared(false);
					setErrorKey(undefined);
				},
				onPrepare: () => void prepare(),
			}}
		>
			{children}
		</ManualReleasePreparationDataBinder>
	);
}
