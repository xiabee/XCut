// Package architecture holds no production code. Its tests are static guards for the
// AGENTS.md rules that a reviewer cannot catch by reading a diff: "no shell, ever" and
// "analyzers never generate FFmpeg commands" are both properties of the tree as a whole,
// and both are the kind of thing that regresses one plausible line at a time.
//
// The pattern is the one docs/SECURITY.md's UI rule uses: classify source text with a
// pure function, drive that function with cases that must fire and cases that must not
// (so a classifier that matches nothing is caught by the test, not by a future incident),
// and refuse to report a pass on a scan that read nothing.
package architecture
