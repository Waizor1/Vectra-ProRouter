export type TaskCardProps = {
  title: string;
  description: string;
  status: "pending" | "running" | "complete";
  steps: string[];
};

const statusMap: Record<TaskCardProps["status"], string> = {
  pending: "bg-sky-500/12 text-sky-200",
  running: "bg-amber-500/12 text-amber-200",
  complete: "bg-emerald-500/12 text-emerald-200",
};

const statusLabels: Record<TaskCardProps["status"], string> = {
  pending: "в планах",
  running: "в работе",
  complete: "завершено",
};

export function TaskCard({ title, description, status, steps }: TaskCardProps) {
  return (
    <article className="flex h-full flex-col gap-3 rounded-md border border-white/10 bg-[var(--vectra-panel)] p-4">
      <div className="flex items-center justify-between">
        <h3 className="text-base font-semibold text-white">{title}</h3>
        <span
          className={`vectra-chip rounded-full px-3 py-1 ${statusMap[status]}`}
        >
          {statusLabels[status]}
        </span>
      </div>
      <p className="text-sm leading-6 text-slate-300">{description}</p>
      <ul className="space-y-2 text-sm text-slate-200">
        {steps.map((step) => (
          <li key={step} className="flex items-start gap-3">
            <span className="mt-1.5 h-2 w-2 rounded-full bg-[var(--vectra-accent)]" />
            <span>{step}</span>
          </li>
        ))}
      </ul>
    </article>
  );
}
