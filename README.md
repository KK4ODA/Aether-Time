# Aether Time

Aether Time keeps a computer's clock exact for FT8, FT4 and other timing-critical
digital modes, at home or in the field. It runs on Windows, macOS and Linux
(including Raspberry Pi) as one self-contained program with a browser dashboard.

It brings together what operators use JTSync and BktTimeSync for, and adds:

- **Several time sources at once**: NTP, web time over HTTPS, a GPS receiver
  (serial, USB, Bluetooth or network), gpsd with PPS, and the DT of your FT8/FT4
  decodes from WSJT-X, JTDX or MSHV. Sources that agree are combined; one that
  disagrees with the others is ignored.
- **Accurate offline time**: while you're online it learns your GPS receiver's
  message delay and the DT your station sees when the clock is right. When you
  go portable without internet, GPS and decodes keep you on true UTC, not just
  "on the band".
- **Holdover**: it measures how fast your clock drifts. If every source
  disappears, it keeps correcting from that drift.
- **Corrections that don't break your QSOs**: small errors are corrected
  gradually. Nothing changes while WSJT-X or JTDX is transmitting.
- **A time server for your whole site**: one GPS-equipped laptop can serve
  exact time to every other computer at a field day or DXpedition.
- **A simple, safe updater**: new releases are checked against a signature
  and a checksum before they replace the program. Offline, it simply checks
  later.

## What it combines

| Feature | JTSync | BktTimeSync | Aether Time |
|---|---|---|---|
| NTP internet time | yes | yes | yes, several servers with outlier rejection |
| Time from FT8 decodes (offline) | yes | no | yes, UDP and ALL.TXT, with DT bias learned automatically |
| GPS receiver (NMEA) | | yes | yes, serial, TCP or UDP; message delay learned automatically |
| GPS from a phone | | yes, through its Android app | yes, through any app that shares NMEA over Wi-Fi |
| LAN server for other PCs | | yes, its own client/server | yes, standard NTP, so any OS's built-in client works |
| Send GPS grid to WSJT-X (AutoGrid) | | yes | yes |
| Manual offset / manual adjust | | yes | yes |
| Max correction limit, ignore GPS date | | yes | yes |
| On-screen clock | | yes, BktClock | yes, full-screen wall clock page |
| Sync log | | yes | yes, CSV |
| Tray icon | | yes | yes, with the sync state as a coloured badge |
| Start with the computer | | yes | yes, as a service, plus a login item for the icon |
| Update check | | yes, notifies | yes, signed automatic install |
| Web time over HTTPS (works where NTP is blocked) | | | yes |
| gpsd with PPS (sub-millisecond) | | | yes |
| Combining sources and rejecting bad ones | | | yes |
| Drift learning and holdover | | | yes |
| Gradual correction, no changes during TX | | | yes |
| Relays WSJT-X traffic to JTAlert, GridTracker | | | yes, both directions |
| Warns about other time programs fighting for the clock | | | yes |
| Windows, macOS, Linux, Raspberry Pi | Windows | Windows | yes |
| Remote dashboard and JSON API | | | yes |

The JTSync and BktTimeSync columns describe their published documentation. An
empty cell means the feature isn't mentioned there.

## Getting started

1. Download the archive for your system from the
   [releases page](https://github.com/KK4ODA/Aether-Time/releases) and unpack it.
2. Install it as a service. The service starts at boot and has permission to
   set the clock.

   Windows (Command Prompt opened with **Run as administrator**):

   ```bash
   aether-time.exe service install --start
   ```

   macOS and Linux:

   ```bash
   sudo ./aether-time service install --start
   ```

3. Start `aether-time` from your desktop (double-click it on Windows). An icon
   appears in the system tray or menu bar; choose **Start at login** in its
   menu to keep it there. The dashboard is at <http://127.0.0.1:8123/>.

On Windows you can skip step 2: double-click `aether-time.exe` and use
**Install service** on the dashboard. Windows asks for permission once.

Running `aether-time` without the service also works, but it can only measure
until it's started as administrator or root.

### The tray icon

The badge on the clock shows the state at a glance: green when synchronized,
amber while correcting (or when the clock can't be changed), blue in holdover,
red with no time source, and grey when Aether Time isn't running. Hover for
the current clock error. The menu opens the dashboard or the wall clock, syncs
now, switches to measuring only, installs a waiting update, and turns
**Start at login** on or off.

The service has no access to your desktop, so the icon runs as a small
separate program in your session and talks to the service. Starting
`aether-time` while the service runs shows just the icon; without the service,
the same program also does the time keeping.

- Linux: works with KDE, XFCE, Cinnamon, MATE and others. GNOME needs the
  "AppIndicator and KStatusNotifierItem Support" extension (Ubuntu ships it).
  Without a desktop (a headless Raspberry Pi, SSH) the icon is skipped.
- macOS: the menu-bar icon needs Apple's toolchain, so it's included in builds
  made on a Mac, which is how the release workflow builds them.

### Connect WSJT-X, JTDX or MSHV

In WSJT-X open **Settings → Reporting**. Set **UDP Server** to `127.0.0.1`,
port `2237`, and tick **Accept UDP requests**. JTDX and MSHV have the same
settings.

If JTAlert, GridTracker or a logger already listens on that port, choose one of
these:

- Let Aether Time sit in the middle. Move the other program to port 2238 and add
  `127.0.0.1:2238` under **Settings → FT8/FT4 decodes → Pass the traffic on to**.
  Replies such as "call this station" are passed back to WSJT-X.
- Use a multicast address such as `239.255.0.1` in WSJT-X and in every program
  that listens.
- Point **Also read ALL.TXT from** at WSJT-X's ALL.TXT log.

### Connect a GPS receiver

Under **Settings → GPS receiver**, choose the serial port and speed. Most
receivers use 4800 or 9600 baud. Phone apps that share NMEA over Wi-Fi work with
the TCP connection type. While the computer is online, Aether Time learns the
receiver's message delay (watch **Message delay** on the dashboard). After that
the GPS gives accurate time on its own.

On a Raspberry Pi with gpsd and a PPS signal, enable **gpsd** instead for
sub-millisecond accuracy.

### Field day: serve time to the other computers

Enable **Settings → Time server for your network** on the computer that has GPS
or internet time. On the other computers, set that computer's address as the
NTP server. On Windows that's
`w32tm /config /manualpeerlist:192.168.1.10 /syncfromflags:manual /update`.
Those computers can also run Aether Time with that address as their only NTP
server.

## How it decides

Every source reports its measured offset and an honest error bound. Aether Time
finds the sources whose bounds overlap and averages them, weighting the more
precise ones more heavily. A source that disagrees with the others is shown as
"Disagrees, ignored". Offsets smaller than 20 ms are left alone. Larger ones are
corrected gradually, at most 25 ms per second. Errors of 500 ms or more are
corrected in one step. All of these thresholds can be changed under
**Settings → Clock correction**.

With only FT8 decodes, typical accuracy is ±30 to 60 ms. With a calibrated
serial GPS it's a few milliseconds. With PPS it's under a millisecond.

## Command line

```text
aether-time                      start and open the dashboard
aether-time run --monitor        measure only, never change the clock
aether-time run --no-tray        no tray icon
aether-time tray                 only the tray icon, for the running service
aether-time sync [--dry-run]     one-shot NTP sync, like ntpdate
aether-time status               state of the running instance
aether-time service install --start | uninstall | start | stop | restart | status
aether-time update [--check]     check for or install the latest release
aether-time config               show the settings file
```

Settings live in `config.json` in the data folder: `C:\ProgramData\AetherTime`,
`/Library/Application Support/AetherTime` or `/var/lib/aether-time` when running
as a service. A per-user folder is used otherwise. Set `AETHER_TIME_HOME` to use
a different folder.

## The dashboard is private by default

The dashboard listens only on `127.0.0.1`. It refuses requests that name another
host (DNS rebinding) and refuses changes from other web sites. To reach it from
a phone or another computer, set **Settings → Dashboard → Address** to
`0.0.0.0:8123` and set a password.

## Publishing releases and signed updates

Releases are built by GitHub Actions when you push a version tag:

```bash
git tag v0.1.0
```

```bash
git push origin v0.1.0
```

Tags with a suffix such as `v0.2.0-beta.1` go to the beta channel.

### Signed updates

Set this up once, before the first public release, on your own computer (it
needs Git and Go 1.24 or newer):

1. In the repository folder, create the key pair:

   ```bash
   go run ./cmd/aether-sign keygen
   ```

   The private key goes to `aether-signing-key.secret` (git-ignored, never
   printed). The public key goes into `internal/updater/signing_key.pub`.
2. Store the private key as the repository secret `AETHER_SIGNING_KEY`, either
   with the GitHub CLI or by pasting the file's contents under
   **Settings → Secrets and variables → Actions → New repository secret**:

   ```bash
   gh secret set AETHER_SIGNING_KEY < aether-signing-key.secret
   ```

3. Keep a copy of the file's contents in your password manager, then delete
   the file.
4. Commit and push `internal/updater/signing_key.pub`.

Every build after that refuses updates that aren't signed with your key. The
release workflow checks that the secret matches the committed public key, and
stops rather than publish an unsigned release. For extra protection, add
yourself as a required reviewer of the `release` environment
(**Settings → Environments**) so the key is only used after you approve a run.

If the private key is ever lost, run `keygen -force` to make a new pair. Copies
already installed won't accept releases signed with the new key, so their
users update by hand once.

## Building from source

Go 1.24 or newer is required. No C compiler is needed.

```bash
go test ./...
```

```bash
go build ./cmd/aether-time
```

```bash
scripts/build.sh 0.1.0 && scripts/checksums.sh
```

`scripts/build.sh` cross-compiles every platform into `dist/`, and
`scripts/checksums.sh` writes `dist/checksums.txt`. macOS builds include the
menu-bar icon only when built on a Mac.

## Not done yet

- Trimble TSIP receivers. NMEA output works with most receivers, including
  Trimble units set to NMEA.
- Pop-up notifications on each correction. The tray badge and the dashboard
  show the state instead.
- Signed installers. Windows SmartScreen and macOS Gatekeeper warn about
  unsigned downloads until code-signing certificates are added to the release
  workflow.
- NTS (authenticated NTP) and decoding WWV, CHU or DCF77 audio.

The fonts in the dashboard are B612 and B612 Mono, released under the SIL Open
Font License; see `internal/web/static/fonts/OFL.txt`.
