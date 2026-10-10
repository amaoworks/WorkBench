import * as RadixSelect from "@radix-ui/react-select";
import { Check, ChevronDown } from "lucide-react";
import type { AriaAttributes } from "react";
import { cn } from "../../shared/cn";

export type SelectOption = { value: string; label: string };

// Radix reserves the empty string for "no selection", so an explicit empty option needs a stand-in value.
const EMPTY = "__empty__";
const toItem = (value: string) => (value === "" ? EMPTY : value);

type SelectProps = AriaAttributes & {
  value: string;
  onValueChange: (value: string) => void;
  options: readonly SelectOption[];
  id?: string;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
};

/** Shared single-choice dropdown. Field passes id and aria props through to the trigger. */
export function Select({ value, onValueChange, options, id, placeholder, disabled, className, ...aria }: SelectProps) {
  return (
    <div className={cn("ui-select", className)}>
      <RadixSelect.Root value={toItem(value)} onValueChange={(next) => onValueChange(next === EMPTY ? "" : next)} disabled={disabled}>
        <RadixSelect.Trigger id={id} className="ui-input ui-select-trigger" {...aria}>
          <RadixSelect.Value placeholder={placeholder} />
          <RadixSelect.Icon className="ui-select-icon">
            <ChevronDown size={16} aria-hidden="true" />
          </RadixSelect.Icon>
        </RadixSelect.Trigger>
        <RadixSelect.Portal>
          <RadixSelect.Content position="popper" sideOffset={4} className="ui-select-content">
            <RadixSelect.Viewport className="ui-select-viewport">
              {options.map((option) => (
                <RadixSelect.Item key={option.value} value={toItem(option.value)} className="ui-select-item">
                  <RadixSelect.ItemText>{option.label}</RadixSelect.ItemText>
                  <RadixSelect.ItemIndicator className="ui-select-indicator">
                    <Check size={14} aria-hidden="true" />
                  </RadixSelect.ItemIndicator>
                </RadixSelect.Item>
              ))}
            </RadixSelect.Viewport>
          </RadixSelect.Content>
        </RadixSelect.Portal>
      </RadixSelect.Root>
    </div>
  );
}
