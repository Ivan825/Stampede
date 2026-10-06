// Package tui is Stampede's interactive terminal UI: slash commands, plain
// language requests turned into commands, and live run views.
package tui

import (
	"regexp"
	"strings"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// Command is a parsed slash command.
type Command struct {
	Name string
	Args []string
	// Flags hold --key value pairs.
	Flags map[string]string
}

// String renders the command as the user would type it.
func (c Command) String() string {
	parts := []string{"/" + c.Name}
	parts = append(parts, c.Args...)
	for _, k := range []string{"scenario", "target", "rate", "vus", "duration", "max", "start"} {
		if v, ok := c.Flags[k]; ok {
			parts = append(parts, "--"+k, v)
		}
	}
	return strings.Join(parts, " ")
}

// ParseSlash parses "/run spike checkout --rate 100/s".
func ParseSlash(line string) (Command, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return Command{}, false
	}
	fields := strings.Fields(line[1:])
	if len(fields) == 0 {
		return Command{}, false
	}
	c := Command{Name: strings.ToLower(fields[0]), Flags: map[string]string{}}
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		if strings.HasPrefix(f, "--") {
			k := strings.TrimPrefix(f, "--")
			if k, v, ok := strings.Cut(k, "="); ok {
				c.Flags[k] = v
				continue
			}
			if i+1 < len(fields) {
				c.Flags[k] = fields[i+1]
				i++
			} else {
				c.Flags[k] = "true"
			}
			continue
		}
		c.Args = append(c.Args, f)
	}
	return c, true
}

var (
	durationRe = regexp.MustCompile(`\b(\d+(?:\.\d+)?)\s*(s|sec|secs|seconds?|m|min|mins|minutes?|h|hr|hrs|hours?)\b`)
	rateRe     = regexp.MustCompile(`\b(\d+(?:\.\d+)?)\s*(?:/s|rps|req/s|requests? (?:per|a) second)\b`)
	usersRe    = regexp.MustCompile(`\b(\d+)\s*(?:users|vus|virtual users)\b`)
	forRe      = regexp.MustCompile(`\b(?:for|on|of)\s+(?:the\s+)?([a-z0-9][a-z0-9._-]*)(?:\s+(?:journey|scenario|flow|page|endpoint))?\b`)
)

// shapeWords maps phrases to traffic shapes, most specific first.
var shapeWords = []struct {
	words []string
	shape string
}{
	{[]string{"breaking point", "breakpoint", "where it breaks", "how much traffic", "max capacity", "maximum load"}, "breakpoint"},
	{[]string{"spike", "sudden", "flash sale", "burst"}, "spike"},
	{[]string{"soak", "endurance", "leak", "over hours", "long run"}, "soak"},
	{[]string{"stress", "beyond peak", "overload"}, "stress"},
	{[]string{"step", "steps", "increments"}, "steps"},
	{[]string{"recover", "recovery"}, "recovery"},
	{[]string{"wave", "peaks and troughs"}, "wave"},
	{[]string{"smoke", "sanity", "does it work", "quick check"}, "smoke"},
	{[]string{"baseline", "normal traffic", "normal load"}, "baseline"},
}

// Interpret turns a plain-language request into a command. It is
// deliberately simple and always shows the result for confirmation; it
// never guesses at anything it cannot name.
func Interpret(text string, scenarios []string) (Command, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	c := Command{Name: "run", Flags: map[string]string{}}
	for _, sw := range shapeWords {
		for _, w := range sw.words {
			if strings.Contains(t, w) {
				c.Args = []string{sw.shape}
				break
			}
		}
		if len(c.Args) > 0 {
			break
		}
	}
	if m := rateRe.FindStringSubmatch(t); m != nil {
		c.Flags["rate"] = m[1] + "/s"
	}
	if m := usersRe.FindStringSubmatch(t); m != nil {
		c.Flags["vus"] = m[1]
	}
	if m := durationRe.FindStringSubmatch(t); m != nil {
		unit := m[2][:1]
		if d, err := scenario.ParseDuration(m[1] + unit); err == nil {
			c.Flags["duration"] = d.String()
		}
	}
	for _, s := range scenarios {
		if strings.Contains(t, strings.ToLower(s)) {
			c.Flags["scenario"] = s
			break
		}
	}
	if _, ok := c.Flags["scenario"]; !ok {
		if m := forRe.FindStringSubmatch(t); m != nil {
			for _, s := range scenarios {
				if strings.Contains(strings.ToLower(s), m[1]) {
					c.Flags["scenario"] = s
					break
				}
			}
		}
	}
	if len(c.Args) == 0 && len(c.Flags) == 0 {
		return Command{}, false
	}
	if len(c.Args) == 0 {
		c.Args = []string{"baseline"}
		if _, hasRate := c.Flags["rate"]; !hasRate {
			if _, hasVUs := c.Flags["vus"]; !hasVUs {
				return Command{}, false
			}
		}
	}
	return c, true
}
