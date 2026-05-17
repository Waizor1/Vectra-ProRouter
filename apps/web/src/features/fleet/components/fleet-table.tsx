"use client";

import * as React from "react";
import Link from "next/link";
import {
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  type ColumnDef,
  type FilterFn,
  type Row,
  type SortingState,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  Copy,
  ExternalLink,
  MoreHorizontal,
  RotateCcw,
  Search,
} from "lucide-react";

import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Input } from "~/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";
import { StatusDot } from "~/components/vectra/status-dot";
import { ToneBadge } from "~/components/vectra/tone-badge";
import { cn } from "~/lib/utils";
import { toneClasses, type Tone } from "~/lib/tone";

import { FleetBulkActions } from "./fleet-bulk-actions";

export type FleetTableOperationalState =
  | "stable"
  | "recovery"
  | "offline"
  | "review"
  | "blocked";

export interface FleetTableRouter {
  id: string;
  name: string;
  statusLabel: string;
  operationalState: FleetTableOperationalState;
  lastSeen: string;
  lastSeenAt: string | null;
  memoryLabel: string;
  memoryDetail: string;
  memoryLevel: "good" | "warning" | "critical" | "unknown";
  controllerVersion: string;
  alertCount: number;
}

export interface FleetTableProps {
  routers: FleetTableRouter[];
  initialSearchQuery?: string;
}

const operationalTone: Record<FleetTableOperationalState, Tone> = {
  stable: "good",
  recovery: "critical",
  offline: "warning",
  review: "info",
  blocked: "warning",
};

const operationalLabel: Record<FleetTableOperationalState, string> = {
  stable: "в строю",
  recovery: "rescue / direct",
  offline: "offline",
  review: "нужна сверка",
  blocked: "ограничен",
};

const memoryTone: Record<FleetTableRouter["memoryLevel"], Tone> = {
  good: "good",
  warning: "warning",
  critical: "critical",
  unknown: "neutral",
};

const fuzzyFilter: FilterFn<FleetTableRouter> = (row, _columnId, value) => {
  const query = String(value ?? "").trim().toLowerCase();
  if (!query) return true;
  const r = row.original;
  const haystack = [
    r.name,
    r.id,
    r.statusLabel,
    r.operationalState,
    operationalLabel[r.operationalState],
    r.controllerVersion,
    r.memoryLabel,
    r.memoryDetail,
    r.lastSeen,
  ]
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
};

export function FleetTable({ routers, initialSearchQuery = "" }: FleetTableProps) {
  const [rowSelection, setRowSelection] = React.useState<
    Record<string, boolean>
  >({});
  const [globalFilter, setGlobalFilter] = React.useState(initialSearchQuery);
  const [sorting, setSorting] = React.useState<SortingState>([
    { id: "operationalState", desc: false },
  ]);

  const columns = React.useMemo<ColumnDef<FleetTableRouter>[]>(
    () => [
      {
        id: "select",
        size: 32,
        enableSorting: false,
        header: ({ table }) => (
          <Checkbox
            aria-label="Выбрать все строки"
            checked={
              table.getIsAllPageRowsSelected() ||
              (table.getIsSomePageRowsSelected() && "indeterminate")
            }
            onCheckedChange={(value) =>
              table.toggleAllPageRowsSelected(Boolean(value))
            }
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            aria-label={`Выбрать ${row.original.name}`}
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(Boolean(value))}
            onClick={(event) => event.stopPropagation()}
          />
        ),
      },
      {
        accessorKey: "name",
        header: ({ column }) => (
          <SortHeader column={column} label="Роутер" />
        ),
        cell: ({ row }) => {
          const r = row.original;
          return (
            <div className="flex min-w-0 flex-col">
              <Link
                href={`/routers/${r.id}`}
                className="truncate font-medium text-foreground hover:text-primary"
              >
                {r.name}
              </Link>
              <span className="truncate text-xs text-muted-foreground">
                {r.id}
              </span>
            </div>
          );
        },
      },
      {
        accessorKey: "operationalState",
        header: ({ column }) => (
          <SortHeader column={column} label="Состояние" />
        ),
        sortingFn: (a, b) =>
          stateRank[a.original.operationalState] -
          stateRank[b.original.operationalState],
        cell: ({ row }) => {
          const r = row.original;
          const tone = operationalTone[r.operationalState];
          return (
            <div className="flex items-center gap-2">
              <StatusDot tone={tone} />
              <span className={cn("text-sm", toneClasses[tone].text)}>
                {operationalLabel[r.operationalState]}
              </span>
              {r.alertCount > 0 ? (
                <ToneBadge tone={tone} className="ml-1 px-1.5 py-0">
                  {r.alertCount}
                </ToneBadge>
              ) : null}
            </div>
          );
        },
      },
      {
        accessorKey: "lastSeen",
        header: ({ column }) => (
          <SortHeader column={column} label="Last seen" />
        ),
        sortingFn: (a, b) => {
          const lhs = a.original.lastSeenAt
            ? Date.parse(a.original.lastSeenAt)
            : 0;
          const rhs = b.original.lastSeenAt
            ? Date.parse(b.original.lastSeenAt)
            : 0;
          return lhs - rhs;
        },
        cell: ({ row }) => (
          <span className="whitespace-nowrap text-sm text-muted-foreground">
            {row.original.lastSeen}
          </span>
        ),
      },
      {
        accessorKey: "memoryLabel",
        header: "Память",
        enableSorting: false,
        cell: ({ row }) => {
          const r = row.original;
          const tone = memoryTone[r.memoryLevel];
          return (
            <div className="flex min-w-0 flex-col">
              <span className={cn("text-sm font-medium", toneClasses[tone].text)}>
                {r.memoryLabel}
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {r.memoryDetail}
              </span>
            </div>
          );
        },
      },
      {
        accessorKey: "controllerVersion",
        header: ({ column }) => (
          <SortHeader column={column} label="Контроллер" />
        ),
        cell: ({ row }) => (
          <span className="font-[family:var(--font-plex-mono,inherit)] text-xs tabular-nums text-foreground">
            {row.original.controllerVersion}
          </span>
        ),
      },
      {
        id: "actions",
        size: 40,
        enableSorting: false,
        header: () => <span className="sr-only">Действия</span>,
        cell: ({ row }) => <RouterActionsMenu router={row.original} />,
      },
    ],
    [],
  );

  const table = useReactTable({
    data: routers,
    columns,
    state: { rowSelection, globalFilter, sorting },
    getRowId: (router) => router.id,
    enableRowSelection: true,
    onRowSelectionChange: setRowSelection,
    onGlobalFilterChange: setGlobalFilter,
    onSortingChange: setSorting,
    globalFilterFn: fuzzyFilter,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  const selectedIds = table
    .getSelectedRowModel()
    .rows.map((row) => row.original.id);

  const handleClearSelection = React.useCallback(() => {
    setRowSelection({});
  }, []);

  const filteredCount = table.getFilteredRowModel().rows.length;

  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="relative w-full sm:max-w-xs">
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
            strokeWidth={1.75}
          />
          <Input
            value={globalFilter}
            onChange={(event) => setGlobalFilter(event.target.value)}
            placeholder="Поиск по имени, ID, состоянию…"
            className="h-9 pl-8"
          />
        </div>
        <span className="text-xs text-muted-foreground">
          Показано {filteredCount} из {routers.length}
        </span>
      </div>

      <FleetBulkActions
        selectedIds={selectedIds}
        onClear={handleClearSelection}
      />

      <div className="rounded-md border border-border/40 bg-card/30">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id} className="hover:bg-transparent">
                {headerGroup.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    style={
                      header.getSize() !== 150
                        ? { width: header.getSize() }
                        : undefined
                    }
                  >
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext(),
                        )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className="h-24 text-center text-sm text-muted-foreground"
                >
                  Ничего не найдено по вашему запросу.
                </TableCell>
              </TableRow>
            ) : (
              table.getRowModel().rows.map((row) => (
                <FleetTableBodyRow key={row.id} row={row} />
              ))
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

const stateRank: Record<FleetTableOperationalState, number> = {
  recovery: 0,
  offline: 1,
  blocked: 2,
  review: 3,
  stable: 4,
};

function FleetTableBodyRow({ row }: { row: Row<FleetTableRouter> }) {
  return (
    <TableRow
      data-state={row.getIsSelected() ? "selected" : undefined}
      className="align-top"
    >
      {row.getVisibleCells().map((cell) => (
        <TableCell key={cell.id}>
          {flexRender(cell.column.columnDef.cell, cell.getContext())}
        </TableCell>
      ))}
    </TableRow>
  );
}

interface SortHeaderProps {
  // ColumnDef header gives us the column instance; we type loosely to avoid leaking generics.
  column: {
    getCanSort: () => boolean;
    getIsSorted: () => false | "asc" | "desc";
    toggleSorting: (desc?: boolean) => void;
  };
  label: string;
}

function SortHeader({ column, label }: SortHeaderProps) {
  if (!column.getCanSort()) {
    return <span className="text-xs font-medium uppercase tracking-wider">{label}</span>;
  }
  const sort = column.getIsSorted();
  const Icon = sort === "asc" ? ArrowUp : sort === "desc" ? ArrowDown : ArrowUpDown;
  return (
    <button
      type="button"
      className="-ml-2 inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium uppercase tracking-wider text-muted-foreground transition-colors hover:bg-secondary/30 hover:text-foreground"
      onClick={() => column.toggleSorting(sort === "asc")}
    >
      {label}
      <Icon className="h-3 w-3" strokeWidth={1.75} />
    </button>
  );
}

function RouterActionsMenu({ router }: { router: FleetTableRouter }) {
  const handleCopyId = React.useCallback(() => {
    if (typeof navigator === "undefined" || !navigator.clipboard) return;
    void navigator.clipboard.writeText(router.id);
  }, [router.id]);

  const handleReboot = React.useCallback(() => {
    if (typeof window === "undefined") return;
    console.info(`[fleet-v2] reboot pending wiring for ${router.id}`);
  }, [router.id]);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          aria-label={`Действия для ${router.name}`}
        >
          <MoreHorizontal className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem asChild>
          <Link href={`/routers/${router.id}`}>
            <ExternalLink className="mr-2 h-3.5 w-3.5" />
            Открыть
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem onClick={handleReboot}>
          <RotateCcw className="mr-2 h-3.5 w-3.5" />
          Reboot
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={handleCopyId}>
          <Copy className="mr-2 h-3.5 w-3.5" />
          Скопировать ID
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
