import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../shared/cn";

type SwitchProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange"> & {
  checked: boolean;
  onChange: (value: boolean) => void;
  label: string;
};

export function Switch({ checked, onChange, label, className, ...props }: SwitchProps) {
  return (
    <button
      {...props}
      type="button"
      className={cn("studio-switch focus-ring", className)}
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
    >
      <span className="studio-switch-track">
        <span />
      </span>
    </button>
  );
}
