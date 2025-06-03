package metainfo

import (
	"bytes"       // For bencoding the InfoDict to calculate InfoHash
	"crypto/sha1" // For SHA-1 hashing
	"errors"      // For creating custom errors
	"io"          // For reading file content
	"os"          // For file operations

	"github.com/jackpal/bencode-go"
)

// LoadFromFile parses a .torrent file from the given path.
func LoadFromFile(filePath string) (*MetaInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return New(file)
}

// New parses a .torrent file from an io.Reader (e.g., an opened file).
func New(r io.Reader) (*MetaInfo, error) {
	// The top-level structure of a .torrent file is a bencoded dictionary.
	// We first parse it into a temporary structure that mirrors MetaInfo
	// but with Info as a RawMessage to calculate InfoHash before full parsing.
	var torrentFile struct {
		Announce     string                `bencode:"announce"`
		AnnounceList [][]string            `bencode:"announce-list"`
		Comment      string                `bencode:"comment"`
		CreatedBy    string                `bencode:"created by"`
		CreationDate int64                 `bencode:"creation date"`
		Info         bencode.RawMessage    `bencode:"info"` // Keep 'info' as raw bytes first
	}

	if err := bencode.Unmarshal(r, &torrentFile); err != nil {
		return nil, err
	}

	if torrentFile.Announce == "" {
		return nil, errors.New("metainfo: announce URL is required")
	}
	if len(torrentFile.Info) == 0 {
		return nil, errors.New("metainfo: info dictionary is missing or empty")
	}

	mi := &MetaInfo{
		Announce:     torrentFile.Announce,
		AnnounceList: torrentFile.AnnounceList,
		Comment:      torrentFile.Comment,
		CreatedBy:    torrentFile.CreatedBy,
		CreationDate: torrentFile.CreationDate,
	}

	// Calculate InfoHash: SHA-1 hash of the bencoded 'info' dictionary
	h := sha1.New()
	h.Write(torrentFile.Info) // torrentFile.Info contains the raw bencoded 'info' part
	copy(mi.InfoHash[:], h.Sum(nil))

	// Now parse the 'info' dictionary into mi.Info
	if err := bencode.Unmarshal(bytes.NewReader(torrentFile.Info), &mi.Info); err != nil {
		return nil, errors.New("metainfo: could not parse info dictionary: " + err.Error())
	}

	// Validate and process pieces
	if mi.Info.PieceLength == 0 {
		return nil, errors.New("metainfo: piece length cannot be zero")
	}
	if len(mi.Info.Pieces)%sha1.Size != 0 {
		return nil, errors.New("metainfo: pieces string length is not a multiple of SHA-1 hash size (20 bytes)")
	}
	numPieces := len(mi.Info.Pieces) / sha1.Size
	mi.PieceHashes = make([][20]byte, numPieces)
	for i := 0; i < numPieces; i++ {
		copy(mi.PieceHashes[i][:], []byte(mi.Info.Pieces[i*sha1.Size:(i+1)*sha1.Size]))
	}

	// Calculate TotalLength
	if mi.Info.Length > 0 { // Single-file torrent
		mi.TotalLength = mi.Info.Length
	} else { // Multi-file torrent
		for _, fileInfo := range mi.Info.Files {
			mi.TotalLength += fileInfo.Length
		}
	}
	if mi.TotalLength == 0 {
		return nil, errors.New("metainfo: total length of files is zero")
	}

	// Basic validation: number of pieces should match total length and piece length
	expectedNumPieces := (mi.TotalLength + mi.Info.PieceLength - 1) / mi.Info.PieceLength
	if int(expectedNumPieces) != numPieces {
		return nil, errors.New("metainfo: number of pieces does not match total length and piece length")
	}


	return mi, nil
}

// MetaInfo predstavlja parsirane podatke iz .torrent fajla
type MetaInfo struct {
	Announce     string     // URL glavnog trackera
	AnnounceList [][]string `bencode:"announce-list"` // Opciona lista backup trackera (lista listi stringova)
	Comment      string     `bencode:"comment"`       // Opcioni komentar kreatora torrenta
	CreatedBy    string     `bencode:"created by"`    // Opciono, ime programa koji je kreirao torrent
	CreationDate int64      `bencode:"creation date"` // Opciono, Unix timestamp kreiranja

	// 'Info' deo je ključan i sadrži informacije o fajlovima
	// U .torrent fajlu je kao dictionary, ali ga mi čuvamo kao strukturu
	Info InfoDict

	// Ovi podaci se izračunavaju NAKON parsiranja 'Info' dela
	// i ne čitaju se direktno iz bencode-a na ovom nivou
	InfoHash    [20]byte   // SHA-1 hash bencode-ovanog 'Info' dictionary-ja
	PieceHashes [][20]byte // Niz 20-bajtnih SHA-1 hasheva za svaki deo
	TotalLength int64      // Ukupna veličina svih fajlova u torrentu
}

// InfoDict predstavlja "info" dictionary unutar .torrent fajla.
// Ovaj dictionary je ono što se hashira da bi se dobio InfoHash.
type InfoDict struct {
	PieceLength int64  `bencode:"piece length"` // Veličina jednog dela u bajtovima
	Pieces      string `bencode:"pieces"`       // String koji sadrži spojene 20-bajtne SHA-1 hasheve svih delova
	Private     int    `bencode:"private"`      // Opciono, da li je torrent privatan (1 za da, 0 ili odsutno za ne)
	Name        string `bencode:"name"`         // Predloženo ime za čuvanje (fajla ili direktorijuma)

	// Za torrente sa jednim fajlom:
	Length int64 `bencode:"length,omitempty"` // Veličina fajla u bajtovima (samo za single-file torrente)

	// Za torrente sa više fajlova:
	Files []FileInfo `bencode:"files,omitempty"` // Lista fajlova (samo za multi-file torrente)
}

// FileInfo opisuje jedan fajl unutar multi-file torrenta
type FileInfo struct {
	Length int64    `bencode:"length"`          // Veličina ovog fajla u bajtovima
	Path   []string `bencode:"path"`            // Lista stringova koji predstavljaju putanju do fajla (npr. ["dir1", "dir2", "file.ext"])
	Md5sum string   `bencode:"md5sum,omitempty"` // Opcioni MD5 hash fajla (retko se koristi)
}