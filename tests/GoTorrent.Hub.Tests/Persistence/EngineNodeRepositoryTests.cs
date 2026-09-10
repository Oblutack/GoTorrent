using GoTorrent.Hub.Core.Nodes;
using GoTorrent.Hub.Infrastructure.Nodes;
using Microsoft.Data.Sqlite;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Tests.Persistence;

public sealed class EngineNodeRepositoryTests : IDisposable
{
    private readonly SqliteDbContextFixture _fixture = new();

    public void Dispose() => _fixture.Dispose();

    private static EngineNode MakeNode(string name = "seedbox") => new()
    {
        Name = name,
        BaseAddress = new Uri("http://192.168.1.50:6880/"),
        Token = "super-secret-gottrentd-token",
        Enabled = true,
    };

    [Fact]
    public async Task AddAsync_ThenGetByIdAsync_RoundTrips()
    {
        using var db = _fixture.CreateContext();
        var repo = new EngineNodeRepository(db);
        var node = MakeNode();

        await repo.AddAsync(node, CancellationToken.None);

        using var db2 = _fixture.CreateContext();
        var found = await new EngineNodeRepository(db2).GetByIdAsync(node.Id, CancellationToken.None);

        Assert.NotNull(found);
        Assert.Equal(node.Name, found.Name);
        Assert.Equal(node.BaseAddress, found.BaseAddress);
        // The point of ProtectedStringConverter: a value read back through
        // the repository is plaintext again, indistinguishable from a
        // plain string column to every caller above EF Core.
        Assert.Equal(node.Token, found.Token);
    }

    [Fact]
    public async Task Token_IsStoredEncryptedAtRest()
    {
        using var db = _fixture.CreateContext();
        var repo = new EngineNodeRepository(db);
        var node = MakeNode();
        await repo.AddAsync(node, CancellationToken.None);

        // Bypass EF Core entirely and read the raw column back with a
        // plain ADO.NET query - this is the only way to actually prove
        // the value converter is doing something, rather than just
        // trusting that a round trip through the same repository (which
        // would decrypt on the way out either way) looks right. Reuses
        // the DbContext's own live connection rather than opening a new
        // one: a fresh SqliteConnection to ":memory:" would be its own
        // separate empty database, same reasoning as SqliteDbContextFixture's.
        var connection = (SqliteConnection)db.Database.GetDbConnection();
        await using var command = connection.CreateCommand();
        // EF Core's Sqlite provider stores a Guid column's text as
        // upper-case, while Guid.ToString() is lower-case - COLLATE NOCASE
        // avoids the comparison silently matching nothing.
        command.CommandText = "SELECT Token FROM EngineNodes WHERE Id = $id COLLATE NOCASE";
        command.Parameters.AddWithValue("$id", node.Id.ToString());
        var rawToken = (string?)await command.ExecuteScalarAsync();

        Assert.NotNull(rawToken);
        Assert.NotEqual(node.Token, rawToken);
        Assert.DoesNotContain(node.Token, rawToken);
    }

    [Fact]
    public async Task GetByIdAsync_UnknownIdReturnsNull()
    {
        using var db = _fixture.CreateContext();
        var repo = new EngineNodeRepository(db);

        Assert.Null(await repo.GetByIdAsync(Guid.NewGuid(), CancellationToken.None));
    }

    [Fact]
    public async Task GetAllAsync_ReturnsEveryNodeOrderedByName()
    {
        using var db = _fixture.CreateContext();
        var repo = new EngineNodeRepository(db);
        await repo.AddAsync(MakeNode("Zebra"), CancellationToken.None);
        await repo.AddAsync(MakeNode("Alpha"), CancellationToken.None);

        var all = await repo.GetAllAsync(CancellationToken.None);

        Assert.Equal(["Alpha", "Zebra"], all.Select(n => n.Name));
    }

    [Fact]
    public async Task UpdateAsync_PersistsChanges()
    {
        using var db = _fixture.CreateContext();
        var repo = new EngineNodeRepository(db);
        var node = MakeNode();
        await repo.AddAsync(node, CancellationToken.None);

        node.Enabled = false;
        node.Token = "rotated-token";
        await repo.UpdateAsync(node, CancellationToken.None);

        using var db2 = _fixture.CreateContext();
        var found = await new EngineNodeRepository(db2).GetByIdAsync(node.Id, CancellationToken.None);
        Assert.False(found!.Enabled);
        Assert.Equal("rotated-token", found.Token);
    }

    [Fact]
    public async Task DeleteAsync_RemovesTheNodeAndReturnsTrue()
    {
        using var db = _fixture.CreateContext();
        var repo = new EngineNodeRepository(db);
        var node = MakeNode();
        await repo.AddAsync(node, CancellationToken.None);

        var deleted = await repo.DeleteAsync(node.Id, CancellationToken.None);

        Assert.True(deleted);
        Assert.Null(await repo.GetByIdAsync(node.Id, CancellationToken.None));
    }

    [Fact]
    public async Task DeleteAsync_UnknownIdReturnsFalse()
    {
        using var db = _fixture.CreateContext();
        var repo = new EngineNodeRepository(db);

        Assert.False(await repo.DeleteAsync(Guid.NewGuid(), CancellationToken.None));
    }
}
