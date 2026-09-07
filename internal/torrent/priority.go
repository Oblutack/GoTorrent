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

// normalizedFilePriorities fills in PriorityNormal for any file a caller's
// slice didn't cover (including an empty slice, the "no file selection at
// all" default) and truncates anything longer than the real file count, so
// every other function here can assume exactly numFiles(mi) entries.
func normalizedFilePriorities(mi *metainfo.MetaInfo, filePriorities []picker.Priority) []picker.Priority {
	n := numFiles(mi)
	out := make([]picker.Priority, n)
	for i := range out {
		out[i] = picker.PriorityNormal
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
