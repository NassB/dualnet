package procinfo

import "time"

func timeNow() time.Time                 { return time.Now() }
func sinceNow(t time.Time) time.Duration { return time.Since(t).Round(time.Millisecond) }
