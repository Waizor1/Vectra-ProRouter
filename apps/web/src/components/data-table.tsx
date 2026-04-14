import type { ReactNode } from "react";

export type DataTableColumn = {
  key: string;
  label: string;
  className?: string;
};

export function DataTable({
  columns,
  children,
}: {
  columns: readonly DataTableColumn[];
  children: ReactNode;
}) {
  return (
    <div className="vectra-scrollbarless overflow-x-auto rounded-md border border-white/10 bg-[var(--vectra-panel-soft)]">
      <table className="min-w-full border-collapse text-[13px] sm:text-sm">
        <thead>
          <tr className="border-b border-white/10">
            {columns.map((column) => (
              <th
                key={column.key}
                className={`vectra-kicker px-3 py-2 text-left text-slate-500 ${column.className ?? ""}`}
              >
                {column.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
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
        className="px-3 py-8 text-center text-sm leading-7 text-slate-400"
      >
        {children}
      </td>
    </tr>
  );
}
