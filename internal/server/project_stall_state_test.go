package server

import "testing"

func TestTerminalProjectTaskReasonInfersImpossibleLiveState(t *testing.T) {
	row := projectTerminalStallTaskRow{
		WorkStatus:             "blocked",
		FlowBacked:             true,
		CurrentFlowNodeMissing: true,
		RuntimeStatus:          "",
	}

	got := terminalProjectTaskReason(row)
	want := "automatic repair blocked impossible live task state: flow-backed task lost runtime owner and active execution"
	if got != want {
		t.Fatalf("terminalProjectTaskReason() = %q, want %q", got, want)
	}
}
