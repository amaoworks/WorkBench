import { cn } from "../../shared/cn";

export function TaskCheck({
  checked,
  label,
  disabled,
  onChange,
  className,
}: {
  checked: boolean;
  label: string;
  disabled?: boolean;
  onChange: () => void;
  className?: string;
}) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      className={cn("ck focus-ring", checked && "done", className)}
      onClick={onChange}
    />
  );
}
