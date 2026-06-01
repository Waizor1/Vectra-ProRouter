import { createTRPCRouter, protectedProcedure } from "~/server/api/trpc";
import { mintHappCrypt5Link } from "~/server/vectra/happ-crypt";
import { z } from "zod";

const encryptInputSchema = z.object({ url: z.string().url() });

export const happCryptRouter = createTRPCRouter({
  encrypt: protectedProcedure
    .input(encryptInputSchema)
    .mutation(async ({ input }) => {
      const link = await mintHappCrypt5Link(input.url);
      return { link };
    }),
});
