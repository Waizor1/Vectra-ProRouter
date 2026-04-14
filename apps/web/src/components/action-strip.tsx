import type { ReactNode } from "react";

export function ActionStrip({
  children,
  justify = "between",
}: {
  children: ReactNode;
  justify?: "between" | "start";
}) {
  return (
    <div
      className={`flex flex-col items-stretch gap-2 rounded-md border border-white/10 bg-[var(--vectra-panel-soft)] px-3 py-3 [&>*]:w-full sm:flex-row sm:flex-wrap sm:items-center sm:[&>*]:w-auto ${
        justify === "between"
          ? "sm:justify-between"
          : "sm:justify-start"
      }`}
    >
      {children}
    </div>
  );
}
