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
  src/Hub/GoTorrent.Hub.Api/            controllers, DI, OpenAPI, health checks, background services
  src/Hub/GoTorrent.Hub.Core/           domain models, IEngineClient, RSS rule engine, multi-node aggregation, history/analytics, JWT options
  src/Hub/GoTorrent.Hub.Infrastructure/ EngineClient (typed HttpClient + Polly resilience), EF Core/SQLite persistence, ASP.NET Core Identity + JWT issuing
  tests/GoTorrent.Hub.Tests/            xUnit — unit tests, real-SQLite persistence tests, WebApplicationFactory integration tests
```

## Running it locally

1. Start a `gottrentd` (see the repo root README) and note its API
   address and the token it generated (`<state-dir>/../api-token`, or
   wherever `-config` points).
2. Point the Hub at it, and set a JWT signing key, via .NET user-secrets —
   never `appsettings.json`. Both are real credentials and must never be
   committed:
   ```sh
   cd src/Hub/GoTorrent.Hub.Api
   dotnet user-secrets set "Engine:BaseAddress" "http://127.0.0.1:6880/"
   dotnet user-secrets set "Engine:Token" "<the token gottrentd generated>"
   dotnet user-secrets set "Jwt:SigningKey" "<any random string, 32+ bytes>"
   ```
   A quick way to generate a signing key: `openssl rand -base64 32`
   (or, in PowerShell, `[Convert]::ToBase64String((1..32|%{Get-Random -Max 256}))`).
   The Hub refuses to start without one at least 32 bytes long — an
   unset or short key wouldn't just fail the first login, it would make
   every token the Hub ever issues forgeable.
3. `dotnet run --project src/Hub/GoTorrent.Hub.Api`. `/health` reports
   whether the Hub can actually reach that `gottrentd` and needs no
   token; every other route does. `/openapi/v1.json` (and, in
   Development, `/scalar/v1` for an interactive explorer) documents
   every route.
4. Create the first account — allowed once, with no token, only while no
   account exists yet:
   ```sh
   curl -X POST http://localhost:5000/api/v1/auth/register \
     -H "Content-Type: application/json" \
     -d '{"userName":"you","password":"<a real password>"}'
   ```
   Then log in to get a bearer token:
   ```sh
   curl -X POST http://localhost:5000/api/v1/auth/login \
     -H "Content-Type: application/json" \
     -d '{"userName":"you","password":"<the same password>"}'
   ```
   `POST /api/v1/auth/register` accepts a second account too, but only
   from a caller who's already authenticated — an open self-registration
   endpoint has no place on a Hub that might be reachable from outside
   the LAN.

## Live events

Connect a SignalR client (any language SignalR supports) to
`/hubs/events` with the same bearer token as everything else — for a
browser client that can't set an `Authorization` header on the
connection, SignalR's client already knows to send the token as an
`?access_token=` query parameter instead, and the Hub accepts that too.
Every message is `NodeEvent`, one per event any registered node's own
`gottrentd` produces (torrent added/removed/state-changed, peer
connected/disconnected, piece verified, a `sessionStats` snapshot once a
second), tagged with which node it came from:

```json
{ "nodeId": "...", "nodeName": "...", "event": { "kind": "torrentStateChanged", "infoHash": "...", "state": "Seeding", "time": "..." } }
```

## Status

All five of ROADMAP.md's 5.2 features are done:

- **Torrents/session proxy** — `GET /api/v1/torrents`, `GET /api/v1/session`,
  proxied from the one node configured via `Engine:*` — proof the
  Hub-to-engine seam works, not the Hub's actual value proposition.
- **RSS + auto-download rules** — `RssRulesController` (CRUD) and a
  background poller that matches feed items against a rule's pattern and
  adds matches to the engine, with duplicate suppression.
- **Multi-node aggregation** — `NodesController`: register several
  `gottrentd` instances, aggregate their torrents/status behind one API
  with concurrent per-node fan-out and failure tolerance. Each node's
  bearer token is encrypted at rest.
- **History/analytics** — `HistoryController`: a completed-torrent
  archive that outlives any one engine process, plus a pruned
  download/upload session timeline, both recorded by reusing multi-node
  aggregation's own fan-out.
- **Identity + JWT** — `AuthController`: real ASP.NET Core Identity
  (password hashing, lockout after repeated failed logins) issuing
  signed JWT bearer tokens. Every route requires one except
  `/api/v1/auth/*` and `/health`. No roles (every account has the same
  access) and no refresh-token flow — both real, deliberately
  out-of-scope simplifications for now, not oversights.
- **SignalR fan-out** — one `/hubs/events` connection relaying every
  registered node's own live WebSocket event stream, instead of a client
  opening its own raw connection per node. `NodeEventFanOutService`
  keeps exactly one subscription running per enabled node, restarting
  one that drops without affecting the others.

Phase 5.2 is complete; Identity + JWT and SignalR fan-out both landed the
same day as the rest.
