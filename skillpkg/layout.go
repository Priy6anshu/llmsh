package skillpkg

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

// This file answers a question the rest of the inspection cannot: is this one
// archive, or is it several wearing a trench coat?
//
// Everything else we check is about the entries a zip declares. These checks
// are about the file around them — the bytes before the first entry, the bytes
// after the directory, and whether two entries claim the same bytes. Each is a
// way to build a file that two readers disagree about, and disagreement is the
// whole attack: the scanner reads one archive, the person reviewing it reads
// another, and the agent installing it reads a third.
//
// docs/11 committed to all three. They were the part of that document that had
// not been built.

const (
	eocdSignature    = 0x06054b50
	localHeaderSig   = 0x04034b50
	eocdMinLen       = 22
	maxCommentLen    = 1 << 16
	localHeaderFixed = 30 // bytes before the filename in a local file header
)

// checkArchiveShape inspects the archive as a file rather than as a list of entries.
func checkArchiveShape(ra io.ReaderAt, size int64, res *Result) {
	eocdOff, commentLen, ok := findEOCD(ra, size)
	if !ok {
		// zip.NewReader has already failed or will; nothing useful to add.
		return
	}

	// Trailing garbage. The end-of-central-directory record carries its own
	// comment length, so the archive states exactly where it ends. Anything
	// past that is a second file sharing the name of the first, and which one
	// a reader sees depends on whether it scans forward or backward for the
	// EOCD. Two readers, two archives, one review.
	checkPrepended(ra, size, res)

	declaredEnd := eocdOff + eocdMinLen + int64(commentLen)
	if declaredEnd < size {
		res.Add(SeverityError, "trailing_data",
			fmt.Sprintf("%d bytes follow the end of the archive", size-declaredEnd),
			Hint("Two zip readers can disagree about which archive this file contains. Re-create it with a standard zip writer."))
	}
}

// checkEntrySpans runs once the entries are known: where their data actually
// sits in the file.
func checkEntrySpans(spans []entryExtent, size int64, res *Result) {
	if len(spans) == 0 {
		return
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	// Overlapping entries. Two entries whose compressed data occupies the same
	// bytes is how a zip quine and the 42.zip family get their amplification:
	// the declared total is small, the expanded total is not, and every
	// per-entry limit is satisfied because each entry really is small. It is
	// also unrepresentable by any honest writer.
	for i := 1; i < len(spans); i++ {
		prev, cur := spans[i-1], spans[i]
		if cur.start < prev.end {
			res.Add(SeverityError, "overlapping_entries",
				fmt.Sprintf("%s and %s claim the same bytes in the file", prev.name, cur.name),
				At(cur.name, 0),
				Hint("No zip writer produces this. It is how an archive expands to far more than it declares."))
			return // one report is enough; the archive is refused either way
		}
	}
}

// entryExtent is where one entry's compressed data actually lives.
type entryExtent struct {
	name  string
	start int64
	end   int64
}

// checkPrepended refuses an archive that does not begin with its first entry.
//
// A zip's readers locate entries from the central directory, so the entries can
// legally start anywhere — which is what makes a file that is simultaneously a
// valid image and a valid zip possible. We have no use for one, and a polyglot
// is a file that means a different thing to whatever opens it.
func checkPrepended(ra io.ReaderAt, size int64, res *Result) {
	if size < 4 {
		return
	}
	var sig [4]byte
	if _, err := ra.ReadAt(sig[:], 0); err != nil {
		return
	}
	if binary.LittleEndian.Uint32(sig[:]) == localHeaderSig {
		return
	}
	res.Add(SeverityError, "prepended_data",
		"the archive does not begin with its first entry",
		Hint("Something is in front of the zip. A file that is both an archive and something else is not reviewable as either."))
}

// findEOCD locates the end-of-central-directory record and its comment length.
//
// It scans backwards the way every zip reader does, which is also why trailing
// data is dangerous: a reader that scans forward finds a different record.
func findEOCD(ra io.ReaderAt, size int64) (offset int64, commentLen uint16, ok bool) {
	span := int64(maxCommentLen + eocdMinLen)
	if span > size {
		span = size
	}
	buf := make([]byte, span)
	if _, err := ra.ReadAt(buf, size-span); err != nil {
		return 0, 0, false
	}
	for i := len(buf) - eocdMinLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(buf[i:i+4]) != eocdSignature {
			continue
		}
		cl := binary.LittleEndian.Uint16(buf[i+20 : i+22])
		return size - span + int64(i), cl, true
	}
	return 0, 0, false
}
