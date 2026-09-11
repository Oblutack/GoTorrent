namespace GoTorrent.Desktop.Models;

/// <summary>
/// Mirrors gottrentd's real <c>GET /api/v1/torrents/{hash}/pieces</c>
/// response (<c>internal/api.PiecesResponse</c>). <see cref="Bitfield"/> is
/// the raw BEP 3 packed bytes (most-significant-bit-first per byte, same
/// as <c>internal/bitfield.Bitfield.Has</c> on the Go side) —
/// <c>System.Text.Json</c> base64-decodes a <c>byte[]</c> field
/// automatically, matching what <c>encoding/json</c> does with one on
/// the way out.
/// </summary>
public sealed record PiecesInfo(int NumPieces, int HaveCount, byte[] Bitfield)
{
    /// <summary>Unpacks <see cref="Bitfield"/> into one bool per piece, for the piece-map control to render directly.</summary>
    public bool[] ToHaveArray()
    {
        var result = new bool[NumPieces];
        for (var i = 0; i < NumPieces; i++)
        {
            var byteIndex = i / 8;
            var bitIndex = 7 - (i % 8);
            result[i] = byteIndex < Bitfield.Length && (Bitfield[byteIndex] & (1 << bitIndex)) != 0;
        }
        return result;
    }
}
