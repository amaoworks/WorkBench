import { createContext, useContext, useEffect } from "react";

/** Lets a settings panel report unsaved edits to the dialog that hosts it. */
export type SettingsDialogControl = {
  requestClose: () => void;
  setDirty: (dirty: boolean) => void;
};

export const SettingsDialogContext = createContext<SettingsDialogControl | null>(null);

export function useSettingsDialog() {
  return useContext(SettingsDialogContext);
}

export function useReportDirty(dirty: boolean) {
  const control = useSettingsDialog();
  useEffect(() => {
    control?.setDirty(dirty);
  }, [control, dirty]);
  useEffect(() => () => control?.setDirty(false), [control]);
}
