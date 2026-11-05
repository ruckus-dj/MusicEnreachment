import type { SourceFile } from "./catalogData";

/** Displays only fixture-backed technical measurements; codec is shown separately. */
export function audioProperties(source: SourceFile | undefined): string {
	if (!source) return "—";
	const facts = [
		source.bits != null ? `${source.bits} bit` : null,
		source.sampleRate != null ? `${source.sampleRate / 1000} kHz` : null,
		source.bitrate != null ? `${source.bitrate} bit/s` : null,
	].filter((fact): fact is string => Boolean(fact));
	return facts.length ? facts.join(" · ") : "—";
}
