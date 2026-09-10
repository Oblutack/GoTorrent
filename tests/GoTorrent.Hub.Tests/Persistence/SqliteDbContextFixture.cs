using GoTorrent.Hub.Infrastructure.Persistence;
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
        return new GoTorrentHubDbContext(options);
    }

    public void Dispose() => _connection.Dispose();
}
