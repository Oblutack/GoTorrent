using System;
using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace GoTorrent.Hub.Infrastructure.Persistence.Migrations
{
    /// <inheritdoc />
    public partial class AddHistory : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "SessionSnapshots",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "TEXT", nullable: false),
                    NodeId = table.Column<Guid>(type: "TEXT", nullable: false),
                    NodeName = table.Column<string>(type: "TEXT", maxLength: 200, nullable: false),
                    CapturedAt = table.Column<long>(type: "INTEGER", nullable: false),
                    TorrentCount = table.Column<int>(type: "INTEGER", nullable: false),
                    DownloadingCount = table.Column<int>(type: "INTEGER", nullable: false),
                    SeedingCount = table.Column<int>(type: "INTEGER", nullable: false),
                    TotalDownloaded = table.Column<long>(type: "INTEGER", nullable: false),
                    TotalUploaded = table.Column<long>(type: "INTEGER", nullable: false),
                    TotalPeerCount = table.Column<int>(type: "INTEGER", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_SessionSnapshots", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "TorrentHistoryEntries",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "TEXT", nullable: false),
                    NodeId = table.Column<Guid>(type: "TEXT", nullable: false),
                    NodeName = table.Column<string>(type: "TEXT", maxLength: 200, nullable: false),
                    InfoHash = table.Column<string>(type: "TEXT", maxLength: 64, nullable: false),
                    Name = table.Column<string>(type: "TEXT", maxLength: 500, nullable: false),
                    Category = table.Column<string>(type: "TEXT", maxLength: 200, nullable: true),
                    TotalLength = table.Column<long>(type: "INTEGER", nullable: false),
                    Downloaded = table.Column<long>(type: "INTEGER", nullable: false),
                    Uploaded = table.Column<long>(type: "INTEGER", nullable: false),
                    SeedRatio = table.Column<double>(type: "REAL", nullable: false),
                    CompletedAt = table.Column<long>(type: "INTEGER", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_TorrentHistoryEntries", x => x.Id);
                });

            migrationBuilder.CreateIndex(
                name: "IX_SessionSnapshots_NodeId_CapturedAt",
                table: "SessionSnapshots",
                columns: new[] { "NodeId", "CapturedAt" });

            migrationBuilder.CreateIndex(
                name: "IX_TorrentHistoryEntries_NodeId_InfoHash",
                table: "TorrentHistoryEntries",
                columns: new[] { "NodeId", "InfoHash" },
                unique: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "SessionSnapshots");

            migrationBuilder.DropTable(
                name: "TorrentHistoryEntries");
        }
    }
}
