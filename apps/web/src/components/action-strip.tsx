import type { ReactNode } from "react";

export function ActionStrip({
  children,
  justify = "between",
  dense = false,
}: {
  children: ReactNode;
  justify?: "between" | "start";
  dense?: boolean;
}) {
  return (
    <div
      className={`vectra-toolbar flex-col items-stretch ${dense ? "gap-2" : "gap-3"} [&>*]:w-full sm:flex-row sm:flex-wrap sm:items-center sm:[&>*]:w-auto ${
        justify === "between"
          ? "sm:justify-between"
          : "sm:justify-start"
      }`}
    >
      {children}
    </div>
  );
}
