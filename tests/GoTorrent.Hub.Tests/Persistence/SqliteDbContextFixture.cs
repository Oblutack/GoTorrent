using GoTorrent.Hub.Infrastructure.Persistence;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.Data.Sqlite;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Tests.Persistence;

/// <summary>
/// A real SQLite database (in-memory, one connection kept open for the
/// fixture's lifetime — a fresh connection to ":memory:" would otherwise
/// be its own separate empty database) with the real migrations applied,
/// not <c>UseInMemoryDatabase</c>'s fake provider: this project's own
/// convention (see CLAUDE.md's Testing section on the Go side) is real
/// fixtures over mocks, and EF Core's InMemory provider does not enforce
/// real constraints (the unique index ProcessedFeedItemStore relies on,
/// notably) the way a real database does.
/// </summary>
public sealed class SqliteDbContextFixture : IDisposable
{
    private readonly SqliteConnection _connection = new("Data Source=:memory:");

    // One in-memory key ring for the fixture's whole lifetime - encrypting
    // EngineNode.Token with one CreateContext() call and decrypting it
    // with another only works if every context built from this fixture
    // shares the same Data Protection keys. Ephemeral (never touches
    // disk) is exactly right for a test double: real, working encryption
    // with no key-file cleanup burden.
    private readonly IDataProtectionProvider _dataProtectionProvider = new EphemeralDataProtectionProvider();

    public SqliteDbContextFixture()
    {
        _connection.Open();
        using var db = CreateContext();
        db.Database.Migrate();
    }

    public GoTorrentHubDbContext CreateContext()
    {
        var options = new DbContextOptionsBuilder<GoTorrentHubDbContext>()
            .UseSqlite(_connection)
            .Options;
        return new GoTorrentHubDbContext(options, _dataProtectionProvider);
    }

    public void Dispose() => _connection.Dispose();
}
