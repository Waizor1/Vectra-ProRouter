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
      className={`vectra-toolbar min-w-0 flex-col items-stretch ${dense ? "gap-2" : "gap-3"} [&>*]:min-w-0 [&>*]:w-full lg:flex-row lg:flex-wrap lg:items-center lg:[&>*]:w-auto lg:[&>*]:shrink-0 ${
        justify === "between"
          ? "lg:justify-between"
          : "lg:justify-start"
      }`}
    >
      {children}
    </div>
  );
}
