package capacity_test

import (
	"testing"

	"mobile-egress/internal/capacity"
)

func TestProductionCapacityContract(t *testing.T) {
	if capacity.ClientMaxConcurrentStreams != 256 || capacity.AgentMaxConcurrentStreams != 256 {
		t.Fatal("stream contract drifted")
	}
	if capacity.DataFramesPerStream != 32 || capacity.DataFramesPerLane != 8_192 || capacity.DataBytesPerLane != 64<<20 {
		t.Fatal("data-lane contract drifted")
	}
	if capacity.ControlFramesPerSession != 512 || capacity.StreamTombstones != 1_024 {
		t.Fatal("control or tombstone contract drifted")
	}
}
