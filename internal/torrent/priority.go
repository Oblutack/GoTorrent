package torrent

import (
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/picker"
)

// numFiles is how many entries a per-file priority (or skip) slice needs:
// one per FileInfo for a multi-file torrent, exactly one for a single-file
// one — mi.Info.Files is empty in that case, but there is still exactly one
// file to have an opinion about.
func numFiles(mi *metainfo.MetaInfo) int {
	if mi.Info.IsMultiFile() {
		return len(mi.Info.Files)
	}
	return 1
}

// fileLengths returns each file's length, in the same order numFiles counts
// them.
func fileLengths(mi *metainfo.MetaInfo) []int64 {
	if mi.Info.IsMultiFile() {
		out := make([]int64, len(mi.Info.Files))
		for i, f := range mi.Info.Files {
			out[i] = f.Length
		}
		return out
	}
	return []int64{mi.Info.Length}
}

// paddingFlags reports, index-aligned with numFiles(mi), which files are
// BEP 47 padding files (FileInfo.IsPadding) — always all-false for a
// single-file torrent, which has no file list to carry the attr at all.
func paddingFlags(mi *metainfo.MetaInfo) []bool {
	n := numFiles(mi)
	flags := make([]bool, n)
	if mi.Info.IsMultiFile() {
		for i, f := range mi.Info.Files {
			flags[i] = f.IsPadding()
		}
	}
	return flags
}

// normalizedFilePriorities fills in a default for any file a caller's slice
// didn't cover (including an empty slice, the "no file selection at all"
// default) and truncates anything longer than the real file count, so every
// other function here can assume exactly numFiles(mi) entries. The default
// is PrioritySkip for a BEP 47 padding file and PriorityNormal for
// everything else — a caller's own explicit value always wins (copy runs
// last), same "caller's value wins" precedence FilePriorities/WebSeeds
// normalization already follows elsewhere in this codebase.
func normalizedFilePriorities(mi *metainfo.MetaInfo, filePriorities []picker.Priority) []picker.Priority {
	n := numFiles(mi)
	out := make([]picker.Priority, n)
	pad := paddingFlags(mi)
	for i := range out {
		if pad[i] {
			out[i] = picker.PrioritySkip
		} else {
			out[i] = picker.PriorityNormal
		}
	}
	copy(out, filePriorities)
	return out
}

// piecePriorities computes each piece's effective priority as the highest
// priority of any file it overlaps. This is what makes "partial-piece
// handling at skipped-file boundaries" correct without any special case at
// all: a piece straddling a skipped file and a wanted one takes the wanted
// file's priority, since BitTorrent pieces are atomic — there is no such
// thing as downloading 60% of one — and "skip" only ever wins a piece when
// every file touching it is also skip.
func piecePriorities(mi *metainfo.MetaInfo, filePriorities []picker.Priority) []picker.Priority {
	priorities := normalizedFilePriorities(mi, filePriorities)
	lengths := fileLengths(mi)

	type fileSpan struct {
		start, end int64
		priority   picker.Priority
	}
	spans := make([]fileSpan, len(lengths))
	var offset int64
	for i, length := range lengths {
		spans[i] = fileSpan{start: offset, end: offset + length, priority: priorities[i]}
		offset += length
	}

	n := mi.NumPieces()
	out := make([]picker.Priority, n)
	si := 0
	for i := 0; i < n; i++ {
		pieceStart := int64(i) * mi.Info.PieceLength
		pieceEnd := pieceStart + mi.PieceLen(i)

		for si < len(spans) && spans[si].end <= pieceStart {
			si++
		}
		best := picker.PrioritySkip
		for j := si; j < len(spans) && spans[j].start < pieceEnd; j++ {
			if spans[j].priority > best {
				best = spans[j].priority
			}
		}
		out[i] = best
	}
	return out
}

// filesNeedingAllocation reports, index-aligned with numFiles(mi), which
// files must actually exist on disk: either because their own priority
// wants them, or because some piece straddling their byte range is going
// to be downloaded anyway (piecePriorities already lets a wanted piece
// "win" over a neighboring skipped file — a piece is atomic, there's no
// such thing as downloading 60% of one — see its own doc comment) and
// storage.WriteAt needs that skipped file's own region to exist to write
// into it.
//
// This closes a real, general gap that predates BEP 47: nothing previously
// allocated a skipped file just because a wanted piece happened to share a
// boundary with it, so WriteAt would fail forever for that file's own
// byte range the moment such a piece arrived — a bug no existing test
// happened to trigger, because every skip scenario tested so far used a
// piece-aligned skipped file. BEP 47 padding files (defaulted to Skip by
// normalizedFilePriorities) hit this on almost every multi-file torrent
// that has one, since a pad file's whole reason for existing is sitting
// right at a wanted file's own piece boundary.
func filesNeedingAllocation(mi *metainfo.MetaInfo, filePriorities []picker.Priority, pp []picker.Priority) []bool {
	lengths := fileLengths(mi)
	need := make([]bool, len(lengths))
	var offset int64
	for i, length := range lengths {
		if filePriorities[i] != picker.PrioritySkip {
			need[i] = true
			offset += length
			continue
		}
		if length > 0 {
			firstPiece := int(offset / mi.Info.PieceLength)
			lastPiece := int((offset + length - 1) / mi.Info.PieceLength)
			for p := firstPiece; p <= lastPiece && p < len(pp); p++ {
				if pp[p] != picker.PrioritySkip {
					need[i] = true
					break
				}
			}
		}
		offset += length
	}
	return need
}

// boostFirstAndLastPiece raises the first and last piece of every non-skip
// file to PriorityHigh in place, on top of whatever piecePriorities already
// computed — 3.1's "first-and-last-piece-first" (makes a video file's start
// and end arrive early enough to preview while the rest is still coming
// in). A file's own priority is never lowered by this — only ever raised —
// and a skipped file's pieces are left alone entirely, same as everywhere
// else priority is derived: skip means skip.
func boostFirstAndLastPiece(mi *metainfo.MetaInfo, filePriorities []picker.Priority, priorities []picker.Priority) {
	lengths := fileLengths(mi)
	var offset int64
	for i, length := range lengths {
		if length > 0 && filePriorities[i] != picker.PrioritySkip {
			first := int(offset / mi.Info.PieceLength)
			last := int((offset + length - 1) / mi.Info.PieceLength)
			if priorities[first] < picker.PriorityHigh {
				priorities[first] = picker.PriorityHigh
			}
			if priorities[last] < picker.PriorityHigh {
				priorities[last] = picker.PriorityHigh
			}
		}
		offset += length
	}
}
