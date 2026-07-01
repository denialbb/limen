package tui

// NewTerminalTheme returns a theme that uses only ANSI 0-15 color indices,
// so all colors are resolved from the terminal emulator's own palette.
// No hex or 256-color values — the terminal's configured colorscheme drives
// everything.
func NewTerminalTheme() *Theme {
	return &Theme{
		HeaderBgColor:    "",  // terminal default background
		HeaderFgColor:    "7", // terminal white/foreground
		HeaderPadH:       1,
		HeaderFieldColor: "8", // bright black (dim)
		HeaderStateColor: "5", // magenta
		HeaderCountColor: "8", // bright black (dim)

		TabActiveBgColor: "5",  // magenta
		TabActiveFgColor: "0",  // black
		TabInactiveColor: "8",  // bright black (dim)
		TabPadH:          1,
		TabBoundaryPad:   2,

		SeparatorColor: "8",  // bright black
		SeparatorRune:  "─",
		SeparatorPadV:  0,

		FooterBgColor: "8", // bright black bg
		FooterFgColor: "3", // yellow

		SplitWidthThreshold:   120,
		SplitHeightThreshold:  30,
		SplitLeftWidthPct:     0.30,
		SplitRouterHeightPct:  0.35,
		SplitWorkersHeightPct: 0.30,
		SplitDividerColor:     "8", // bright black
		SplitPanelTitleColor:  "8", // bright black

		TimestampColor: "8", // bright black (dim timestamp)
		EventTextColor: "",  // terminal default foreground
		KeywordColors: map[string]string{
			"PASS":      "2",  // green
			"FAIL":      "1",  // red
			"APPROVED":  "2",  // green
			"REVISION":  "3",  // yellow
			"PROCEED":   "2",  // green
			"ABORT":     "1",  // red
			"CONFLICT":  "3",  // yellow
			"FINALIZED": "6",  // cyan
			"CRITICAL":  "1",  // red
			"WARNING":   "3",  // yellow
			"DONE":      "2",  // green
			"COMMITTED": "2",  // green
		},
	}
}
