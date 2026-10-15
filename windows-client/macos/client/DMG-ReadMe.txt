Inevitable Mobile Relay — Mac app (Apple Silicon, macOS 13 or later)

Copy “Inevitable Mobile Relay.app” into your personal Applications folder:
  Finder → Go → Home → Applications (create this folder if needed).
Open the copied app, then eject this disk image. No administrator password is
needed. You can also place the app in another folder you own.

Keep the app open while using the local proxies. Minimizing keeps sharing
active; closing its window, choosing Quit, or signing out stops all traffic.
This app does not install a daemon, login item, or automatic startup task.

Activate and pair this app separately from a PKG installation. A same-signed
app upgrade keeps your per-user state. The phone still pairs locally by QR.
Hosted connectivity is the default and needs no public workload listener.

Copy HTTP proxy uses localtest.me for Refract compatibility. It resolves to
this Mac's loopback address; the app still listens only on 127.0.0.1. No
administrator setup is needed. After upgrading, copy the HTTP line again;
your phone's port, credentials and pairing stay the same. SOCKS5 is unchanged.
This hostname requires DNS resolution, which some managed networks block.

If the installed PKG service is active, stop it through its existing controls
before using this app. The app cannot take over another process's ports or
silently switch proxy addresses. The PKG remains available for administrator
installation and background service operation.
