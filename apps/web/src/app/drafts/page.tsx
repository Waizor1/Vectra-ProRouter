import { redirect } from "next/navigation";

// JSON expert drafts surface is retired — the visual config editor and the
// profiles/rollout center on /updates replace it. Redirect any old links.
export default function DraftsPage() {
  redirect("/updates");
}
