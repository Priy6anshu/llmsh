package skillpkg

import "testing"

// The working copy's own marker must never ship.
//
// It carried the publisher's API URL and the skill they had cloned from, and it
// went out in every package published from a working copy -- because the strip
// list named ".aq", which is what this directory was called before the tool was
// renamed, and nothing updated it. The symptom was visible the whole time and
// read as something else: an unrecognized_layout warning naming a file the
// publisher had never created.
func TestTheWorkingCopyMarkerIsStripped(t *testing.T) {
	for _, rel := range []string{
		".llmsh/origin.json",
		".llmsh/anything",
		"nested/.llmsh/origin.json",
		".aq/origin.json", // the old name, for packages published in between
	} {
		if !isStripped(rel) {
			t.Errorf("%s should be stripped, and would have shipped", rel)
		}
	}
	// Not over-eager: a file that merely starts the same way is somebody's.
	for _, rel := range []string{"llmsh.md", ".llmshrc", "docs/.llmsh-notes.md"} {
		if isStripped(rel) {
			t.Errorf("%s is not the marker directory and must survive", rel)
		}
	}
}
