package tools

import (
	"regexp"
	"strings"
)

// dangerousPattern pairs a human-readable name with a regex that matches a
// catastrophic command. These are rejected outright before any permission
// prompt: no amount of user approval makes `rm -rf /` or a raw-device write
// safe.
type dangerousPattern struct {
	name    string
	pattern *regexp.Regexp
}

var dangerousPatterns = []dangerousPattern{
	// rm -rf (or rm -rfv, rm --no-preserve-root -rf, ...) whose only target
	// is the filesystem root. Targets like /tmp/foo or /tmp/foo/ are fine;
	// only a bare / or /* is catastrophic.
	{
		name: "rm -rf /",
		pattern: regexp.MustCompile(
			`(?i)^\s*rm\s+(?:--no-preserve-root\s+)?-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+(?:--no-preserve-root\s+)?/[*]?\s*$`,
		),
	},
	// sudo rm -rf on anything: as root, recursive forced deletion is
	// destructive regardless of the target.
	{
		name:    "sudo rm -rf",
		pattern: regexp.MustCompile(`(?i)^\s*sudo\s+rm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\b`),
	},
	// Filesystem formatting tools.
	{
		name:    "mkfs",
		pattern: regexp.MustCompile(`(?i)\bmkfs(?:\.\w+)?\b`),
	},
	// dd writing to a raw block device (partitions included). /dev/null is
	// not matched.
	{
		name:    "dd to raw device",
		pattern: regexp.MustCompile(`(?i)\bdd\b.*\bof=/dev/(?:sd[a-z]+|hd[a-z]+|nvme\d+n\d+)`),
	},
	// Fork bombs (the classic `:(){ :|:& };:` form).
	{
		name:    "fork bomb",
		pattern: regexp.MustCompile(`(?i):\(\)\{`),
	},
	// Redirection directly onto a raw block device.
	{
		name:    "write to raw device",
		pattern: regexp.MustCompile(`(?i)>>?\s*/dev/(?:sd[a-z]+|hd[a-z]+|nvme\d+n\d+)`),
	},
}

// checkDangerousCommand reports whether the command contains a catastrophic
// command. The command is split on shell separators so `rm -rf /` is caught
// even when chained (e.g. "rm -rf / && echo done"), then each segment is
// matched against the dangerous patterns.
func checkDangerousCommand(cmd string) (string, bool) {
	for _, seg := range splitCommandSegments(cmd) {
		for _, dp := range dangerousPatterns {
			if dp.pattern.MatchString(seg) {
				return dp.name, true
			}
		}
	}
	return "", false
}

// splitCommandSegments splits a shell command into individual command
// segments on ; && || | and newlines. Quoted separators can cause a
// false positive; for a safety guard that is the acceptable direction.
func splitCommandSegments(cmd string) []string {
	return strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ';' || r == '&' || r == '|' || r == '\n'
	})
}
