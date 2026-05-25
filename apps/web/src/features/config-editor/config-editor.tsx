"use client";

import type { PasswallDesiredConfig } from "@vectra/contracts";

import { DnsSection } from "./sections/dns-section";
import { LogSection } from "./sections/log-section";
import { MainSection } from "./sections/main-section";
import { MaintenanceSection } from "./sections/maintenance-section";
import { ShuntRulesSection } from "./sections/shunt-rules-section";

export interface ConfigEditorProps {
  config: PasswallDesiredConfig;
  onChange: (next: PasswallDesiredConfig) => void;
  disabled?: boolean;
}

// Visual, JSON-free editor over PasswallDesiredConfig. Controlled component:
// holds no state, every edit emits a new config via onChange. Reused by the
// router-detail config tab and the rollout-profile editor.
export function ConfigEditor({ config, onChange, disabled }: ConfigEditorProps) {
  // Single column on small screens; masonry (CSS columns) from xl up so cards of
  // differing heights pack tightly into two columns with no ragged gaps, while
  // still filling desktop width and minimizing vertical scroll.
  return (
    <div className="vectra-config-editor [&>*]:mb-4 xl:columns-2 xl:gap-4 xl:[&>*]:break-inside-avoid">
      <MainSection config={config} onChange={onChange} disabled={disabled} />
      <ShuntRulesSection
        config={config}
        onChange={onChange}
        disabled={disabled}
      />
      <DnsSection config={config} onChange={onChange} disabled={disabled} />
      <LogSection config={config} onChange={onChange} disabled={disabled} />
      <MaintenanceSection
        config={config}
        onChange={onChange}
        disabled={disabled}
      />
    </div>
  );
}
