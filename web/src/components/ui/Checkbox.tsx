import { useId, type ButtonHTMLAttributes, type ReactNode } from "react";
import { Check } from "lucide-react";
import { cn } from "../../shared/cn";

type CheckboxProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange" | "children"> & {
  checked: boolean;
  onChange: (checked: boolean) => void;
  children: ReactNode;
};

/** Shared checkbox with its visible label; exposed as role="checkbox" like Switch is exposed as role="switch". */
export function Checkbox({ checked, onChange, children, className, ...props }: CheckboxProps) {
  const labelId = useId();
  return (
    <button
      {...props}
      type="button"
      role="checkbox"
      aria-checked={checked}
      aria-labelledby={labelId}
      className={cn("ui-checkbox focus-ring", className)}
      onClick={() => onChange(!checked)}
    >
      <span className="ui-checkbox-box" aria-hidden="true">
        {checked && <Check size={12} strokeWidth={3} />}
      </span>
      <span id={labelId}>{children}</span>
    </button>
  );
}
