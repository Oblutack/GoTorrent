// Package merkle implements the SHA-256 binary merkle tree BitTorrent v2
// (BEP 52) uses to verify pieces: a branching-factor-2 tree whose leaves are
// SHA-256 hashes of 16KiB blocks, padded with zero hashes to a power of two
// so the tree is always balanced.
//
// No official test-vector suite exists for this (same situation as
// internal/mse and internal/utp) — every rule here was cross-checked
// against BEP 52's own spec text at bittorrent.org/beps/bep_0052.html and,
// for the padding rule specifically (the prose alone leaves the exact
// padding value ambiguous — "set to zero" could mean a raw zero leaf hash
// or the hash of an all-zero piece, and those are different values one
// layer up), against BEP 52's own linked reference implementation,
// bep_0052_torrent_creator.py. That script settled it: leaf-level padding
// (inside one piece's own mini-tree) uses a raw all-zero 32-byte hash;
// the layer above (combining a file's own per-piece roots into its overall
// pieces root) pads with the *root hash of an entirely-zero piece*
// (Root(PadLeaves(nil, blocksPerPiece))), not a raw zero value. Getting
// this backwards would produce plausible-looking but wrong hashes that
// only a real compliant peer would ever catch.
//
// This package is the pure hashing primitive only — a from-scratch, no-
// dependency SHA-256 merkle tree with no knowledge of .torrent files, the
// wire protocol, or piece geometry beyond "how many 16KiB blocks make up
// one piece." internal/metainfo (parsing/building v2 .torrent files) and
// internal/storage (per-piece verification) are what give it meaning.
package merkle
