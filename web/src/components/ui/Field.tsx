import { cloneElement, useId, type AriaAttributes, type ReactElement, type ReactNode } from "react";
import { cn } from "../../shared/cn";

type FieldProps = {
  label: ReactNode;
  meta?: ReactNode;
  error?: string;
  className?: string;
  children: ReactElement<AriaAttributes & { id?: string }>;
};

export function Field({ label, meta, error, className, children }: FieldProps) {
  const generatedId = useId();
  const id = children.props.id ?? generatedId;
  const labelId = `${id}-label`;
  const metaId = `${id}-meta`;
  const errorId = `${id}-error`;
  const describedBy =
    [children.props["aria-describedby"], meta ? metaId : undefined, error ? errorId : undefined].filter(Boolean).join(" ") || undefined;
  return (
    <div className={cn("studio-field", className)}>
      <label htmlFor={id} className="studio-field-label">
        <span id={labelId}>{label}</span>
        {meta && <span id={metaId}>{meta}</span>}
      </label>
      {cloneElement(children, {
        id,
        "aria-labelledby": children.props["aria-labelledby"] ?? (children.props["aria-label"] ? undefined : labelId),
        "aria-describedby": describedBy,
        "aria-invalid": error ? true : children.props["aria-invalid"],
      })}
      {error && (
        <p id={errorId} className="field-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
