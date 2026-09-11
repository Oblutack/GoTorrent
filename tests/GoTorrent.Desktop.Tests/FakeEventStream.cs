using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>
/// An <see cref="IEventStream"/> that never actually connects - tests
/// drive <c>MainViewModel.HandleEvent</c> directly instead of routing
/// events through a socket loop, so this only needs to exist to satisfy
/// the constructor.
/// </summary>
public sealed class FakeEventStream : IEventStream
{
    public async IAsyncEnumerable<WsEvent> ConnectAsync(EngineOptions options, [System.Runtime.CompilerServices.EnumeratorCancellation] CancellationToken cancellationToken)
    {
        await Task.CompletedTask;
        yield break;
    }
}
