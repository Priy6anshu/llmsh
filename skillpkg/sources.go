package skillpkg

// Sources renders a package's files the way a person reads them.
//
// The review screen and the publish report both show a package file by file,
// and both must show exactly what an agent will receive rather than what a
// viewer makes of it -- a zero-width character is the whole point of the
// exercise. Written twice they would drift, and the half that drifted would be
// the one nobody was looking at when it mattered.
//
// Reading is the caller's job because the two have their bytes in different
// places: api holds an unpacked archive in memory, ingest has just written the
// same files to a temp directory.

// A file larger than this is reported by size rather than sent. The cap is the
// file viewer's, so the same file reads the same way on every screen.
const MaxSourceBytes = 512 << 10

// And a budget for the package as a whole, so one response cannot carry the
// entire uncompressed limit.
const MaxSourcesBytes = 2 << 20

// Source is one file's contents, escaped.
type Source struct {
	IsText bool `json:"is_text"`
	// Truncated means the file exists and is readable, but is too large to
	// send. The size is already in the file list, so nothing is lost but the
	// body.
	Truncated bool   `json:"truncated,omitempty"`
	Content   string `json:"content,omitempty"`
	// Hidden reports characters that do not render as themselves, so a reader
	// can tell the author's words from something hiding among them.
	Hidden []HiddenRune `json:"hidden,omitempty"`
}

// Sources reads every file it can afford to and reveals what is hiding in it.
//
// A file that cannot be read is omitted rather than reported as empty: the file
// list is what says the package contains it, and an empty pane would claim the
// author wrote nothing there.
func Sources(files []FileEntry, read func(f FileEntry) ([]byte, error)) map[string]Source {
	out := make(map[string]Source, len(files))
	budget := int64(MaxSourcesBytes)

	for _, f := range files {
		if f.Size > MaxSourceBytes || f.Size > budget {
			out[f.Path] = Source{IsText: true, Truncated: true}
			continue
		}
		b, err := read(f)
		if err != nil {
			continue
		}
		s := Source{IsText: IsText(b)}
		if s.IsText {
			// Only text spends the budget. A binary file carries no body, so
			// an icon-heavy package does not crowd out the instructions.
			budget -= int64(len(b))
			s.Content = RevealHidden(string(b))
			s.Hidden = HiddenRunes(string(b))
		}
		out[f.Path] = s
	}
	return out
}
