namespace GoTorrent.Desktop.Services;

/// <summary>
/// Thrown when the Hub answers with a non-success status - the
/// <see cref="IHubClient"/> counterpart to <see cref="EngineRequestException"/>.
/// Carries whatever real body text the Hub sent (a plain string for
/// login failures, e.g. the lockout message; ASP.NET Core's default
/// <c>ProblemDetails</c> JSON for a model-validation 400) rather than a
/// bare "401 Unauthorized".
/// </summary>
public sealed class HubRequestException(string message) : Exception(message);
