package capacity

const (
	ResolverWorkers         = 256
	DataFramesPerStream     = 32
	DataFramesPerLane       = 8_192
	DataBytesPerLane        = 64 << 20
	ControlFramesPerSession = 512
	StreamTombstones        = 1_024
)
