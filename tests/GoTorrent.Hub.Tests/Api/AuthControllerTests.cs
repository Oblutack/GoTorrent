using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using GoTorrent.Hub.Api.Controllers;

namespace GoTorrent.Hub.Tests.Api;

public sealed class AuthControllerTests(GoTorrentHubApiFactory factory) : IClassFixture<GoTorrentHubApiFactory>
{
    private const string Password = "P@ssw0rd1234!";

    [Fact]
    public async Task Register_FirstUserOnAFreshHub_SucceedsWithoutAuthentication()
    {
        // A fresh, isolated factory - the shared class-fixture one may
        // already have a bootstrap user from another test in this class,
        // and "first user on an empty Hub" is exactly the case under test.
        using var freshFactory = new GoTorrentHubApiFactory();
        using var client = freshFactory.CreateClient();

        var response = await client.PostAsJsonAsync("/api/v1/auth/register", new RegisterRequest("first-user", Password));

        response.EnsureSuccessStatusCode();
    }

    [Fact]
    public async Task Register_SecondUserWithoutAuthentication_IsForbidden()
    {
        using var freshFactory = new GoTorrentHubApiFactory();
        using var client = freshFactory.CreateClient();
        var first = await client.PostAsJsonAsync("/api/v1/auth/register", new RegisterRequest("owner", Password));
        first.EnsureSuccessStatusCode();

        var second = await client.PostAsJsonAsync("/api/v1/auth/register", new RegisterRequest("intruder", Password));

        Assert.Equal(HttpStatusCode.Forbidden, second.StatusCode);
    }

    [Fact]
    public async Task Register_AnotherUserWhileAuthenticated_Succeeds()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.PostAsJsonAsync(
            "/api/v1/auth/register", new RegisterRequest($"second-user-{Guid.NewGuid()}", Password));

        response.EnsureSuccessStatusCode();
    }

    [Fact]
    public async Task Register_DuplicateUserName_ReturnsBadRequest()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var name = $"dup-{Guid.NewGuid()}";
        var first = await client.PostAsJsonAsync("/api/v1/auth/register", new RegisterRequest(name, Password));
        first.EnsureSuccessStatusCode();

        var second = await client.PostAsJsonAsync("/api/v1/auth/register", new RegisterRequest(name, Password));

        Assert.Equal(HttpStatusCode.BadRequest, second.StatusCode);
    }

    [Fact]
    public async Task Login_WithCorrectCredentials_ReturnsATokenThatActuallyWorks()
    {
        using var bootstrap = await factory.CreateAuthenticatedClientAsync();
        using var client = factory.CreateClient();

        var login = await client.PostAsJsonAsync(
            "/api/v1/auth/login", new LoginRequest(GoTorrentHubApiFactory.TestUserName, GoTorrentHubApiFactory.TestPassword));
        login.EnsureSuccessStatusCode();
        var body = await login.Content.ReadFromJsonAsync<LoginResponse>();

        client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", body!.AccessToken);
        var protectedResponse = await client.GetAsync("/api/v1/torrents");
        Assert.Equal(HttpStatusCode.OK, protectedResponse.StatusCode);
    }

    [Fact]
    public async Task Login_WithUnknownUserName_ReturnsUnauthorized()
    {
        using var client = factory.CreateClient();

        var response = await client.PostAsJsonAsync(
            "/api/v1/auth/login", new LoginRequest($"nobody-{Guid.NewGuid()}", "whatever"));

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task Login_WithWrongPassword_ReturnsUnauthorized()
    {
        using var bootstrap = await factory.CreateAuthenticatedClientAsync();
        using var client = factory.CreateClient();

        var response = await client.PostAsJsonAsync(
            "/api/v1/auth/login", new LoginRequest(GoTorrentHubApiFactory.TestUserName, "definitely-wrong"));

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task Login_LocksOutAfterRepeatedFailures()
    {
        // Isolated fresh factory: this test intentionally trips lockout,
        // which would otherwise poison every other test sharing this
        // class's bootstrap user.
        using var freshFactory = new GoTorrentHubApiFactory();
        using var client = freshFactory.CreateClient();
        var register = await client.PostAsJsonAsync("/api/v1/auth/register", new RegisterRequest("lockout-user", Password));
        register.EnsureSuccessStatusCode();

        // UserManager.CheckPasswordAsync alone never locks anyone out -
        // AuthController.LoginAsync's own AccessFailedAsync/IsLockedOutAsync
        // calls are what this test actually proves are wired up.
        // Identity's default threshold is 5 failed attempts.
        for (var i = 0; i < 5; i++)
        {
            var failed = await client.PostAsJsonAsync("/api/v1/auth/login", new LoginRequest("lockout-user", "wrong-password"));
            Assert.Equal(HttpStatusCode.Unauthorized, failed.StatusCode);
        }

        // Even the correct password is now rejected - IsLockedOutAsync
        // short-circuits before CheckPasswordAsync ever runs.
        var lockedOut = await client.PostAsJsonAsync("/api/v1/auth/login", new LoginRequest("lockout-user", Password));

        Assert.Equal(HttpStatusCode.Unauthorized, lockedOut.StatusCode);
        var body = await lockedOut.Content.ReadAsStringAsync();
        Assert.Contains("locked", body, StringComparison.OrdinalIgnoreCase);
    }
}
