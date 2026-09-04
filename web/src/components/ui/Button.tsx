import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../shared/cn";

const variants = cva(
  "focus-ring inline-flex items-center justify-center gap-2 rounded-lg px-3.5 py-2 font-medium transition disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        primary: "bg-indigo-500 text-white shadow-sm hover:bg-indigo-600",
        secondary: "border border-[var(--border)] bg-[var(--surface)] hover:bg-slate-100 dark:hover:bg-slate-800",
        ghost: "hover:bg-slate-100 dark:hover:bg-slate-800",
        danger: "bg-red-500 text-white hover:bg-red-600"
      },
      size: { sm: "px-2.5 py-1.5 text-xs", md: "", icon: "size-9 p-0" }
    },
    defaultVariants: { variant: "primary", size: "md" }
  }
);

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof variants> {}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, ...props }, ref) => (
  <button ref={ref} className={cn(variants({ variant, size }), className)} {...props} />
));
Button.displayName = "Button";

