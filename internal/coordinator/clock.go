package coordinator

import "time"

// clockSample is one ping/pong round trip, NTP style. t1 and t4 are the
// server's send and receive times; t2 and t3 are the worker's receive and
// send times in its own clock.
type clockSample struct {
	t1, t2, t3, t4 int64
}

// offset is the worker clock minus the server clock, assuming the network
// delay is the same in both directions.
func (s clockSample) offset() time.Duration {
	return time.Duration(((s.t2 - s.t1) + (s.t3 - s.t4)) / 2)
}

// rtt is the network round trip, excluding the worker's processing time.
func (s clockSample) rtt() time.Duration {
	return time.Duration((s.t4 - s.t1) - (s.t3 - s.t2))
}

// estimateOffset picks the sample with the lowest round trip. Queueing
// delay is what makes paths asymmetric, and the fastest exchange had the
// least of it, so its offset error (at most rtt/2) is the smallest.
func estimateOffset(samples []clockSample) (offset, rtt time.Duration, ok bool) {
	for _, s := range samples {
		if s.rtt() < 0 {
			continue // clock stepped mid-exchange
		}
		if !ok || s.rtt() < rtt {
			offset, rtt, ok = s.offset(), s.rtt(), true
		}
	}
	return offset, rtt, ok
}
