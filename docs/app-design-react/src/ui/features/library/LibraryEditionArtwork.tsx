import { useState } from "react";

const tileClass =
  "relative grid size-12 flex-none place-items-center overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--raised)] text-[var(--ink)] data-[palette=forest]:bg-[color-mix(in_srgb,#486d59_14%,var(--raised))] data-[palette=dusk]:bg-[color-mix(in_srgb,#676e95_14%,var(--raised))] data-[palette=sand]:bg-[color-mix(in_srgb,#aa905f_16%,var(--raised))] data-[palette=slate]:bg-[color-mix(in_srgb,#4c717e_14%,var(--raised))] max-[600px]:size-10";

export function LibraryEditionArtwork({
  title,
  palette = "clay",
  imageSrc,
  imageAlt,
}: {
  title: string;
  palette?: string;
  imageSrc?: string;
  imageAlt?: string;
}) {
  const [failedImage, setFailedImage] = useState<string>();
  const visibleImage =
    imageSrc && imageSrc !== failedImage ? imageSrc : undefined;

  return (
    <span
      aria-hidden={visibleImage && imageAlt ? undefined : true}
      className={tileClass}
      data-palette={palette}
    >
      {!visibleImage ? (
        <span
          aria-hidden="true"
          className="text-sm font-semibold uppercase text-[var(--muted)]"
        >
          {title.trim().slice(0, 1).toLocaleUpperCase() || "M"}
        </span>
      ) : null}
      {visibleImage ? (
        <img
          key={visibleImage}
          src={visibleImage}
          alt={imageAlt ?? ""}
          loading="lazy"
          onError={() => setFailedImage(visibleImage)}
          className="absolute inset-0 size-full bg-[var(--raised)] object-cover"
        />
      ) : null}
    </span>
  );
}
