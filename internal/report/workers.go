package report

// WorkerRow is one worker's part in a distributed run. Times are seconds
// since the run started, like the timeline.
type WorkerRow struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Region     string  `json:"region,omitempty"`
	ShareLo    float64 `json:"shareLo"`
	ShareHi    float64 `json:"shareHi"`
	State      string  `json:"state"`
	StopReason string  `json:"stopReason,omitempty"`
	Error      string  `json:"error,omitempty"`
	PeakVUs    int     `json:"peakVUs"`
	Requests   uint64  `json:"requests"`
	// Saturated lists the windows in which the worker reported itself
	// saturated (CPU, scheduling lag, GC pauses, file descriptors or
	// dropped iterations); latency measured there may reflect the
	// generator rather than the target.
	Saturated         []Span   `json:"saturated,omitempty"`
	SaturationReasons []string `json:"saturationReasons,omitempty"`
	// Lost is the window from the worker's loss to the end of the run,
	// during which its share of the load was not generated.
	Lost *Span `json:"lost,omitempty"`
	// ClockOffset is the worker's measured clock offset in seconds.
	ClockOffset float64 `json:"clockOffset"`
}

// Span is a time window in seconds since the start; To is exclusive.
type Span struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
}
