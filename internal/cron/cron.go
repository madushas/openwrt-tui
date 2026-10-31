// Package cron edits a crontab as text: it lists jobs, adds and removes them,
// switches them off and on, and builds the jobs for common tasks. Jobs added
// here carry a tag so they can be recognised later. It never talks to the
// router.
package cron

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	tagPrefix = " # owrt-tui:"
	offPrefix = "#off# "
)

// Job is one line of the crontab that is a job (active or switched off).
type Job struct {
	Line     int // index in the crontab's lines
	Schedule string
	Command  string
	Off      bool
	Tag      string // set for jobs created by owrt-tui
}

// Lines splits a crontab into lines (without the final empty one).
func Lines(text string) []string {
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// Text joins lines back into a crontab.
func Text(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

var envLine = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*=`)

func split(line string) (sched, cmd string, ok bool) {
	f := strings.Fields(line)
	if len(f) < 6 || envLine.MatchString(line) {
		return "", "", false
	}
	if err := ValidSchedule(strings.Join(f[:5], " ")); err != nil {
		return "", "", false
	}
	rest := line
	for i := 0; i < 5; i++ {
		rest = strings.TrimLeft(rest, " \t")
		rest = rest[strings.IndexAny(rest, " \t"):]
	}
	return strings.Join(f[:5], " "), strings.TrimSpace(rest), true
}

// Parse lists the jobs in a crontab. Other lines (comments, variables) are
// not jobs and are left alone by every edit.
func Parse(text string) []Job {
	var out []Job
	for i, l := range Lines(text) {
		off := false
		body := l
		if strings.HasPrefix(l, offPrefix) {
			off, body = true, strings.TrimPrefix(l, offPrefix)
		} else if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		s, c, ok := split(body)
		if !ok {
			continue
		}
		j := Job{Line: i, Schedule: s, Command: c, Off: off}
		if k := strings.Index(c, strings.TrimSpace(tagPrefix)); k >= 0 {
			j.Tag = strings.TrimSpace(c[k+len(strings.TrimSpace(tagPrefix)):])
			j.Command = strings.TrimSpace(c[:k])
		}
		out = append(out, j)
	}
	return out
}

var fieldRe = regexp.MustCompile(`^(\*|\d+(-\d+)?)(/\d+)?(,(\*|\d+(-\d+)?)(/\d+)?)*$`)

// ValidSchedule checks a five-field schedule (numbers, *, ranges, steps and lists).
func ValidSchedule(s string) error {
	f := strings.Fields(s)
	if len(f) != 5 {
		return fmt.Errorf("a schedule has five fields: minute hour day-of-month month day-of-week")
	}
	max := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	names := [5]string{"minute", "hour", "day of month", "month", "day of week"}
	for i, x := range f {
		if !fieldRe.MatchString(x) {
			return fmt.Errorf("%s: %q is not valid (use numbers, *, ranges like 1-5, lists like 1,3 and steps like */15)", names[i], x)
		}
		for _, n := range regexp.MustCompile(`\d+`).FindAllString(strings.SplitN(x, "/", 2)[0], -1) {
			v, _ := strconv.Atoi(n)
			if v < max[i][0] || v > max[i][1] {
				return fmt.Errorf("%s: %d is out of range %d-%d", names[i], v, max[i][0], max[i][1])
			}
		}
		if strings.Contains(x, "/") {
			if st, _ := strconv.Atoi(x[strings.Index(x, "/")+1:]); st == 0 {
				return fmt.Errorf("%s: step cannot be 0", names[i])
			}
		}
	}
	return nil
}

// ValidCommand checks the command part.
func ValidCommand(c string) error {
	c = strings.TrimSpace(c)
	if c == "" {
		return fmt.Errorf("command is empty")
	}
	if strings.ContainsAny(c, "\n\r\x00") {
		return fmt.Errorf("command must be one line")
	}
	if strings.Contains(c, "# owrt-tui:") {
		return fmt.Errorf("command may not contain the owrt-tui tag")
	}
	if strings.HasPrefix(c, "#") {
		return fmt.Errorf("command may not start with #")
	}
	return nil
}

var tagRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// Add appends a job. tag may be empty; with a tag the same tag cannot be added twice.
func Add(text, schedule, command, tag string) (string, error) {
	if err := ValidSchedule(schedule); err != nil {
		return "", err
	}
	if err := ValidCommand(command); err != nil {
		return "", err
	}
	line := strings.Join(strings.Fields(schedule), " ") + " " + strings.TrimSpace(command)
	if tag != "" {
		if !tagRe.MatchString(tag) {
			return "", fmt.Errorf("invalid tag")
		}
		for _, j := range Parse(text) {
			if j.Tag == tag {
				return "", fmt.Errorf("this job already exists (line %d); remove it first to change it", j.Line+1)
			}
		}
		line += tagPrefix + tag
	}
	return Text(append(Lines(text), line)), nil
}

func lineOf(lines []string, line int) error {
	if line < 0 || line >= len(lines) {
		return fmt.Errorf("no such line")
	}
	return nil
}

// Remove deletes the job on a line.
func Remove(text string, line int) (string, error) {
	ls := Lines(text)
	if err := lineOf(ls, line); err != nil {
		return "", err
	}
	if !isJob(ls[line]) {
		return "", fmt.Errorf("that line is not a job")
	}
	return Text(append(ls[:line:line], ls[line+1:]...)), nil
}

func isJob(l string) bool {
	for _, j := range Parse(l + "\n") {
		_ = j
		return true
	}
	return false
}

// Toggle switches a job off (keeps it, commented) or back on.
func Toggle(text string, line int) (string, error) {
	ls := Lines(text)
	if err := lineOf(ls, line); err != nil {
		return "", err
	}
	if !isJob(ls[line]) {
		return "", fmt.Errorf("that line is not a job")
	}
	if strings.HasPrefix(ls[line], offPrefix) {
		ls[line] = strings.TrimPrefix(ls[line], offPrefix)
	} else {
		ls[line] = offPrefix + ls[line]
	}
	return Text(ls), nil
}

// ---- presets ----

var radioRe = regexp.MustCompile(`^radio[0-9]{1,2}$`)

// Reboot is a nightly (or weekly) reboot at hh:mm. dow is "*" for every day,
// or a day-of-week such as "0" for Sunday.
func Reboot(hour, min int, dow string) (schedule, command, tag string, err error) {
	if hour < 0 || hour > 23 || min < 0 || min > 59 {
		return "", "", "", fmt.Errorf("time must be 00:00 to 23:59")
	}
	s := fmt.Sprintf("%d %d * * %s", min, hour, dow)
	if err := ValidSchedule(s); err != nil {
		return "", "", "", err
	}
	return s, "sleep 30 && reboot", "reboot", nil
}

// WifiSchedule switches a radio off at one time and on at another (runtime
// only: nothing is written to flash, and a reboot brings Wi-Fi back).
func WifiSchedule(radio string, offH, offM, onH, onM int) (jobs [2][3]string, err error) {
	if !radioRe.MatchString(radio) {
		return jobs, fmt.Errorf("invalid radio %q", radio)
	}
	for _, v := range [][2]int{{offH, offM}, {onH, onM}} {
		if v[0] < 0 || v[0] > 23 || v[1] < 0 || v[1] > 59 {
			return jobs, fmt.Errorf("time must be 00:00 to 23:59")
		}
	}
	if offH == onH && offM == onM {
		return jobs, fmt.Errorf("the off and on times are the same")
	}
	jobs[0] = [3]string{fmt.Sprintf("%d %d * * *", offM, offH), "wifi down " + radio, "wifi-off-" + radio}
	jobs[1] = [3]string{fmt.Sprintf("%d %d * * *", onM, onH), "wifi up " + radio, "wifi-on-" + radio}
	return jobs, nil
}

var flashRe = regexp.MustCompile(`>>?\s*(/[^\s;&|]*)`)

// FlashWrite reports the path when a command redirects output somewhere on
// flash. Frequent jobs doing this wear the flash out.
func FlashWrite(command string) string {
	for _, m := range flashRe.FindAllStringSubmatch(command, -1) {
		p := m[1]
		if strings.HasPrefix(p, "/tmp") || strings.HasPrefix(p, "/dev/") || strings.HasPrefix(p, "/var/") || strings.HasPrefix(p, "/proc/") {
			continue
		}
		return p
	}
	return ""
}

// Frequent reports whether a schedule fires at least every 15 minutes.
func Frequent(schedule string) bool {
	f := strings.Fields(schedule)
	if len(f) != 5 {
		return false
	}
	if f[0] == "*" {
		return true
	}
	if strings.HasPrefix(f[0], "*/") {
		n, err := strconv.Atoi(f[0][2:])
		return err == nil && n <= 15
	}
	return false
}
