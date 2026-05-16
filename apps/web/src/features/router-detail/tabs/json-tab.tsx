import Link from "next/link";
import { ArrowRight, Code2 } from "lucide-react";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";

export interface JsonTabProps {
  routerId: string;
}

export function JsonTab({ routerId }: JsonTabProps) {
  const legacyHref = `/routers/${routerId}?ui=v1&tab=basic-settings`;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Code2 className="h-4 w-4" strokeWidth={1.75} />
          JSON эксперт
        </CardTitle>
        <CardDescription>
          PassWall desired-config как JSON: точечные правки и
          import/export для нестандартных случаев.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">
          JSON-режим конкретного роутера временно в legacy. Глобальный
          JSON-черновик доступен через /drafts.
        </p>
        <div className="flex flex-wrap gap-2">
          <Button asChild size="sm">
            <Link href={legacyHref}>
              Открыть legacy JSON
              <ArrowRight className="ml-1 h-3 w-3" strokeWidth={1.75} />
            </Link>
          </Button>
          <Button asChild size="sm" variant="outline">
            <Link href="/drafts">Глобальные черновики</Link>
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
