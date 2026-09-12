using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using Avalonia.Platform;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// <see cref="IDesktopNotifier"/> via the classic <c>Shell_NotifyIcon</c>
/// balloon API - real toast-style notifications
/// (<c>Windows.UI.Notifications</c>) need an AUMID registration and COM
/// activation that only really make sense for an MSIX-packaged app; this
/// app isn't packaged yet (that's 6.4), and this mechanism is what every
/// unpackaged Win32 tray app has used for this since Windows 2000 - still
/// real, still shows in the notification area and Action Center history
/// on modern Windows, still no admin rights or manifest work needed.
///
/// <para>
/// <c>Shell_NotifyIcon</c> can only attach a balloon to an icon that is
/// already registered via <c>NIM_ADD</c> - there is no way to show a
/// balloon with no anchoring icon at all. Avalonia's own <c>TrayIcon</c>
/// (App.axaml, 6.3's first slice) owns its native icon internally with no
/// public handle this class can reuse, so <see cref="Attach"/> registers
/// a second, independent notify-icon entry under the main window's own
/// HWND purely to anchor notifications to. **This is a real, deliberate
/// trade-off, not an oversight**: it means a second small icon appears in
/// the tray/overflow area alongside the app's visible one. Building a
/// real modern-toast pipeline (AUMID registration, COM activation,
/// CsWinRT projections for an unpackaged app) would avoid it, but is
/// substantially more machinery for one 6.3 slice - left as a documented
/// gap 6.4's packaging work could revisit rather than solved here.
/// </para>
/// </summary>
public sealed class WindowsDesktopNotifier : IDesktopNotifier
{
    private const int NotifyIconId = 1;

    private IntPtr _hwnd;
    private bool _added;
    private IconHandle? _icon;

    public void Attach(IntPtr ownerWindowHandle)
    {
        if (!OperatingSystem.IsWindows() || ownerWindowHandle == IntPtr.Zero)
        {
            return;
        }
        _hwnd = ownerWindowHandle;
        _icon = LoadAppIcon();

        var data = NewData();
        data.uFlags = NIF_ICON | NIF_TIP;
        data.hIcon = _icon?.Handle ?? IntPtr.Zero;
        data.szTip = "GoTorrent";
        _added = Shell_NotifyIcon(NIM_ADD, ref data);
    }

    public void Notify(string title, string message)
    {
        if (!_added)
        {
            return;
        }
        var data = NewData();
        data.uFlags = NIF_INFO;
        data.szInfoTitle = title;
        data.szInfo = message;
        data.dwInfoFlags = NIIF_INFO;
        Shell_NotifyIcon(NIM_MODIFY, ref data);
    }

    /// <summary>
    /// The <c>ByValTStr</c>-marshaled string fields must never be null -
    /// the marshaler throws trying to copy a null string into a fixed
    /// buffer regardless of which <c>uFlags</c> say Windows itself will
    /// actually read, since marshaling happens on the whole struct before
    /// the call, not per logical field.
    /// </summary>
    private NOTIFYICONDATA NewData() => new()
    {
        cbSize = Marshal.SizeOf<NOTIFYICONDATA>(),
        hWnd = _hwnd,
        uID = NotifyIconId,
        szTip = string.Empty,
        szInfo = string.Empty,
        szInfoTitle = string.Empty,
    };

    /// <summary>
    /// The app's own tray icon (<c>App.axaml</c>) is loaded from this same
    /// embedded Avalonia resource - not a loose file, so
    /// <c>ExtractIconEx</c> (which needs a real path on disk) can't read
    /// it directly. <see cref="System.Drawing.Icon"/> parses an .ico
    /// file's bytes from any <see cref="Stream"/>, no package reference
    /// needed - same "already part of the Windows shared framework, check
    /// before reaching for a compatibility package" lesson this app's own
    /// autostart service already ran into with <c>Microsoft.Win32.Registry</c>.
    /// </summary>
    [SupportedOSPlatform("windows")]
    private static IconHandle? LoadAppIcon()
    {
        try
        {
            using var stream = AssetLoader.Open(new Uri("avares://GoTorrent.Desktop/Assets/app-icon.ico"));
            return new IconHandle(new System.Drawing.Icon(stream));
        }
        catch (Exception ex) when (ex is IOException or FileNotFoundException or ArgumentException)
        {
            return null;
        }
    }

    /// <summary>Keeps the loaded <see cref="System.Drawing.Icon"/> alive for as long as its HICON is in use - disposing it would destroy the handle Shell_NotifyIcon still references.</summary>
    [SupportedOSPlatform("windows")]
    private sealed class IconHandle(System.Drawing.Icon icon) : IDisposable
    {
        public IntPtr Handle => icon.Handle;

        public void Dispose() => icon.Dispose();
    }

    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    private static extern bool Shell_NotifyIcon(int dwMessage, ref NOTIFYICONDATA data);

    private const int NIM_ADD = 0x00000000;
    private const int NIM_MODIFY = 0x00000001;
    private const int NIF_ICON = 0x00000002;
    private const int NIF_INFO = 0x00000010;
    private const int NIF_TIP = 0x00000004;
    private const int NIIF_INFO = 0x00000001;

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NOTIFYICONDATA
    {
        public int cbSize;
        public IntPtr hWnd;
        public int uID;
        public int uFlags;
        public int uCallbackMessage;
        public IntPtr hIcon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
        public string szTip;
        public int dwState;
        public int dwStateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)]
        public string szInfo;
        public int uTimeoutOrVersion;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)]
        public string szInfoTitle;
        public int dwInfoFlags;
        public Guid guidItem;
        public IntPtr hBalloonIcon;
    }
}
