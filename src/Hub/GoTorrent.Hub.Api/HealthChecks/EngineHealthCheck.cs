using GoTorrent.Hub.Core.Engine;
using Microsoft.Extensions.Diagnostics.HealthChecks;

namespace GoTorrent.Hub.Api.HealthChecks;

/// <summary>
/// Reports healthy only if the configured gottrentd node actually answers
/// <c>GET /api/v1/session</c> - "the process is running" (the default
/// ASP.NET Core liveness signal) says nothing about whether the Hub can
/// reach the one thing it exists to front, which is exactly what a
/// readiness check should catch.
/// </summary>
public sealed class EngineHealthCheck(IEngineClient engineClient) : IHealthCheck
{
    public async Task<HealthCheckResult> CheckHealthAsync(
        HealthCheckContext context, CancellationToken cancellationToken = default)
    {
        try
        {
            await engineClient.GetSessionAsync(cancellationToken);
            return HealthCheckResult.Healthy();
        }
        catch (Exception ex)
        {
            return HealthCheckResult.Unhealthy("gottrentd is not reachable.", ex);
        }
    }
}
