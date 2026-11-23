import { type ReactNode, useLayoutEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";
import {
  AggregatePublicationPreviewView,
  type AggregatePublicationPreviewViewProps,
} from "../library/AggregatePublicationPreviewView";

const copy = {
  en: {
    back: "Back to saved links",
    title: "Publication from saved matching links",
    prepare: "Prepare preview",
    confirm: "Publish ready positions",
    sourceLegend: "Linked file for publication",
    sourceNone: "No file selected",
  },
  ru: {
    back: "К сохранённым связям",
    title: "Публикация по сохранённым связям",
    prepare: "Подготовить предпросмотр",
    confirm: "Опубликовать готовые позиции",
    sourceLegend: "Связанный файл для публикации",
    sourceNone: "Файл не выбран",
  },
} as const;

export function matchingReceiptFileChoiceLabels(locale: keyof typeof copy) {
  const { sourceLegend: legend, sourceNone: emptyLabel } = copy[locale];
  return { legend, emptyLabel };
}

type MatchingReceiptPublicationFrameProps = Omit<
  AggregatePublicationPreviewViewProps,
  | "mode"
  | "onModeChange"
  | "title"
  | "headingRef"
  | "prepareLabel"
  | "confirmLabel"
> & {
  releaseTitle: string;
  onBack: () => void;
  children?: ReactNode;
};

export function MatchingReceiptPublicationFrame({
  releaseTitle,
  onBack,
  children,
  ...preview
}: MatchingReceiptPublicationFrameProps) {
  const text = copy[preview.locale];
  const headingRef = useRef<HTMLHeadingElement>(null);
  useLayoutEffect(() => {
    const heading = headingRef.current;
    heading?.focus();
    heading?.scrollIntoView?.({ block: "start" });
  }, []);

  return (
    <section data-matching-receipt-publication className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="m-0 wrap-anywhere font-medium">{releaseTitle}</p>
        <AppButton onPress={onBack}>{text.back}</AppButton>
      </div>
      <AggregatePublicationPreviewView
        {...preview}
        title={text.title}
        headingRef={headingRef}
        prepareLabel={text.prepare}
        confirmLabel={text.confirm}
      />
      {children}
    </section>
  );
}
