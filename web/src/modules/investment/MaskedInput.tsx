import { useState, type InputHTMLAttributes } from "react";
import { Eye, EyeOff } from "lucide-react";
import { Button } from "../../components/ui/Button";

/** A text input that stays masked until the user asks to see it. Field passes id and aria props through to the input. */
export function MaskedInput({ revealLabel, ...props }: Omit<InputHTMLAttributes<HTMLInputElement>, "type"> & { revealLabel: string }) {
  const [shown, setShown] = useState(false);
  return (
    <div className="masked-input">
      <input {...props} type={shown ? "text" : "password"} autoComplete="off" spellCheck={false} />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={`${shown ? "隐藏" : "显示"} ${revealLabel}`}
        aria-pressed={shown}
        disabled={props.disabled}
        onClick={() => setShown((value) => !value)}
      >
        {shown ? <EyeOff size={16} aria-hidden="true" /> : <Eye size={16} aria-hidden="true" />}
      </Button>
    </div>
  );
}
