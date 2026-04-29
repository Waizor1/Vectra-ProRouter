import { notFound } from "next/navigation";

import { TRPCError } from "@trpc/server";

import { RescueCaseCockpit } from "~/components/rescue-case-cockpit";
import { api } from "~/trpc/server";

export default async function RescueCasePage({
  params,
}: {
  params: Promise<{ caseId: string }>;
}) {
  const { caseId } = await params;
  const details = await api.rescue
    .caseById({ caseId })
    .catch((error: unknown) => {
      if (error instanceof TRPCError && error.code === "NOT_FOUND") {
        notFound();
      }
      throw error;
    });

  return <RescueCaseCockpit initialDetails={details} />;
}
