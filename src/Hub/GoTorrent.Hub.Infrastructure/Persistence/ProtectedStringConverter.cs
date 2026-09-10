using Microsoft.AspNetCore.DataProtection;
using Microsoft.EntityFrameworkCore.Storage.ValueConversion;

namespace GoTorrent.Hub.Infrastructure.Persistence;

/// <summary>
/// An EF Core value converter that encrypts a string column at rest via
/// ASP.NET Core Data Protection (<see cref="IDataProtector.Protect(string)"/>/
/// <see cref="IDataProtector.Unprotect(string)"/>) — used for
/// <c>EngineNode.Token</c>, a real gottrentd bearer token that must not
/// sit in plaintext in the Hub's own SQLite database. Transparent to
/// every caller above EF Core: the domain model
/// (<see cref="Core.Nodes.EngineNode"/>) and every repository method see
/// plaintext in and plaintext out, exactly like every other string
/// property — only the bytes actually written to disk are ciphertext.
/// </summary>
public sealed class ProtectedStringConverter(IDataProtector protector)
    : ValueConverter<string, string>(plaintext => protector.Protect(plaintext), ciphertext => protector.Unprotect(ciphertext));
