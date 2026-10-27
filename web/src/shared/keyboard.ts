const applePlatform =
  typeof navigator !== "undefined" && /Macintosh|Mac OS X|iPhone|iPad|iPod/.test(navigator.userAgent);

export const aiShortcut = applePlatform
  ? { label: "⌘J", ariaKeyShortcuts: "Meta+J" }
  : { label: "Ctrl J", ariaKeyShortcuts: "Control+J" };
