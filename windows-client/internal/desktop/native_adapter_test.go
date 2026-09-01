package desktop

import "testing"

func TestExistingApplicationLoopTrayRegistersCallbacksWithoutStartingAnotherLoop(t *testing.T) {
	t.Parallel()

	registerCalls := 0
	readyCalls := 0
	tray := existingApplicationLoopTray{
		register: func(onReady, onExit func()) {
			registerCalls++
			if onReady == nil {
				t.Fatal("register onReady callback = nil, want callback")
			}
			if onExit == nil {
				t.Fatal("register onExit callback = nil, want callback")
			}
			onReady()
		},
	}

	tray.Start(func() { readyCalls++ })

	if registerCalls != 1 {
		t.Fatalf("register calls = %d, want 1", registerCalls)
	}
	if readyCalls != 1 {
		t.Fatalf("ready calls = %d, want 1", readyCalls)
	}
}
