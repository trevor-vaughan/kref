package render

import (
	"fmt"
	"time"
)

// RelTime renders how long ago t was relative to now, as a compact staleness
// cue: "just now", "Nm ago", "Nh ago", "Nd ago", "Nw ago". It returns "" past
// eight weeks (relative age stops being useful; the caller shows the absolute
// date instead). A future t clamps to "just now".
func RelTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 8*7*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d/(7*24*time.Hour)))
	default:
		return ""
	}
}
