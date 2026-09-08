package metainfo

import "github.com/Oblutack/GoTorrent/internal/bencode"

// Export rebuilds a standalone .torrent file's raw bytes from mi — its own
// InfoBytes verbatim (this is what keeps InfoHash correct: never a
// re-encoding) plus Announce/AnnounceList/Comment/CreatedBy/CreationDate
// as already known — for a torrent that didn't come from a .torrent file
// in the first place (a magnet, whose mi only ever has an info dict, no
// top-level fields at all) or one whose tracker list has grown since
// (Torrent.AddTracker). extraTrackers is merged in as additional
// announce-list tiers, deduplicated against whatever mi.Announce/
// AnnounceList already has — so calling this on an ordinarily
// Load-from-file torrent with no extraTrackers just reconstructs the same
// tracker list, unchanged.
func Export(mi *MetaInfo, extraTrackers []string) ([]byte, error) {
	if mi == nil {
		return nil, ErrNoMetadata
	}

	tf := torrentFile{
		Announce:     mi.Announce,
		AnnounceList: mi.AnnounceList,
		Comment:      mi.Comment,
		CreatedBy:    mi.CreatedBy,
		CreationDate: mi.CreationDate,
		UrlList:      mi.UrlList,
		Info:         mi.InfoBytes,
	}

	seen := make(map[string]bool)
	for _, tier := range tf.AnnounceList {
		for _, u := range tier {
			seen[u] = true
		}
	}
	if tf.Announce != "" {
		seen[tf.Announce] = true
	}
	for _, u := range extraTrackers {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		tf.AnnounceList = append(tf.AnnounceList, []string{u})
		if tf.Announce == "" {
			tf.Announce = u
		}
	}

	return bencode.Marshal(tf)
}
