using GoTorrent.Hub.Infrastructure.Auth;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Api.Controllers;

/// <summary>
/// The only unauthenticated routes in the Hub (every other controller is
/// protected by <c>Program.cs</c>'s <c>MapControllers().RequireAuthorization()</c>).
/// </summary>
[ApiController]
[Route("api/v1/auth")]
[AllowAnonymous]
public sealed class AuthController(UserManager<IdentityUser<Guid>> users, JwtTokenService tokens) : ControllerBase
{
    /// <summary>
    /// Creates an account. Deliberately not an open self-service endpoint
    /// on what may be an internet-reachable Hub: allowed either when no
    /// account exists yet (bootstrapping the first/owner account, the
    /// same self-bootstrap shape gottrentd's own <c>LoadOrCreateToken</c>
    /// uses on the Go side) or when the caller is already authenticated
    /// as an existing user (an owner adding another account). Note that
    /// authentication middleware still runs on an <see cref="AllowAnonymous"/>
    /// action - it just isn't required to reach it - so a valid bearer
    /// token on the request is enough to satisfy the second case.
    /// </summary>
    [HttpPost("register")]
    public async Task<IActionResult> RegisterAsync(RegisterRequest request, CancellationToken cancellationToken)
    {
        var anyUserExists = await users.Users.AnyAsync(cancellationToken);
        if (anyUserExists && User.Identity?.IsAuthenticated != true)
        {
            return Forbid();
        }

        var user = new IdentityUser<Guid> { UserName = request.UserName };
        var result = await users.CreateAsync(user, request.Password);
        if (!result.Succeeded)
        {
            return BadRequest(result.Errors.Select(e => e.Description));
        }
        return NoContent();
    }

    /// <summary>
    /// <see cref="UserManager{TUser}.CheckPasswordAsync"/> alone does
    /// <b>not</b> track failed attempts or enforce lockout - that only
    /// happens automatically via <c>SignInManager</c>, which this Hub
    /// doesn't use (no cookie sign-in, only tokens). Calling
    /// <see cref="UserManager{TUser}.AccessFailedAsync"/>/
    /// <see cref="UserManager{TUser}.IsLockedOutAsync"/> by hand here is
    /// what actually makes Identity's lockout policy (5 failed attempts,
    /// 5-minute lockout by default) real instead of configured-but-inert.
    /// </summary>
    [HttpPost("login")]
    public async Task<ActionResult<LoginResponse>> LoginAsync(LoginRequest request, CancellationToken cancellationToken)
    {
        var user = await users.FindByNameAsync(request.UserName);
        if (user is null)
        {
            return Unauthorized();
        }
        if (await users.IsLockedOutAsync(user))
        {
            return Unauthorized("Account locked after too many failed attempts. Try again later.");
        }
        if (!await users.CheckPasswordAsync(user, request.Password))
        {
            await users.AccessFailedAsync(user);
            return Unauthorized();
        }
        await users.ResetAccessFailedCountAsync(user);

        var (accessToken, expiresAt) = tokens.CreateToken(user);
        return Ok(new LoginResponse(accessToken, expiresAt));
    }
}
