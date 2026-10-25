package spike

import "time"

// stampDuration converts the difference of two stamp readings.
func stampDuration(d int64) time.Duration { return ticksDuration(d, stampFreq()) }

// ticksDuration converts ticks of a clock that counts freq per second,
// without overflowing for durations up to the time.Duration limit.
func ticksDuration(ticks, freq int64) time.Duration {
	s := int64(time.Second)
	return time.Duration(ticks/freq*s + ticks%freq*s/freq)
}
