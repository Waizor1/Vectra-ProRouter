import Link from "next/link";
import { ShieldCheck } from "lucide-react";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import { Alert, AlertDescription, AlertTitle } from "~/components/ui/alert";
import { Separator } from "~/components/ui/separator";

export interface LoginV2Props {
  hasError: boolean;
}

export function LoginV2({ hasError }: LoginV2Props) {
  return (
    <section className="mx-auto flex min-h-[70vh] w-full max-w-md items-center px-4">
      <Card className="w-full bg-card/95 backdrop-blur-sm">
        <CardHeader className="space-y-2">
          <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">
            <ShieldCheck className="h-3.5 w-3.5" strokeWidth={1.75} />
            Вход оператора
          </div>
          <CardTitle className="text-2xl tracking-tight">
            Панель Vectra ProRouter
          </CardTitle>
          <CardDescription>
            Доступ только для операторов. Введите учётные данные, чтобы
            управлять парком сертифицированных OpenWrt-роутеров.
          </CardDescription>
        </CardHeader>

        <form action="/api/operator/login" method="post">
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="login-username">Логин</Label>
              <Input
                id="login-username"
                name="username"
                type="text"
                autoComplete="username"
                placeholder="operator"
                required
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="login-password">Пароль</Label>
              <Input
                id="login-password"
                name="password"
                type="password"
                autoComplete="current-password"
                placeholder="Введите пароль оператора"
                required
              />
            </div>

            {hasError ? (
              <Alert variant="destructive">
                <AlertTitle>Не удалось войти</AlertTitle>
                <AlertDescription>Неверный логин или пароль.</AlertDescription>
              </Alert>
            ) : null}
          </CardContent>

          <CardFooter className="flex flex-col items-stretch gap-3 pt-0">
            <Button type="submit" size="lg" className="w-full">
              Войти
            </Button>
            <p className="text-center text-xs text-muted-foreground">
              После входа основной экран —{" "}
              <span className="font-medium text-foreground">Fleet</span>:
              алерты, состояние парка, действия по роутеру.
            </p>
          </CardFooter>
        </form>

        <Separator />

        <div className="px-6 py-4 text-center text-sm">
          <Link
            href="/install"
            className="text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            Перейти к публичной установке
          </Link>
        </div>
      </Card>
    </section>
  );
}
