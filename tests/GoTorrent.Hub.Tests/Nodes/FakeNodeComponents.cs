using GoTorrent.Hub.Core.Engine;
using GoTorrent.Hub.Core.Nodes;

namespace GoTorrent.Hub.Tests.Nodes;

/// <summary>In-memory <see cref="IEngineNodeRepository"/> for NodeAggregationServiceTests.</summary>
public sealed class FakeEngineNodeRepository : IEngineNodeRepository
{
    private readonly List<EngineNode> _nodes = [];

    public void Seed(EngineNode node) => _nodes.Add(node);

    public Task<IReadOnlyList<EngineNode>> GetAllAsync(CancellationToken cancellationToken) =>
        Task.FromResult<IReadOnlyList<EngineNode>>([.. _nodes]);

    public Task<EngineNode?> GetByIdAsync(Guid id, CancellationToken cancellationToken) =>
        Task.FromResult(_nodes.FirstOrDefault(n => n.Id == id));

    public Task AddAsync(EngineNode node, CancellationToken cancellationToken)
    {
        _nodes.Add(node);
        return Task.CompletedTask;
    }

    public Task UpdateAsync(EngineNode node, CancellationToken cancellationToken) => Task.CompletedTask;

    public Task<bool> DeleteAsync(Guid id, CancellationToken cancellationToken) =>
        Task.FromResult(_nodes.RemoveAll(n => n.Id == id) > 0);
}

/// <summary>
/// A canned <see cref="IEngineClient"/> for one fake node - set
/// <see cref="Torrents"/>/<see cref="Session"/> for what a healthy node
/// returns, or <see cref="Failure"/> to simulate that node being
/// unreachable.
/// </summary>
public sealed class FakeEngineClient : IEngineClient
{
    public List<TorrentSummary> Torrents { get; init; } = [];

    public SessionStats? Session { get; init; }

    public Exception? Failure { get; init; }

    public Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken) =>
        Failure is not null
            ? Task.FromException<IReadOnlyList<TorrentSummary>>(Failure)
            : Task.FromResult<IReadOnlyList<TorrentSummary>>(Torrents);

    public Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken) =>
        Failure is not null
            ? Task.FromException<SessionStats>(Failure)
            : Task.FromResult(Session ?? throw new InvalidOperationException("Test bug: Session was never set."));

    public Task<AddTorrentResult> AddTorrentAsync(AddTorrentRequest request, CancellationToken cancellationToken) =>
        throw new NotSupportedException("Node aggregation never adds torrents through a per-node client.");
}

/// <summary>Hands back one fixed <see cref="FakeEngineClient"/> per node id, so a test can configure it before the call under test runs.</summary>
public sealed class FakeEngineClientFactory : IEngineClientFactory
{
    private readonly Dictionary<Guid, FakeEngineClient> _clients = [];

    public void SetClient(EngineNode node, FakeEngineClient client) => _clients[node.Id] = client;

    public IEngineClient CreateClient(EngineNode node) =>
        _clients.TryGetValue(node.Id, out var client)
            ? client
            : throw new InvalidOperationException($"Test bug: no fake client configured for node {node.Id}.");
}
