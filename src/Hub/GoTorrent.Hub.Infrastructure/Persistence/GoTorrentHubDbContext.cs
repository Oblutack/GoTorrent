using GoTorrent.Hub.Core.History;
using GoTorrent.Hub.Core.Nodes;
using GoTorrent.Hub.Core.Rss;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Infrastructure.Persistence;

/// <summary>
/// The Hub's own database — deliberately separate from anything
/// gottrentd persists (its manifest/resume data are the Go engine's own
/// business): this is state that outlives, and is meaningless to, any one
/// engine node — RSS rules, registered nodes, history/analytics, and
/// (once built) Identity. SQLite for dev (see
/// DependencyInjection.AddRssRules); nothing here uses a SQLite-specific
/// type mapping, so swapping to Npgsql later is a provider change, not a
/// schema rewrite.
/// </summary>
public sealed class GoTorrentHubDbContext(
    DbContextOptions<GoTorrentHubDbContext> options,
    IDataProtectionProvider dataProtectionProvider) : DbContext(options)
{
    public DbSet<RssRule> RssRules => Set<RssRule>();

    public DbSet<ProcessedFeedItem> ProcessedFeedItems => Set<ProcessedFeedItem>();

    public DbSet<EngineNode> EngineNodes => Set<EngineNode>();

    public DbSet<TorrentHistoryEntry> TorrentHistoryEntries => Set<TorrentHistoryEntry>();

    public DbSet<SessionSnapshot> SessionSnapshots => Set<SessionSnapshot>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.Entity<RssRule>(entity =>
        {
            entity.HasKey(r => r.Id);
            entity.Property(r => r.Name).IsRequired().HasMaxLength(200);
            entity.Property(r => r.FeedUrl).IsRequired().HasMaxLength(2000);
            entity.Property(r => r.TitlePattern).IsRequired().HasMaxLength(500);
            entity.Property(r => r.Category).HasMaxLength(200);
            entity.Property(r => r.DownloadDir).HasMaxLength(1000);
        });

        modelBuilder.Entity<ProcessedFeedItem>(entity =>
        {
            entity.HasKey(p => p.Id);
            // One record per (rule, item) - a duplicate insert (a
            // concurrent poll racing itself) should fail the unique
            // constraint rather than silently double-record, which is
            // exactly what ProcessedFeedItemStore relies on to treat that
            // race as benign.
            entity.HasIndex(p => new { p.RuleId, p.ItemKey }).IsUnique();
            entity.Property(p => p.ItemKey).IsRequired().HasMaxLength(1000);
        });

        modelBuilder.Entity<EngineNode>(entity =>
        {
            entity.HasKey(n => n.Id);
            entity.Property(n => n.Name).IsRequired().HasMaxLength(200);
            entity.Property(n => n.BaseAddress).IsRequired().HasConversion(
                address => address.ToString(), value => new Uri(value));
            // Encrypted at rest (see ProtectedStringConverter) - this is a
            // real gottrentd bearer token, the same secret its own
            // RequireBearerToken middleware trusts. A longer max length
            // than a raw token needs: Data Protection's ciphertext
            // (base64, IV + auth tag + key-ring metadata included) is
            // noticeably larger than the plaintext it wraps.
            entity.Property(n => n.Token).IsRequired().HasMaxLength(4000)
                .HasConversion(new ProtectedStringConverter(dataProtectionProvider.CreateProtector("GoTorrent.Hub.EngineNode.Token")));
        });

        modelBuilder.Entity<TorrentHistoryEntry>(entity =>
        {
            entity.HasKey(h => h.Id);
            entity.Property(h => h.NodeName).IsRequired().HasMaxLength(200);
            entity.Property(h => h.InfoHash).IsRequired().HasMaxLength(64);
            entity.Property(h => h.Name).IsRequired().HasMaxLength(500);
            entity.Property(h => h.Category).HasMaxLength(200);
            // See DateTimeOffsetToTicksConverter's own doc comment - a
            // TEXT-mapped DateTimeOffset column cannot be ordered by at
            // all under EF Core's SQLite provider.
            entity.Property(h => h.CompletedAt).HasConversion(new DateTimeOffsetToTicksConverter());
            // One archive entry per (node, infohash) - see HistoryRecorder's
            // own ExistsAsync check; this is the hard backstop against a
            // concurrent recording pass racing itself, same reasoning as
            // ProcessedFeedItem's unique index.
            entity.HasIndex(h => new { h.NodeId, h.InfoHash }).IsUnique();
        });

        modelBuilder.Entity<SessionSnapshot>(entity =>
        {
            entity.HasKey(s => s.Id);
            entity.Property(s => s.NodeName).IsRequired().HasMaxLength(200);
            entity.Property(s => s.CapturedAt).HasConversion(new DateTimeOffsetToTicksConverter());
            // Not unique - many snapshots per node over time by design.
            // Supports both "snapshots for this node since X" and
            // PruneOlderThanAsync's sweep efficiently.
            entity.HasIndex(s => new { s.NodeId, s.CapturedAt });
        });
    }
}
