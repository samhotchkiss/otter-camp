package tui

import (
	"fmt"
	"strings"
)

type SizeClass string

const (
	SizeXS SizeClass = "XS"
	SizeS  SizeClass = "S"
	SizeM  SizeClass = "M"
	SizeL  SizeClass = "L"
	SizeXL SizeClass = "XL"
)

type layoutState struct {
	sizeClass   SizeClass
	visible     [3]bool
	widths      [3]int
	gutters     int
	contentSize int
	focusOrder  []Panel
	hiddenHints string
}

func resolveSizeClass(width, height int) SizeClass {
	w, h := normalizeDimensions(width, height)
	switch {
	case w < 80 || h <= 22:
		return SizeXS
	case w < 100:
		return SizeS
	case w < 140:
		return SizeM
	case w <= 199:
		return SizeL
	default:
		return SizeXL
	}
}

func normalizeDimensions(width, height int) (int, int) {
	w := width
	if w <= 0 {
		w = 120
	}
	h := height
	if h <= 0 {
		h = 30
	}
	return w, h
}

func computeLayout(width, height int, focus Panel, sidebarVisible bool, proportions [3]float64) layoutState {
	w, h := normalizeDimensions(width, height)
	class := resolveSizeClass(w, h)
	contentWidth := w
	gutter := 0

	visible := [3]bool{}
	weights := proportions
	switch class {
	case SizeXS:
		visible[focus] = true
		weights = [3]float64{1, 1, 1}
	case SizeS:
		if focus == SidebarPanel || sidebarVisible {
			visible[SidebarPanel] = true
			visible[ChatPanel] = true
			weights = [3]float64{0.40, 0, 0.60}
		} else {
			visible[MainPanel] = true
			visible[ChatPanel] = true
			weights = [3]float64{0, 0.58, 0.42}
		}
	case SizeM:
		visible[SidebarPanel] = true
		visible[MainPanel] = true
		visible[ChatPanel] = true
		if w < 120 {
			weights = [3]float64{0.12, 0.50, 0.38}
		} else {
			weights = [3]float64{0.18, 0.46, 0.36}
		}
	case SizeL, SizeXL:
		visible[SidebarPanel] = true
		visible[MainPanel] = true
		visible[ChatPanel] = true
		weights = [3]float64{0.18, 0.50, 0.32}
	default:
		visible[MainPanel] = true
		visible[ChatPanel] = true
		weights = [3]float64{0, 0.6, 0.4}
	}

	layout := layoutState{
		sizeClass:   class,
		visible:     visible,
		widths:      partitionWidths(contentWidth, weights, visible),
		gutters:     gutter,
		contentSize: contentWidth,
		hiddenHints: hiddenPaneHints(visible),
	}
	layout.focusOrder = focusOrderForLayout(layout, sidebarVisible)
	return layout
}

func focusOrderForLayout(layout layoutState, sidebarVisible bool) []Panel {
	switch layout.sizeClass {
	case SizeM:
		if !layout.visible[SidebarPanel] && !sidebarVisible {
			return []Panel{MainPanel, ChatPanel}
		}
		return []Panel{SidebarPanel, MainPanel, ChatPanel}
	case SizeS:
		return []Panel{MainPanel, ChatPanel, SidebarPanel}
	default:
		return []Panel{SidebarPanel, MainPanel, ChatPanel}
	}
}

func normalizeFocus(layout layoutState, focus Panel) Panel {
	if layout.visible[focus] {
		return focus
	}
	for _, candidate := range []Panel{MainPanel, ChatPanel, SidebarPanel} {
		if layout.visible[candidate] {
			return candidate
		}
	}
	return MainPanel
}

func nextPanelInOrder(order []Panel, current Panel) Panel {
	if len(order) == 0 {
		return MainPanel
	}
	for i, panel := range order {
		if panel == current {
			return order[(i+1)%len(order)]
		}
	}
	return order[0]
}

func previousPanelInOrder(order []Panel, current Panel) Panel {
	if len(order) == 0 {
		return MainPanel
	}
	for i, panel := range order {
		if panel == current {
			if i == 0 {
				return order[len(order)-1]
			}
			return order[i-1]
		}
	}
	return order[0]
}

func partitionWidths(total int, weights [3]float64, visible [3]bool) [3]int {
	var indices []int
	for i, on := range visible {
		if on {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 || total <= 0 {
		return [3]int{}
	}
	if len(indices) == 1 {
		var single [3]int
		single[indices[0]] = total
		return single
	}

	sum := 0.0
	for _, idx := range indices {
		weight := weights[idx]
		if weight <= 0 {
			weight = 1
		}
		sum += weight
	}
	if sum <= 0 {
		sum = float64(len(indices))
	}

	remaining := total
	var widths [3]int
	for i, idx := range indices {
		if i == len(indices)-1 {
			widths[idx] = remaining
			break
		}

		weight := weights[idx]
		if weight <= 0 {
			weight = 1
		}
		width := int((weight / sum) * float64(total))
		if width < 1 {
			width = 1
		}

		minRemaining := len(indices) - i - 1
		maxCurrent := remaining - minRemaining
		if width > maxCurrent {
			width = maxCurrent
		}
		widths[idx] = width
		remaining -= width
	}

	return widths
}

func hiddenPaneHints(visible [3]bool) string {
	hints := make([]string, 0, 3)
	if !visible[SidebarPanel] {
		hints = append(hints, "sidebar hidden (1 or :focus sidebar)")
	}
	if !visible[MainPanel] {
		hints = append(hints, "main hidden (2 or :focus main)")
	}
	if !visible[ChatPanel] {
		hints = append(hints, "chat hidden (3 or :focus chat)")
	}
	if len(hints) == 0 {
		return ""
	}
	return "Hidden panes: " + strings.Join(hints, "; ")
}

func renderPanelHeader(name string, focused bool, width int, visible bool) string {
	if !visible || width <= 0 {
		return "[" + name + ":hidden]"
	}

	label := name
	if focused {
		label = "*" + name + "*"
	}
	text := "[" + label + "]"
	if width <= len(text) {
		return text
	}
	padding := width - len(text)
	return text + strings.Repeat(" ", padding)
}

func describeLayoutForShell(class SizeClass, shell string, layout layoutState) string {
	return fmt.Sprintf(
		"size=%s shell=%s visible(sidebar=%t,main=%t,chat=%t) widths=%v",
		class,
		shell,
		layout.visible[SidebarPanel],
		layout.visible[MainPanel],
		layout.visible[ChatPanel],
		layout.widths,
	)
}

func snapshotDimensions(class SizeClass) (int, int) {
	switch class {
	case SizeXS:
		return 69, 22
	case SizeS:
		return 90, 30
	case SizeM:
		return 120, 30
	case SizeL:
		return 160, 34
	default:
		return 220, 40
	}
}
