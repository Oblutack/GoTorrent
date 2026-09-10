using GoTorrent.Hub.Infrastructure.Engine;
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

// gottrentd itself is the thing actually worth reporting on here - if the
// Hub can't reach its one configured engine node, that's exactly the
// "not ready" signal a real health check exists for.
builder.Services.AddHealthChecks()
    .AddCheck<GoTorrent.Hub.Api.HealthChecks.EngineHealthCheck>("engine");

var app = builder.Build();

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
