import { type Config } from "drizzle-kit";

import { env } from "~/env";

export default {
  schema: "../../packages/db/src/schema.ts",
  dialect: "postgresql",
  dbCredentials: {
    url: env.DATABASE_URL,
  },
  out: "../../packages/db/drizzle",
  tablesFilter: ["vectra_*"],
} satisfies Config;
