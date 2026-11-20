import { useEffect, useState } from "react";
import { useI18n } from "../../i18n";
import { ManualReleasePreparationView } from "./ManualReleasePreparationView";
import {
	type ManualReleasePreparationMessageKey,
	manualReleasePreparationMessage,
} from "./manualReleasePreparationContract";

export type MatchingReleasePreparationProps = {
	/** Explicit selection coming from the release search, mirroring `searchRelease`. */
	releaseId?: string | null;
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
	disabled = false,
	resolveRelease,
	onPrepared,
}: MatchingReleasePreparationProps) {
	const { locale } = useI18n();
	const [value, setValue] = useState(releaseId ?? "");
	const [pending, setPending] = useState(false);
	const [prepared, setPrepared] = useState(false);
	const [errorKey, setErrorKey] =
		useState<ManualReleasePreparationMessageKey>();

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
		setPending(true);
		setPrepared(false);
		setErrorKey(undefined);
		await Promise.resolve();
		setPending(false);
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
		<ManualReleasePreparationView
			locale={locale}
			value={value}
			disabled={disabled}
			pending={pending}
			prepared={prepared}
			errors={errorKey ? [{ id: "prepare", message: message(errorKey) }] : []}
			onValueChange={(next) => {
				setValue(next);
				setPrepared(false);
				setErrorKey(undefined);
			}}
			onPrepare={() => void prepare()}
		/>
	);
}
