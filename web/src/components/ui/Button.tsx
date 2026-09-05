import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../shared/cn";

const variants = cva(
  "ui-button focus-ring disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        primary: "bg-[var(--brand)] text-[var(--on-brand)] hover:opacity-85",
        secondary: "border border-[var(--border)] bg-[var(--surface)] hover:bg-[var(--soft)]",
        ghost: "hover:bg-[var(--soft)]",
        danger: "bg-[var(--danger-soft)] text-[var(--danger)] hover:opacity-85"
      },
      size: { sm: "px-2.5 py-1.5 text-xs", md: "", icon: "size-9 p-0" }
    },
    defaultVariants: { variant: "primary", size: "md" }
  }
);

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof variants> {}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, ...props }, ref) => (
  <button ref={ref} data-size={size ?? "md"} className={cn(variants({ variant, size }), className)} {...props} />
));
Button.displayName = "Button";
