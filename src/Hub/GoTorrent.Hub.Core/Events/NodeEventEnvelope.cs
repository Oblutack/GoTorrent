using System.Text.Json;

namespace GoTorrent.Hub.Core.Events;

/// <summary>
/// One event relayed from one node's own WebSocket event stream
/// (gottrentd's <c>GET /api/v1/events</c>, Phase 4.2 — see
/// <c>internal/api.WSEvent</c> on the Go side), tagged with which node it
/// came from. <see cref="Event"/> is passed through verbatim as parsed
/// JSON rather than re-modeled into a parallel C# type: gottrentd's own
/// <c>WSEvent</c> shape is the one source of truth for what an event
/// looks like, and duplicating it here would just be a second copy that
/// can drift out of sync, the same class of bug already fixed once on
/// the Go side (3.6's <c>AddTracker</c> two-copies-of-one-rule bug).
/// </summary>
public sealed record NodeEventEnvelope(Guid NodeId, string NodeName, JsonElement Event);
