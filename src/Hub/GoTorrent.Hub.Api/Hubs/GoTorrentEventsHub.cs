using Microsoft.AspNetCore.Authorization;

namespace GoTorrent.Hub.Api.Hubs;

/// <summary>
/// The single SignalR connection ROADMAP.md's 5.2 fan-out calls for —
/// "one Hub connection feeding the desktop app and any future mobile
/// client from the same stream," instead of a client opening its own raw
/// WebSocket to every registered gottrentd individually.
/// <c>NodeEventFanOutService</c> is what actually pushes into it (via
/// <see cref="SignalRNodeEventBroadcaster"/>/<c>IHubContext</c>);
/// this class has no client-invokable methods of its own yet — nothing
/// today needs a client to call back into the Hub, only to receive from
/// it. A future per-node subscription filter would use SignalR Groups,
/// added here when something actually needs it.
/// </summary>
/// <remarks>
/// Base type spelled out fully-qualified, not via a <c>using</c> — this
/// project's own root namespace is <c>GoTorrent.Hub</c>, which shadows
/// <see cref="Microsoft.AspNetCore.SignalR.Hub"/>'s simple name <c>Hub</c>
/// for any type nested under it (a real, if slightly funny, naming
/// collision caught immediately by the compiler, not silently wrong).
/// </remarks>
[Authorize]
public sealed class GoTorrentEventsHub : Microsoft.AspNetCore.SignalR.Hub;
