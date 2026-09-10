# GoTorrent.Hub

The .NET control plane (Phase 5) in front of one or more `gottrentd` nodes
(Phase 4). See the repo root's `README.md` for the whole project; this
file only covers the Hub itself.

**For a single local machine, the desktop app can talk straight to
`gottrentd`'s own REST/WebSocket API and this layer is unnecessary.** The
Hub exists for what a BitTorrent engine has no business owning: multiple
nodes behind one API, RSS-driven auto-download rules, history that
outlives the engine process, and real remote-access auth. A pass-through
proxy for routes `gottrentd` already serves would be architecture theater
— every feature here is meant to be something the Go engine genuinely
shouldn't do itself.

## Solution layout

```
GoTorrent.sln
  src/Hub/GoTorrent.Hub.Api/            controllers, DI, OpenAPI, health checks
  src/Hub/GoTorrent.Hub.Core/           domain models, IEngineClient
  src/Hub/GoTorrent.Hub.Infrastructure/ EngineClient (typed HttpClient + Polly resilience)
  tests/GoTorrent.Hub.Tests/            xUnit — unit tests on EngineClient, integration tests via WebApplicationFactory
```

## Running it locally

1. Start a `gottrentd` (see the repo root README) and note its API
   address and the token it generated (`<state-dir>/../api-token`, or
   wherever `-config` points).
2. Point the Hub at it via .NET user-secrets, not `appsettings.json` — the
   token is a credential and should never be committed:
   ```sh
   cd src/Hub/GoTorrent.Hub.Api
   dotnet user-secrets set "Engine:BaseAddress" "http://127.0.0.1:6880/"
   dotnet user-secrets set "Engine:Token" "<the token gottrentd generated>"
   ```
3. `dotnet run --project src/Hub/GoTorrent.Hub.Api`. `/health` reports
   whether the Hub can actually reach that `gottrentd`; `/openapi/v1.json`
   (and, in Development, `/scalar/v1` for an interactive explorer) documents
   every route.

## Status

Only enough exists today to prove the Hub-to-engine seam end to end:
`GET /api/v1/torrents` and `GET /api/v1/session`, proxied from the one
configured node. Multi-node aggregation, RSS rules, history/analytics,
Identity + JWT, and SignalR fan-out (ROADMAP.md's 5.2) are real features
still to build, not stubbed out here.
