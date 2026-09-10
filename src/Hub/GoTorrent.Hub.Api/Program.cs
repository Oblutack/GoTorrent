using GoTorrent.Hub.Api.BackgroundServices;
using GoTorrent.Hub.Infrastructure.Engine;
using GoTorrent.Hub.Infrastructure.History;
using GoTorrent.Hub.Infrastructure.Nodes;
using GoTorrent.Hub.Infrastructure.Persistence;
using GoTorrent.Hub.Infrastructure.Rss;
using Microsoft.EntityFrameworkCore;
using Scalar.AspNetCore;
using Serilog;

var builder = WebApplication.CreateBuilder(args);

// Structured logging (5.3): reads its own sinks/levels from the "Serilog"
// configuration section (appsettings.json), falling back to a console
// sink if none is configured - real, not just the default
// Microsoft.Extensions.Logging text writer.
builder.Host.UseSerilog((context, configuration) =>
    configuration.ReadFrom.Configuration(context.Configuration));

builder.Services.AddControllers();
builder.Services.AddOpenApi();
builder.Services.AddEngineClient(builder.Configuration);
builder.Services.AddRssRules(builder.Configuration);
builder.Services.AddHostedService<RssFeedPollingService>();
builder.Services.AddNodeAggregation();
builder.Services.AddHistory(builder.Configuration);
builder.Services.AddHostedService<HistoryRecordingService>();

// gottrentd itself is the thing actually worth reporting on here - if the
// Hub can't reach its one configured engine node, that's exactly the
// "not ready" signal a real health check exists for.
builder.Services.AddHealthChecks()
    .AddCheck<GoTorrent.Hub.Api.HealthChecks.EngineHealthCheck>("engine");

var app = builder.Build();

// The Hub's own database (RSS rules today) - applying migrations on
// startup is a deliberate, documented choice for a project at this stage
// (no separate deploy/migrate step exists yet), not something to carry
// unexamined into a real multi-instance production setup later.
using (var scope = app.Services.CreateScope())
{
    await scope.ServiceProvider.GetRequiredService<GoTorrentHubDbContext>().Database.MigrateAsync();
}

app.UseSerilogRequestLogging();

if (app.Environment.IsDevelopment())
{
    app.MapOpenApi();
    app.MapScalarApiReference();
}

app.UseHttpsRedirection();
app.UseAuthorization();
app.MapControllers();
app.MapHealthChecks("/health");

app.Run();

// Exposed for GoTorrent.Hub.Tests' WebApplicationFactory<Program>.
public partial class Program;
