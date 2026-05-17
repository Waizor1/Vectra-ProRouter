import Link from "next/link";
import { ArrowRight, type LucideIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";

export interface RouterDetailPlaceholderProps {
  routerId: string;
  icon: LucideIcon;
  title: string;
  description: string;
  legacyTab: string;
  ctaLabel?: string;
  hint?: string;
}

export function RouterDetailPlaceholder({
  routerId,
  icon: Icon,
  title,
  description,
  legacyTab,
  ctaLabel = "Открыть расширенный режим",
  hint,
}: RouterDetailPlaceholderProps) {
  const href = `/routers/${routerId}?ui=v1&tab=${legacyTab}`;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Icon className="h-4 w-4" strokeWidth={1.75} />
          {title}
        </CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Расширенный режим временно в legacy. После переноса этой секции на
          shadcn ссылка перестанет быть нужна.
        </p>
        {hint ? (
          <p className="text-xs text-muted-foreground">{hint}</p>
        ) : null}
        <Button asChild size="sm">
          <Link href={href}>
            {ctaLabel}
            <ArrowRight className="ml-1 h-3 w-3" strokeWidth={1.75} />
          </Link>
        </Button>
      </CardContent>
    </Card>
  );
}
