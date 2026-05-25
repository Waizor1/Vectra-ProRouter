"use client";

import { Globe } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";

import { patchDns, type ConfigSectionProps } from "../config-editor-state";
import {
  BooleanField,
  SelectField,
  StringListField,
  TextField,
  type SelectOption,
} from "../fields";

const STRATEGY_OPTIONS: SelectOption[] = [
  { value: "UseIP", label: "UseIP" },
  { value: "UseIPv4", label: "UseIPv4" },
  { value: "UseIPv6", label: "UseIPv6" },
];

const PROTOCOL_OPTIONS: SelectOption[] = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "doh", label: "DoH" },
  { value: "tls", label: "DoT (TLS)" },
  { value: "quic", label: "DoQ (QUIC)" },
  { value: "http3", label: "HTTP/3" },
];

const DETOUR_OPTIONS: SelectOption[] = [
  { value: "remote", label: "Через прокси" },
  { value: "direct", label: "Напрямую" },
];

export function DnsSection({ config, onChange, disabled }: ConfigSectionProps) {
  const dns = config.basicSettings.dns;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <Globe className="h-4 w-4" strokeWidth={1.75} />
          DNS
        </CardTitle>
        <CardDescription>
          Резолвинг прямых и проксируемых запросов.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-x-8 gap-y-0 sm:grid-cols-2">
        <SelectField
          label="Стратегия прямых запросов"
          value={dns.directQueryStrategy}
          options={STRATEGY_OPTIONS}
          onValueChange={(value) =>
            onChange(
              patchDns(config, {
                directQueryStrategy: value as typeof dns.directQueryStrategy,
              }),
            )
          }
          disabled={disabled}
        />
        <SelectField
          label="Протокол удалённого DNS"
          value={dns.remoteDnsProtocol}
          options={PROTOCOL_OPTIONS}
          onValueChange={(value) =>
            onChange(
              patchDns(config, {
                remoteDnsProtocol: value as typeof dns.remoteDnsProtocol,
              }),
            )
          }
          disabled={disabled}
        />
        <TextField
          label="Удалённый DNS"
          value={dns.remoteDns}
          placeholder="1.1.1.1"
          onChange={(value) => onChange(patchDns(config, { remoteDns: value }))}
          disabled={disabled}
        />
        <TextField
          label="Client IP (EDNS)"
          value={dns.remoteDnsClientIp ?? ""}
          placeholder="необязательно"
          onChange={(value) =>
            onChange(
              patchDns(config, {
                remoteDnsClientIp: value.trim() ? value : undefined,
              }),
            )
          }
          disabled={disabled}
        />
        <SelectField
          label="Маршрут удалённого DNS"
          value={dns.remoteDnsDetour}
          options={DETOUR_OPTIONS}
          onValueChange={(value) =>
            onChange(
              patchDns(config, {
                remoteDnsDetour: value as typeof dns.remoteDnsDetour,
              }),
            )
          }
          disabled={disabled}
        />
        <SelectField
          label="Стратегия удалённых запросов"
          value={dns.remoteDnsQueryStrategy}
          options={STRATEGY_OPTIONS}
          onValueChange={(value) =>
            onChange(
              patchDns(config, {
                remoteDnsQueryStrategy:
                  value as typeof dns.remoteDnsQueryStrategy,
              }),
            )
          }
          disabled={disabled}
        />
        <BooleanField
          label="FakeDNS для прокси"
          checked={dns.remoteFakeDns}
          onCheckedChange={(value) =>
            onChange(patchDns(config, { remoteFakeDns: value }))
          }
          disabled={disabled}
        />
        <BooleanField
          label="Перехват DNS (redirect)"
          checked={dns.dnsRedirect}
          onCheckedChange={(value) =>
            onChange(patchDns(config, { dnsRedirect: value }))
          }
          disabled={disabled}
        />
        <div className="sm:col-span-2">
          <TextField
            label="Удалённый DNS (DoH URL)"
            type="url"
            value={dns.remoteDnsDoh}
            placeholder="https://1.1.1.1/dns-query"
            onChange={(value) =>
              onChange(patchDns(config, { remoteDnsDoh: value }))
            }
            disabled={disabled}
          />
        </div>
        <div className="sm:col-span-2">
          <StringListField
            label="DNS hosts"
            description="Статические записи, по одной на строку."
            values={dns.dnsHosts}
            onCommit={(values) =>
              onChange(patchDns(config, { dnsHosts: values }))
            }
            placeholder={"example.com 1.2.3.4"}
            disabled={disabled}
          />
        </div>
      </CardContent>
    </Card>
  );
}
