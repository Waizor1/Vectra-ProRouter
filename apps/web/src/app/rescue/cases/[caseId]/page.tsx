import { redirect } from "next/navigation";

// The V2 rescue surface shows cases and their actions inline on /rescue.
// The standalone case cockpit route is retired — redirect any old links.
export default function RescueCasePage() {
  redirect("/rescue");
}
