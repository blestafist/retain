const glyphs = {
  deck: <><path d="M8 3h10a3 3 0 0 1 3 3v10" /><rect x="3" y="7" width="14" height="14" rx="3" /><path d="M7 12h6m-6 4h4" /></>,
  folder: <path d="M3 7a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />,
  subdeck: <><path d="M3 7a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" /><path d="M12 11v6m-3-3h6" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  edit: <><path d="m4 16-1 5 5-1L20 8a2.1 2.1 0 0 0-4-4L4 16ZM14 6l4 4" /></>,
  delete: <path d="M4 7h16M10 11v6m4-6v6M5 7l1 13h12l1-13M9 7V4h6v3" />,
  transfer: <path d="M7 20V4m-4 4 4-4 4 4m6-4v16m-4-4 4 4 4-4" />,
  import: <path d="M12 3v12m-4-4 4 4 4-4M4 16v4h16v-4" />,
  sparkle: <><path d="m10 3 2.3 6.7L19 12l-6.7 2.3L10 21l-2.3-6.7L1 12l6.7-2.3Z" /><path d="M19 2v5m-2.5-2.5h5" /></>,
  more: <g fill="currentColor" stroke="none"><circle cx="5" cy="12" r="1.8" /><circle cx="12" cy="12" r="1.8" /><circle cx="19" cy="12" r="1.8" /></g>,
  play: <path d="M7 4.5a1 1 0 0 1 1.5-.85l11 7.5a1 1 0 0 1 0 1.7l-11 7.5A1 1 0 0 1 7 19.5Z" fill="currentColor" stroke="none" />,
  direction: <path d="M3 8h17l-4-4m5 12H4l4 4" />,
  next: <path d="m9 6 6 6-6 6" />,
  back: <path d="M20 12H4m7-7-7 7 7 7" />,
};

export function DeckGlyph({ name }: { name: keyof typeof glyphs }) {
  return <svg aria-hidden="true" focusable="false" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{glyphs[name]}</svg>;
}
