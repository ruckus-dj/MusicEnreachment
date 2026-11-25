import { useState } from "react";

const tileClass =
  "relative grid aspect-square w-[68px] flex-none place-items-center overflow-hidden rounded-[7px] bg-[linear-gradient(145deg,color-mix(in_srgb,var(--album-color)_78%,#121212),color-mix(in_srgb,var(--album-color)_44%,#15201d))] text-white shadow-[0_4px_10px_#0002] [--album-color:#ac7059] data-[palette=forest]:[--album-color:#486d59] data-[palette=dusk]:[--album-color:#676e95] data-[palette=sand]:[--album-color:#aa905f] data-[palette=slate]:[--album-color:#4c717e] before:absolute before:inset-[10%] before:rounded-[50%_42%_52%_45%] before:border before:border-[#ffffff60] before:content-[''] before:[transform:rotate(-22deg)] max-[600px]:w-[54px]";

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
      <span
        aria-hidden="true"
        className="relative font-[Georgia,serif] text-[34px] [text-shadow:0_2px_8px_#0008] max-[600px]:text-[28px]"
      >
        {title.trim().slice(0, 1).toLocaleUpperCase() || "M"}
      </span>
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
