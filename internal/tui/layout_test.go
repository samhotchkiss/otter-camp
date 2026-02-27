package tui

import (
	"strings"
	"testing"
)

func TestResolveSizeClassBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		height   int
		wantSize SizeClass
	}{
		{name: "xs width upper boundary", width: 79, height: 30, wantSize: SizeXS},
		{name: "xs height override", width: 120, height: 22, wantSize: SizeXS},
		{name: "s lower boundary", width: 80, height: 30, wantSize: SizeS},
		{name: "s upper boundary", width: 99, height: 30, wantSize: SizeS},
		{name: "m lower boundary", width: 100, height: 30, wantSize: SizeM},
		{name: "m upper boundary", width: 139, height: 30, wantSize: SizeM},
		{name: "l lower boundary", width: 140, height: 30, wantSize: SizeL},
		{name: "l upper boundary", width: 199, height: 30, wantSize: SizeL},
		{name: "xl boundary", width: 200, height: 30, wantSize: SizeXL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSizeClass(tc.width, tc.height)
			if got != tc.wantSize {
				t.Fatalf("resolveSizeClass(%d,%d) = %s, want %s", tc.width, tc.height, got, tc.wantSize)
			}
		})
	}
}

func TestLayoutVisibilityAndHintsBySizeClass(t *testing.T) {
	cases := []struct {
		name              string
		width             int
		height            int
		focus             Panel
		sidebarVisible    bool
		wantClass         SizeClass
		wantVisible       [3]bool
		hiddenHintContain string
	}{
		{
			name:              "xs single panel",
			width:             69,
			height:            30,
			focus:             ChatPanel,
			sidebarVisible:    false,
			wantClass:         SizeXS,
			wantVisible:       [3]bool{false, false, true},
			hiddenHintContain: "sidebar hidden",
		},
		{
			name:              "s main+chat with drawer hidden",
			width:             90,
			height:            30,
			focus:             MainPanel,
			sidebarVisible:    false,
			wantClass:         SizeS,
			wantVisible:       [3]bool{false, true, true},
			hiddenHintContain: "sidebar hidden",
		},
		{
			name:              "s sidebar+chat when sidebar focused",
			width:             90,
			height:            30,
			focus:             SidebarPanel,
			sidebarVisible:    true,
			wantClass:         SizeS,
			wantVisible:       [3]bool{true, false, true},
			hiddenHintContain: "main hidden",
		},
		{
			name:              "m shows all panes",
			width:             120,
			height:            30,
			focus:             MainPanel,
			sidebarVisible:    false,
			wantClass:         SizeM,
			wantVisible:       [3]bool{true, true, true},
			hiddenHintContain: "",
		},
		{
			name:              "l all panes visible",
			width:             160,
			height:            34,
			focus:             MainPanel,
			sidebarVisible:    false,
			wantClass:         SizeL,
			wantVisible:       [3]bool{true, true, true},
			hiddenHintContain: "",
		},
		{
			name:              "xl all panes visible",
			width:             220,
			height:            40,
			focus:             MainPanel,
			sidebarVisible:    false,
			wantClass:         SizeXL,
			wantVisible:       [3]bool{true, true, true},
			hiddenHintContain: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout := computeLayout(tc.width, tc.height, tc.focus, tc.sidebarVisible, DefaultState().PanelProportions)
			if layout.sizeClass != tc.wantClass {
				t.Fatalf("size class = %s, want %s", layout.sizeClass, tc.wantClass)
			}
			if layout.visible != tc.wantVisible {
				t.Fatalf("visible panes = %v, want %v", layout.visible, tc.wantVisible)
			}
			if tc.hiddenHintContain == "" {
				if layout.hiddenHints != "" {
					t.Fatalf("hidden hints = %q, want empty", layout.hiddenHints)
				}
			} else if !strings.Contains(layout.hiddenHints, tc.hiddenHintContain) {
				t.Fatalf("hidden hints = %q, want substring %q", layout.hiddenHints, tc.hiddenHintContain)
			}
		})
	}
}

func TestLayoutSsizeSidebarFocusShowsChatPane(t *testing.T) {
	proportions := DefaultState().PanelProportions
	cases := []struct {
		name           string
		focus          Panel
		sidebarVisible bool
	}{
		{
			name:           "sidebar focus",
			focus:          SidebarPanel,
			sidebarVisible: false,
		},
		{
			name:           "sidebar visible flag",
			focus:          MainPanel,
			sidebarVisible: true,
		},
		{
			name:           "chat focus",
			focus:          ChatPanel,
			sidebarVisible: false,
		},
		{
			name:           "main focus",
			focus:          MainPanel,
			sidebarVisible: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout := computeLayout(90, 30, tc.focus, tc.sidebarVisible, proportions)
			if layout.sizeClass != SizeS {
				t.Fatalf("size class = %s, want %s", layout.sizeClass, SizeS)
			}
			if !layout.visible[ChatPanel] {
				t.Fatalf("chat panel must be visible at S size; focus=%v sidebarVisible=%t", tc.focus, tc.sidebarVisible)
			}
			if tc.focus == SidebarPanel || tc.sidebarVisible {
				if !layout.visible[SidebarPanel] {
					t.Fatalf("sidebar panel must be visible when focused or explicitly visible")
				}
				if layout.visible[MainPanel] {
					t.Fatalf("main panel must be hidden in sidebar+chat S layout")
				}
			}
		})
	}
}

func TestComputeLayoutUsesCustomProportionsWhenValid(t *testing.T) {
	proportions := [3]float64{0.30, 0.50, 0.20}
	layout := computeLayout(160, 34, MainPanel, false, proportions)

	if layout.sizeClass != SizeL {
		t.Fatalf("size class = %s, want %s", layout.sizeClass, SizeL)
	}
	if got := layout.widths[SidebarPanel]; got != 48 {
		t.Fatalf("sidebar width = %d, want 48", got)
	}
	if got := layout.widths[MainPanel]; got != 80 {
		t.Fatalf("main width = %d, want 80", got)
	}
	if got := layout.widths[ChatPanel]; got != 32 {
		t.Fatalf("chat width = %d, want 32", got)
	}
}

func TestComputeLayoutFallsBackToDefaultsWhenProportionsInvalid(t *testing.T) {
	invalid := [3]float64{0.05, 0.80, 0.15}
	layout := computeLayout(160, 34, MainPanel, false, invalid)

	if layout.sizeClass != SizeL {
		t.Fatalf("size class = %s, want %s", layout.sizeClass, SizeL)
	}
	if got := layout.widths[SidebarPanel]; got != 32 {
		t.Fatalf("sidebar width = %d, want 32", got)
	}
	if got := layout.widths[MainPanel]; got != 64 {
		t.Fatalf("main width = %d, want 64", got)
	}
	if got := layout.widths[ChatPanel]; got != 64 {
		t.Fatalf("chat width = %d, want 64", got)
	}
}
