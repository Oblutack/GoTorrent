using GoTorrent.Hub.Api.BackgroundServices;
using GoTorrent.Hub.Core.Auth;
using GoTorrent.Hub.Infrastructure.Auth;
using GoTorrent.Hub.Infrastructure.Engine;
using GoTorrent.Hub.Infrastructure.History;
using GoTorrent.Hub.Infrastructure.Nodes;
using GoTorrent.Hub.Infrastructure.Persistence;
using GoTorrent.Hub.Infrastructure.Rss;
using Microsoft.AspNetCore.Authentication.JwtBearer;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using Microsoft.IdentityModel.Tokens;
using Scalar.AspNetCore;
using Serilog;
using System.Text;

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
builder.Services.AddIdentityAndJwt(builder.Configuration);

// TokenValidationParameters is built from the same IOptions<JwtOptions>
// AddIdentityAndJwt already bound and validated (ValidateOnStart) -
// nothing here re-reads or re-validates configuration, it just consumes
// the one already-checked source.
builder.Services.AddAuthentication(JwtBearerDefaults.AuthenticationScheme)
    .AddJwtBearer();
builder.Services.AddOptions<JwtBearerOptions>(JwtBearerDefaults.AuthenticationScheme)
    .Configure<IOptions<JwtOptions>>((bearerOptions, jwtOptions) =>
    {
        var opts = jwtOptions.Value;
        // Defaults to true, which silently rewrites standard claim types
        // like "sub" to legacy WS-Fed claim URIs on the way into
        // HttpContext.User - without this, User.FindFirst(JwtRegisteredClaimNames.Sub)
        // returns null for every authenticated request, a real gotcha
        // caught by JwtTokenServiceTests before anything in this app
        // actually needed to read that claim.
        bearerOptions.MapInboundClaims = false;
        bearerOptions.TokenValidationParameters = new TokenValidationParameters
        {
            ValidateIssuer = true,
            ValidIssuer = opts.Issuer,
            ValidateAudience = true,
            ValidAudience = opts.Audience,
            ValidateLifetime = true,
            ValidateIssuerSigningKey = true,
            IssuerSigningKey = new SymmetricSecurityKey(Encoding.UTF8.GetBytes(opts.SigningKey)),
            ClockSkew = TimeSpan.FromMinutes(1),
        };
    });
builder.Services.AddAuthorization();

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
app.UseAuthentication();
app.UseAuthorization();
// Every controller requires a valid bearer token by default now - secure
// by default for any controller added later, not just the ones that
// exist today. AuthController's own routes opt back out via
// [AllowAnonymous] (you have to be able to reach /login without one
// already). /health is a separate, deliberately unauthenticated route
// (see below) - health probes are typically hit by infrastructure
// without credentials, and it leaks nothing beyond "the process is up
// and can/can't reach its engine."
app.MapControllers().RequireAuthorization();
app.MapHealthChecks("/health");

app.Run();

// Exposed for GoTorrent.Hub.Tests' WebApplicationFactory<Program>.
public partial class Program;
