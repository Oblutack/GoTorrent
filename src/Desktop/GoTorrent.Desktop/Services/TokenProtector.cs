using System.Security.Cryptography;
using System.Text;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// Encrypts the gottrentd bearer token at rest using Windows DPAPI
/// (<see cref="ProtectedData"/>, per-user scope) - the same real-secret-
/// at-rest problem the Hub already solved with an ASP.NET Core Data
/// Protection-backed EF Core value converter, but Desktop has no EF Core
/// (or any database) to hang a converter off of, so this is a plain
/// static helper <see cref="FileSettingsStore"/> calls directly instead.
///
/// <para>
/// Values start with a literal <c>"dpapi:"</c> prefix once protected -
/// deliberate and load-bearing, not decorative: it's what lets
/// <see cref="Unprotect"/> tell a genuinely-encrypted value apart from
/// an existing plaintext <c>settings.json</c> written by a version of
/// this app from before this feature existed, without guessing from the
/// byte content alone. A settings file with no prefix is treated as
/// already-plaintext and returned unchanged - the very next
/// <see cref="FileSettingsStore.Save"/> encrypts it going forward, a
/// transparent one-time upgrade rather than a migration step a user
/// (or this codebase) has to think about.
/// </para>
/// </summary>
internal static class TokenProtector
{
    private const string Prefix = "dpapi:";

    // DPAPI's own "additional entropy" parameter - not a secret (it's
    // right here in the source), just scopes what CryptProtectData/
    // CryptUnprotectData will actually decrypt, so a token this app
    // encrypted can't be silently unprotected by some other DPAPI
    // consumer that happens to run as the same Windows user.
    private static readonly byte[] Entropy = Encoding.UTF8.GetBytes("GoTorrent.Desktop.Token.v1");

    /// <summary>
    /// Encrypts <paramref name="plaintext"/> for storage. A no-op outside
    /// Windows (DPAPI doesn't exist there, and this app's own
    /// Windows-only-feature guards - autostart, file association - follow
    /// the identical "OperatingSystem.IsWindows() ? real thing : no-op"
    /// shape already) - the token is stored plaintext on a platform with
    /// no equivalent OS-level secret store wired up yet, not silently
    /// dropped or corrupted.
    /// </summary>
    public static string Protect(string plaintext)
    {
        if (string.IsNullOrEmpty(plaintext) || !OperatingSystem.IsWindows())
        {
            return plaintext;
        }
        var bytes = Encoding.UTF8.GetBytes(plaintext);
        var protectedBytes = ProtectedData.Protect(bytes, Entropy, DataProtectionScope.CurrentUser);
        return Prefix + Convert.ToBase64String(protectedBytes);
    }

    /// <summary>
    /// Decrypts a value <see cref="Protect"/> produced, or returns
    /// <paramref name="stored"/> unchanged if it was never protected in
    /// the first place (see the class doc comment) or can't be decrypted
    /// - e.g. a <c>settings.json</c> copied to a different machine or
    /// user account, where DPAPI's own per-user/per-machine key material
    /// genuinely can't unprotect it. Fails soft rather than throwing:
    /// worst case is a stale or garbled token that a subsequent connect
    /// attempt rejects cleanly, the same failure shape a wrong token
    /// typed by hand already produces.
    /// </summary>
    public static string Unprotect(string stored)
    {
        if (string.IsNullOrEmpty(stored) || !stored.StartsWith(Prefix, StringComparison.Ordinal))
        {
            return stored;
        }
        if (!OperatingSystem.IsWindows())
        {
            return stored;
        }
        try
        {
            var protectedBytes = Convert.FromBase64String(stored[Prefix.Length..]);
            var bytes = ProtectedData.Unprotect(protectedBytes, Entropy, DataProtectionScope.CurrentUser);
            return Encoding.UTF8.GetString(bytes);
        }
        catch (Exception ex) when (ex is CryptographicException or FormatException)
        {
            return stored;
        }
    }
}
