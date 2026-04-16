import type { ReactNode } from "react";

export type DataTableColumn = {
  key: string;
  label: string;
  className?: string;
};

export function DataTable({
  columns,
  children,
  title,
  hint,
}: {
  columns: readonly DataTableColumn[];
  children: ReactNode;
  title?: string;
  hint?: string;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-3 px-1 text-[11px] leading-5 text-slate-500">
        <span>{title ?? "Плотная таблица"}</span>
        <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-0.5 text-slate-400">
          {hint ?? "По горизонтали можно прокручивать →"}
        </span>
      </div>

      <div className="overflow-x-auto rounded-2xl border border-white/10 bg-[var(--vectra-panel-muted)] shadow-[inset_0_1px_0_rgba(255,255,255,0.02)]">
        <table className="min-w-full border-collapse text-[13px] sm:text-sm">
          <thead className="sticky top-0 z-10 bg-[rgba(10,14,20,0.96)] backdrop-blur">
            <tr className="border-b border-white/10">
              {columns.map((column) => (
                <th
                  key={column.key}
                  className={`vectra-kicker px-3 py-3 text-left text-slate-500 ${column.className ?? ""}`}
                >
                  {column.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>{children}</tbody>
        </table>
      </div>
    </div>
  );
}

export function DataTableEmpty({
  colSpan,
  children,
}: {
  colSpan: number;
  children: ReactNode;
}) {
  return (
    <tr>
      <td
        colSpan={colSpan}
        className="px-3 py-10 text-center text-sm leading-7 text-slate-400"
      >
        {children}
      </td>
    </tr>
  );
}
