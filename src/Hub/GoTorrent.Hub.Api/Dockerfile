# Build from the repo root: docker build -f src/Hub/GoTorrent.Hub.Api/Dockerfile .
# (or via docker-compose.yml, which already points at this file)

FROM mcr.microsoft.com/dotnet/sdk:10.0 AS build
WORKDIR /src

# Copy just the project files first so `dotnet restore` is its own cached
# layer - editing Program.cs shouldn't force NuGet to re-fetch everything.
COPY src/Hub/GoTorrent.Hub.Api/GoTorrent.Hub.Api.csproj src/Hub/GoTorrent.Hub.Api/
COPY src/Hub/GoTorrent.Hub.Core/GoTorrent.Hub.Core.csproj src/Hub/GoTorrent.Hub.Core/
COPY src/Hub/GoTorrent.Hub.Infrastructure/GoTorrent.Hub.Infrastructure.csproj src/Hub/GoTorrent.Hub.Infrastructure/
RUN dotnet restore src/Hub/GoTorrent.Hub.Api/GoTorrent.Hub.Api.csproj

COPY src/Hub/ src/Hub/
RUN dotnet publish src/Hub/GoTorrent.Hub.Api/GoTorrent.Hub.Api.csproj -c Release -o /app --no-restore

FROM mcr.microsoft.com/dotnet/aspnet:10.0 AS runtime
WORKDIR /app
COPY --from=build /app .

# The Hub's own SQLite database lives here by default (see
# ConnectionStrings:GoTorrentHub) - a real deployment should mount this
# as a volume, or the database resets every time the container is rebuilt.
VOLUME /app/data

EXPOSE 8080
ENV ASPNETCORE_URLS=http://+:8080
ENTRYPOINT ["dotnet", "GoTorrent.Hub.Api.dll"]
