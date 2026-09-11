namespace GoTorrent.Desktop.Services;

/// <summary>
/// Thrown when gottrentd answers with a non-success status. Carries the
/// real message from its <c>{"error": "..."}</c> body (see
/// <c>internal/api/json.go</c>'s <c>writeError</c> on the Go side)
/// instead of a bare "409 Conflict" from <c>EnsureSuccessStatusCode</c>.
/// </summary>
public sealed class EngineRequestException(string message) : Exception(message);
