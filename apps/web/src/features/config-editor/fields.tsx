"use client";

import { useEffect, useId, useState } from "react";

import { Checkbox } from "~/components/ui/checkbox";
import { Input } from "~/components/ui/input";
import { Label } from "~/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Switch } from "~/components/ui/switch";
import { Textarea } from "~/components/ui/textarea";

export interface SelectOption {
  value: string;
  label: string;
}

export function BooleanField({
  label,
  description,
  checked,
  onCheckedChange,
  disabled,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onCheckedChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="flex items-start justify-between gap-3 py-2.5">
      <div className="space-y-0.5">
        <Label htmlFor={id} className="text-sm font-medium">
          {label}
        </Label>
        {description ? (
          <p className="text-xs text-muted-foreground">{description}</p>
        ) : null}
      </div>
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onCheckedChange}
        disabled={disabled}
      />
    </div>
  );
}

export function TextField({
  label,
  description,
  value,
  onChange,
  placeholder,
  disabled,
  type = "text",
}: {
  label: string;
  description?: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
  type?: "text" | "url" | "password";
}) {
  const id = useId();
  return (
    <div className="space-y-1.5 py-2.5">
      <Label htmlFor={id} className="text-sm font-medium">
        {label}
      </Label>
      <Input
        id={id}
        type={type}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      />
      {description ? (
        <p className="text-xs text-muted-foreground">{description}</p>
      ) : null}
    </div>
  );
}

export function NumberField({
  label,
  description,
  value,
  onChange,
  min,
  max,
  disabled,
}: {
  label: string;
  description?: string;
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5 py-2.5">
      <Label htmlFor={id} className="text-sm font-medium">
        {label}
      </Label>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        value={Number.isFinite(value) ? value : ""}
        min={min}
        max={max}
        disabled={disabled}
        onChange={(event) => {
          const parsed = Number.parseInt(event.target.value, 10);
          if (Number.isNaN(parsed)) {
            return;
          }
          onChange(parsed);
        }}
      />
      {description ? (
        <p className="text-xs text-muted-foreground">{description}</p>
      ) : null}
    </div>
  );
}

export function SelectField({
  label,
  description,
  value,
  onValueChange,
  options,
  placeholder,
  disabled,
}: {
  label: string;
  description?: string;
  value: string;
  onValueChange: (value: string) => void;
  options: SelectOption[];
  placeholder?: string;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5 py-2.5">
      <Label htmlFor={id} className="text-sm font-medium">
        {label}
      </Label>
      <Select
        value={value || undefined}
        onValueChange={onValueChange}
        disabled={disabled}
      >
        <SelectTrigger id={id}>
          <SelectValue placeholder={placeholder} />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {description ? (
        <p className="text-xs text-muted-foreground">{description}</p>
      ) : null}
    </div>
  );
}

// Multi-select via checkboxes. Emits the selected values in option order.
export function CheckboxGroupField({
  label,
  description,
  options,
  selected,
  onChange,
  disabled,
}: {
  label: string;
  description?: string;
  options: SelectOption[];
  selected: string[];
  onChange: (values: string[]) => void;
  disabled?: boolean;
}) {
  const toggle = (value: string, checked: boolean) => {
    const set = new Set(selected);
    if (checked) {
      set.add(value);
    } else {
      set.delete(value);
    }
    onChange(options.map((option) => option.value).filter((v) => set.has(v)));
  };

  return (
    <div className="space-y-1.5 py-2.5">
      <Label className="text-sm font-medium">{label}</Label>
      <div className="flex flex-wrap gap-x-4 gap-y-2">
        {options.map((option) => (
          <label
            key={option.value}
            className="inline-flex items-center gap-2 text-sm"
          >
            <Checkbox
              checked={selected.includes(option.value)}
              onCheckedChange={(state) => toggle(option.value, state === true)}
              disabled={disabled}
            />
            {option.label}
          </label>
        ))}
      </div>
      {description ? (
        <p className="text-xs text-muted-foreground">{description}</p>
      ) : null}
    </div>
  );
}

// One entry per line. Commits trimmed, non-empty lines on blur so typing a new
// line is not destroyed by re-parsing on every keystroke.
export function StringListField({
  label,
  description,
  values,
  onCommit,
  placeholder,
  disabled,
  rows = 4,
}: {
  label: string;
  description?: string;
  values: string[];
  onCommit: (values: string[]) => void;
  placeholder?: string;
  disabled?: boolean;
  rows?: number;
}) {
  const id = useId();
  const joined = values.join("\n");
  const [text, setText] = useState(joined);

  useEffect(() => {
    setText(joined);
  }, [joined]);

  return (
    <div className="space-y-1.5 py-2.5">
      <Label htmlFor={id} className="text-sm font-medium">
        {label}
      </Label>
      <Textarea
        id={id}
        rows={rows}
        value={text}
        placeholder={placeholder}
        disabled={disabled}
        onChange={(event) => setText(event.target.value)}
        onBlur={() =>
          onCommit(
            text
              .split("\n")
              .map((line) => line.trim())
              .filter((line) => line.length > 0),
          )
        }
      />
      {description ? (
        <p className="text-xs text-muted-foreground">{description}</p>
      ) : null}
    </div>
  );
}
