// Package metainfo handles parsing and representation of .torrent file metadata.
package metainfo

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os" // OTKOMENTARISANO - potrebno za LoadFromFile

	"github.com/Oblutack/GoTorrent/internal/gobencode"
)

// MetaInfo holds all the data from a .torrent file.
type MetaInfo struct {
	Announce     string
	AnnounceList [][]string
	Comment      string
	CreatedBy    string
	CreationDate int64

	Info InfoDict

	InfoHash    [20]byte
	PieceHashes [][20]byte
	TotalLength int64
}

// InfoDict represents the 'info' dictionary within a .torrent file.
type InfoDict struct {
	PieceLength int64
	Pieces      string
	Private     int
	Name        string

	Length int64      `bencode:"length,omitempty"` // Only for single-file
	Files  []FileInfo `bencode:"files,omitempty"`  // Only for multi-file
}

// FileInfo describes a single file within a multi-file torrent.
type FileInfo struct {
	Length int64
	Path   []string
	Md5sum string `bencode:"md5sum,omitempty"`
}

// LoadFromFile opens and parses a .torrent file from the given path.
func LoadFromFile(filePath string) (*MetaInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("metainfo: could not open file %s: %w", filePath, err)
	}
	defer file.Close()

	return New(file)
}

// New parses .torrent data from an io.Reader.
func New(r io.Reader) (*MetaInfo, error) {
	decodedData, err := gobencode.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("metainfo: failed to decode torrent data: %w", err)
	}

	torrentMap, ok := decodedData.(map[string]interface{})
	if !ok {
		return nil, errors.New("metainfo: top-level bencode data is not a dictionary")
	}

	mi := &MetaInfo{} // Greška 'undefined: MetaInfo' bi trebalo da nestane

	// Populate basic fields
	if announce, ok := torrentMap["announce"].(string); ok {
		mi.Announce = announce
	} else {
		return nil, errors.New("metainfo: 'announce' URL is missing or not a string")
	}
	if alRaw, ok := torrentMap["announce-list"].([]interface{}); ok {
		mi.AnnounceList = make([][]string, len(alRaw))
		for i, tierInterfaces := range alRaw {
			if tierActual, tierOk := tierInterfaces.([]interface{}); tierOk {
				mi.AnnounceList[i] = make([]string, len(tierActual))
				for j, trackerInterface := range tierActual {
					if trackerStr, okStr := trackerInterface.(string); okStr {
						mi.AnnounceList[i][j] = trackerStr
					}
				}
			}
		}
	}
	if comment, ok := torrentMap["comment"].(string); ok {
		mi.Comment = comment
	}
	if createdBy, ok := torrentMap["created by"].(string); ok {
		mi.CreatedBy = createdBy
	}
	if creationDate, ok := torrentMap["creation date"].(int64); ok {
		mi.CreationDate = creationDate
	}

	// Get the 'info' dictionary
	infoMapInterface, infoPresent := torrentMap["info"]
	if !infoPresent {
		return nil, errors.New("metainfo: 'info' dictionary missing")
	}
	infoMap, ok := infoMapInterface.(map[string]interface{})
	if !ok {
		return nil, errors.New("metainfo: 'info' is not a dictionary")
	}

	// Calculate InfoHash
	var infoBuf bytes.Buffer
	if err := gobencode.Encode(&infoBuf, infoMap); err != nil {
		return nil, fmt.Errorf("metainfo: failed to bencode 'info' dictionary for hashing: %w", err)
	}
	h := sha1.New()
	_, err = h.Write(infoBuf.Bytes())
    if err != nil {
        return nil, fmt.Errorf("metainfo: failed to write to sha1 hasher: %w", err)
    }
	copy(mi.InfoHash[:], h.Sum(nil))

	// Populate mi.Info (InfoDict)
	if pl, ok := infoMap["piece length"].(int64); ok {
		if pl <= 0 { return nil, errors.New("metainfo: 'piece length' must be positive") }
		mi.Info.PieceLength = pl
	} else { return nil, errors.New("metainfo: 'piece length' missing or not an integer") }

	if piecesStr, ok := infoMap["pieces"].(string); ok {
		if len(piecesStr)%sha1.Size != 0 { return nil, fmt.Errorf("metainfo: 'pieces' length (%d) is not a multiple of %d", len(piecesStr), sha1.Size) }
		mi.Info.Pieces = piecesStr
	} else { return nil, errors.New("metainfo: 'pieces' missing or not a string") }

	if name, ok := infoMap["name"].(string); ok {
		mi.Info.Name = name
	} else { return nil, errors.New("metainfo: 'name' missing or not a string") }
    
	if private, ok := infoMap["private"].(int64); ok {
		mi.Info.Private = int(private)
	}

	if length, ok := infoMap["length"].(int64); ok {
		if length < 0 { return nil, errors.New("metainfo: 'length' cannot be negative") }
		mi.Info.Length = length
		mi.TotalLength = length
	} else if filesRaw, filesOk := infoMap["files"].([]interface{}); filesOk {
		if len(filesRaw) == 0 { return nil, errors.New("metainfo: 'files' array is present but empty") }
		mi.Info.Files = make([]FileInfo, len(filesRaw)) // Greška 'undefined: FileInfo' bi trebalo da nestane
		var currentTotalLength int64
		for i, fileRawInterface := range filesRaw {
			fileMap, fileMapOk := fileRawInterface.(map[string]interface{})
			if !fileMapOk { return nil, fmt.Errorf("metainfo: file entry %d in 'files' is not a dictionary", i) }
			var fi FileInfo // I ovde
			if l, lOk := fileMap["length"].(int64); lOk {
				if l < 0 { return nil, fmt.Errorf("metainfo: file entry %d has negative length", i) }
				fi.Length = l
				currentTotalLength += l
			} else { return nil, fmt.Errorf("metainfo: file entry %d 'length' missing or not an integer", i) }
			if pRaw, pOk := fileMap["path"].([]interface{}); pOk {
                if len(pRaw) == 0 { return nil, fmt.Errorf("metainfo: file entry %d 'path' array is empty", i) }
				fi.Path = make([]string, len(pRaw))
				for j, partInterface := range pRaw {
					if partStr, partStrOk := partInterface.(string); partStrOk {
						fi.Path[j] = partStr
					} else { return nil, fmt.Errorf("metainfo: file entry %d path segment %d is not a string", i, j) }
				}
			} else { return nil, fmt.Errorf("metainfo: file entry %d 'path' missing or not a list", i) }
            if md5sum, md5Ok := fileMap["md5sum"].(string); md5Ok { fi.Md5sum = md5sum }
			mi.Info.Files[i] = fi
		}
		mi.TotalLength = currentTotalLength
	} else { return nil, errors.New("metainfo: 'info' dictionary must contain either 'length' or 'files'") }

	if mi.Info.PieceLength > 0 {
		numPieces := len(mi.Info.Pieces) / sha1.Size
		mi.PieceHashes = make([][20]byte, numPieces)
		for i := 0; i < numPieces; i++ {
			copy(mi.PieceHashes[i][:], []byte(mi.Info.Pieces[i*sha1.Size:(i+1)*sha1.Size]))
		}
		expectedNumPieces := (mi.TotalLength + mi.Info.PieceLength - 1) / mi.Info.PieceLength
		if mi.TotalLength == 0 && numPieces == 0 && len(mi.Info.Pieces) == 0 {} else 
        if int64(numPieces) != expectedNumPieces {
			return nil, fmt.Errorf("metainfo: number of pieces from 'pieces' string (%d) does not match calculated expected number of pieces (%d)", numPieces, expectedNumPieces)
		}
	} else if len(mi.Info.Pieces) > 0 {
        return nil, errors.New("metainfo: 'pieces' field is present but 'piece length' is zero or invalid")
    }

	return mi, nil
}