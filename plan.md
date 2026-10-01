# tincan — plan

A tin-can telephone for your terminal: a small Discord written in Go, with servers, text channels, voice channels, and webcams rendered as ASCII art. The UI is built with Bubble Tea.

## Goals

- **Text chat**: servers (called *guilds* in code, to avoid confusion with the backend process), text channels, persistent history, and online presence.
- **Voice**: Discord-style voice channels. Joining one connects you to everyone in it, and you stay connected while you browse text channels.
- **ASCII webcam**: while in a voice channel, you can turn on your camera. Everyone in the channel sees a live grid of ASCII-art video tiles.
- **Audience**: a group of about 2–10 friends connecting over the internet to one self-hosted server (a VPS, a home box, or a LAN machine).
- **Easy install**: friends install a prebuilt client binary with one `curl … | sh` command. They need no Go, no compiler and no system libraries (see [Distribution](#distribution)).

## Non-goals (for now)

DMs, roles and permission flags, reactions, threads, file uploads, message edit/delete, screen share, end-to-end encryption, echo cancellation, push-to-talk, federation, mobile, and public sign-up at scale.

## Decisions

Decisions you made:

| Topic | Decision |
|---|---|
| Hosting | One central server binary. It hosts many guilds, and every client connects to its address. |
| Identity | Username + password accounts. Anyone can create a guild and becomes its owner. Others join with an invite code. Only the owner can create or delete channels and kick members. |
| Voice/video UX | Discord-style: voice channels in the sidebar. A voice channel's main pane is the ASCII video grid, and a voice status bar stays visible when you switch to text channels. |
| Language | Go, for both server and client. |
| Distribution | Prebuilt binaries on GitHub Releases, installed and updated with a `curl … \| sh` script. `go install …@latest` also works for friends who have Go. |

Decisions I made by default (**tell me if you disagree**):

| Topic | Default | Why / alternatives |
|---|---|---|
| Transport | **QUIC** ([quic-go](https://github.com/quic-go/quic-go)). Reliable streams carry chat and control, and unreliable datagrams carry voice. It needs one UDP port and has TLS built in. | *WebSocket for everything* is simpler, but TCP head-of-line blocking makes voice stutter on any packet loss. *WebRTC (pion)* has the best media stack but brings SDP/ICE/RTP and SFU complexity that we don't need, since everyone talks to one server. |
| TLS certs | The server generates a self-signed cert on first run and prints its fingerprint. The client pins it on first connect, like SSH trust-on-first-use (TOFU). `-cert/-key` flags accept a real cert, which clients accept without pinning when it's valid for the hostname (so renewals don't trigger warnings). | This avoids having to buy a domain or set up Let's Encrypt just to play with friends. |
| Storage | SQLite via `modernc.org/sqlite` (pure Go) | The server stays cgo-free, so it cross-compiles easily for a Linux VPS. |
| Passwords | bcrypt (`golang.org/x/crypto/bcrypt`). Session tokens are random 32 bytes; only their SHA-256 hash is stored. | Standard. |
| ASCII style | Brightness ramp `" .:-=+*#%@"`, monochrome by default. The `v` key toggles truecolor mode, where each character takes the pixel's color. | This matches the "ASCII" requirement while leaving room for a fancier look. |
| Video format | The sender captures 160×120 at about 12 fps and sends small JPEGs (stdlib `image/jpeg`). **Receivers** convert to ASCII at their own tile size. | That's about 3–4 KB per frame, and each viewer's terminal size picks the resolution. Sending pre-rendered ASCII would fix one size for every viewer. |
| Camera capture | The camera driver from [pion/mediadevices](https://github.com/pion/mediadevices) (`pkg/driver/camera` only, not its codecs), behind a `Camera` interface | Nothing to install. On macOS it uses Apple's AVFoundation through cgo, linking only system frameworks. On Linux it uses v4l2 through pure Go (`blackjack/webcam`). An ffmpeg subprocess would be simpler to code, but friends would have to install ffmpeg, which breaks the one-command install. |
| Audio I/O | [malgo](https://github.com/gen2brain/malgo) (miniaudio) | It uses cgo, but miniaudio is bundled: it links nothing on macOS and only `-ldl` on Linux, so friends need just a C compiler. There's no mature cgo-free option for microphone capture. |
| Audio codec | Opus at 48 kHz mono, 20 ms frames, about 24 kbps, using **pure-Go [gopus](https://pkg.go.dev/github.com/thesyncim/gopus)**. The Phase 2 spike checks quality and CPU. If it fails, **vendor the libopus C source** into the repo as a cgo package. | System libopus (`hraban/opus`) is ruled out: the binary would need libopus installed or statically linked on every CI runner, and `go install` would fail for anyone without `brew install opus pkg-config`. Both options here build with nothing but a C compiler. |
| Module path | `github.com/jvitoroc/tincan`, a public GitHub repo | `go install` needs a fetchable path. |
| Mic mode | Open mic with a noise gate (RMS threshold) plus mute and deafen toggles | Most terminals don't report key-release events, so push-to-talk isn't reliable. Headphones are recommended because there's no echo cancellation. |
| Platforms | macOS and Linux. Windows is best-effort, later. | Your machine is macOS. Windows cgo/audio/camera setup is its own project. |

## Architecture

```
┌──────────────── client: tincan ────────────────┐              ┌──────────── tincan-server ────────────┐
│                                                      │              │                                             │
│  Bubble Tea TUI ◄── client state ◄── conn (RPC+events)◄═ control ══►│ hub ── authz ── handlers ── store (SQLite)  │
│                                                      │   stream     │  │ presence, event fan-out by guild         │
│  mic ─► noise gate ─► opus enc ─────────────────────── datagrams ──►│ voice rooms: relay datagrams to room,       │
│  speaker ◄─ mixer ◄─ jitter buf ◄─ opus dec ◄───────── datagrams ◄──│   stamping sender id (never trust client)   │
│                                                      │              │                                             │
│  camera ─► 160×120 JPEG ──────────────────────────── uni stream ───►│ video relay: per-subscriber "latest frame  │
│  video grid ◄─ ascii ◄─ jpeg dec ◄────────────────── uni stream ◄───│   per publisher" mailbox → drops stale     │
└──────────────────────────────────────────────────────┘   (QUIC/UDP) └─────────────────────────────────────────────┘
```

Everything travels over **one QUIC connection** per client, on one UDP port (default `7777`):

1. **Control stream**: one bidirectional stream, opened by the client right after connecting. It carries length-prefixed JSON messages (requests, responses and server-pushed events).
2. **Voice datagrams**: unreliable QUIC datagrams, one Opus packet each.
3. **Video streams**: a publisher opens a unidirectional stream and writes JPEG frames. The server opens one unidirectional stream to each subscriber and writes frames from all publishers, tagged with the sender's id.

### Repository layout

```
go.mod                        module github.com/jvitoroc/tincan  (no replace directives!)
Makefile                      build / test / run-server / run-client
README.md                     install + host instructions for friends
install.sh                    curl | sh installer (detect OS/arch, verify checksum)
.github/workflows/ci.yml      vet + race tests + cgo-free builds on every push
.github/workflows/release.yml per-OS builds → GitHub Release → install smoke test
cmd/
  tincan/               client main: flags, config, start TUI
  tincan-server/        server main: flags, data dir, start listener
internal/
  version/                    app version from debug.ReadBuildInfo + protocol version
  proto/                      wire types + framing (shared by both sides)
  transport/                  shared QUIC config; server listener + self-signed cert; client dial + cert trust (CA / pinned / first use)
  server/
    store/                    SQLite schema, migrations, queries
    hub/                      connections, auth, presence, request handlers, event fan-out
    voice/                    voice rooms, datagram relay, video relay
  client/
    config/                   config.json (mode 0600): servers, pinned fingerprints, session tokens
    conn/                     hello handshake, request/response correlation, event channel
    state/                    client-side cache of guilds/channels/members/messages (pure, testable)
    audio/                    device I/O, codec, noise gate, jitter buffer, mixer, fake mic
    video/                    camera sources (mediadevices, fake), downscale, JPEG encode/decode, decode worker
    ascii/                    image → ASCII cells (mono + color), aspect correction
    tui/                      Bubble Tea models (app, login, sidebar, messages, input, grid, voice bar)
```

Each package has one job, and the pure packages (`proto`, `store`, `state`, `ascii`, the jitter buffer and mixer) can be tested without a network, audio device or terminal.

## Protocol

### Control stream

Framing: a 4-byte big-endian length, then a JSON object (maximum 64 KiB).

```jsonc
// request (client → server)
{"id": 7, "type": "message.send", "data": {"channel_id": 12, "content": "hi"}}
// response (server → client), matches the request id
{"re": 7, "data": {"message": {...}}}
{"re": 7, "error": {"code": "forbidden", "message": "not a member of this guild"}}
// event (server → client), no id
{"type": "message.created", "data": {"message": {...}}}
```

**Requests**

| Type | Who | Notes |
|---|---|---|
| `hello` | anyone | Must be the first request. The client sends `protocol` (an integer) and `client_version`. If the protocol is incompatible, the server refuses and includes its own version, and the client tells the user how to update. |
| `auth.register` / `auth.login` | anyone | Returns a session token and the user. Must come right after `hello`. |
| `auth.resume` | anyone | Logs in with a stored token. |
| `guild.create` | any user | Creates `#general` and `🔊 General` by default. |
| `guild.join` | any user | Takes an invite code. |
| `guild.leave` | member | The owner can't leave; they delete the guild instead. |
| `guild.delete` | owner | |
| `invite.create` | member | Returns an 8-character code. The client shows it as `host:port/CODE`, so the invite alone is enough for a new friend to connect (see `tincan join`). |
| `channel.create` / `channel.delete` | owner | `kind`: `text` or `voice`. |
| `member.kick` | owner | |
| `message.send` | member | Up to 2000 characters. |
| `message.history` | member | `before_id`, `limit` (≤ 100) |
| `voice.join` / `voice.leave` | member | Joining returns the participants and your `ssrc` (a numeric sender id). One voice channel per user; joining another one moves you. |
| `voice.state` | in voice | `muted`, `deafened`, `camera` |
| `video.subscribe` | in voice | `enabled`. The client only receives video while the grid is actually on screen. |

**Events:** `ready` (a full snapshot after auth: guilds, channels, members, presence, voice states), `message.created`, `guild.updated`, `guild.deleted`, `channel.created`, `channel.deleted`, `member.joined`, `member.left`, `presence.updated`, `voice.state.updated`.

Events for a guild go to every connected member of that guild, so the sidebar, presence and voice lists stay live.

### Voice datagrams

```
[kind u8 = 0x01][ssrc u32][seq u16][opus payload …]
```

The same layout works in both directions. The server **overwrites `ssrc`** with the sender's assigned id and then forwards the datagram to everyone else in the room (never back to the sender). With a noise gate, a sender only sends while speaking, so receiving packets from someone means they're speaking; that drives the speaking indicator.

### Video streams

```
publisher → server   (uni stream, first byte 0x02)   repeated: [len u32][seq u32][jpeg …]
server → subscriber  (uni stream, first byte 0x03)   repeated: [len u32][ssrc u32][seq u32][jpeg …]
```

The server keeps a **latest-frame-per-publisher mailbox** for each subscriber, with one writer goroutine draining it. A slow subscriber simply skips frames, and it never blocks the relay or the other subscribers.

## Data model (SQLite)

```sql
users    (id INTEGER PK, username TEXT UNIQUE, password_hash BLOB, created_at)
sessions (token_hash BLOB PK, user_id, created_at, last_used_at)
guilds   (id INTEGER PK, name, owner_id, created_at)
members  (guild_id, user_id, joined_at, PK(guild_id, user_id))
channels (id INTEGER PK, guild_id, name, kind CHECK (kind IN ('text','voice')), position, created_at)
messages (id INTEGER PK, channel_id, author_id, content, created_at)   -- INDEX (channel_id, id)
invites  (code TEXT PK, guild_id, created_by, created_at, uses)
```

Presence and voice membership live **in memory only**: they describe live connections, not data.

## Media pipeline

**Audio (48 kHz, mono, int16, 20 ms = 960 samples)**

- *Send:* malgo capture callback → ring buffer → 20 ms frames → noise gate (with about 300 ms of hangover so words aren't clipped) → Opus encode → datagram. Nothing is sent while muted.
- *Receive:* datagram → a jitter buffer per sender (ordered by `seq`, target 60 ms, max 200 ms, drops excess to catch up) → Opus decode, or packet-loss concealment for gaps → the **mixer** sums active senders into int32, clamps to int16, and feeds a ring buffer → malgo playback callback. Deafen stops playback and also mutes, like Discord.
- *Budget:* about 40 kbps per speaker including overhead.

**Video (about 12 fps)**

- *Send:* mediadevices camera driver, opened at the smallest supported mode (for example 640×480) → `image.Image` frames → throttle to 12 fps → downscale to 160×120 (`golang.org/x/image/draw`) → JPEG (quality around 50) → video stream.
- *Receive:* stream → decode worker (off the UI goroutine) keeps the latest frame per sender → ASCII conversion at the current tile size → a `videoFrameMsg` to Bubble Tea.
- *ASCII:* terminal cells are about 2:1 tall, so each character samples a 1×2 pixel block. Brightness maps onto the ramp, and color mode wraps each character in a truecolor escape.
- *Budget:* 3–4 KB × 12 fps ≈ 300–400 kbps per camera. With 5 people on camera, each client downloads about 1.6 Mbps and the server uploads about 8 Mbps, which a small VPS handles fine.

**Test sources:** `--fake-mic` (sine tone or WAV file) and `--fake-cam` (animated test pattern), so you can run several clients on one machine and test without hardware.

## TUI

```
┌Srv─┬ friends ───────┬ #general ─────────────────────────────────────┐
│[fr]│ TEXT           │ alice  12:01  anyone up for a call?           │
│ wk │ ▸# general     │ bob    12:02  yep, joining hangout            │
│ +  │  # memes       │ you    12:02  one sec                         │
│    │ VOICE          │                                               │
│    │ ▾🔊 hangout     │                                               │
│    │   ● alice 🎤📷 │                                               │
│    │   ● bob   🎤   │                                               │
│    ├────────────────┤                                               │
│    │🔊 hangout      │───────────────────────────────────────────────┤
│    │ muted · cam on │ > _                                           │
└────┴────────────────┴───────────────────────────────────────────────┘
```

- **Screens:** login/register → main. `tincan join host:port/CODE` starts at login with the server filled in and joins the guild after auth. The server address, pinned certs and session token are saved in `os.UserConfigDir()/tincan/config.json` (mode 0600). `TINCAN_CONFIG_DIR` overrides the location, so several clients can run on one machine.
- **Focus:** `Tab` / `Shift+Tab` cycles guilds → channels → messages → input. Use `↑/↓` or `j/k` and `Enter` in lists, and `Esc` to leave the input.
- **Selecting a voice channel** joins it, and its main pane becomes the ASCII grid. The grid lays out automatically for 1–9 tiles and shows a name label and a speaking highlight on each tile.
- **Shortcuts** (when the input isn't focused): `m` mute, `d` deafen, `c` camera, `v` ASCII color mode, `L` leave voice, `q` quit.
- **Slash commands** (always available, and they avoid terminal key-combo problems): `/new-server <name>`, `/join <invite>`, `/invite`, `/new-channel <name>`, `/new-voice <name>`, `/delete-channel`, `/kick <user>`, `/leave-server`, `/mute`, `/deafen`, `/cam`, `/hangup`, `/help`.
- **History:** opening a channel loads the last 50 messages, and scrolling to the top fetches older ones.
- **Rendering:** network, audio and video goroutines talk to the UI only via `program.Send(msg)`, so the UI goroutine never blocks on I/O.

## Distribution

Friends install a **prebuilt binary** with nothing else required: no Go, no C compiler, no Homebrew. `go install` still works for friends who have Go.

**What a friend runs:**

```sh
curl -fsSL https://raw.githubusercontent.com/jvitoroc/tincan/main/install.sh | sh
tincan join friends.example.com:7777/K3J9QX2A     # the invite you got from /invite
```

Running the same curl command again updates to the latest release. `join` opens the register/login screen with the server already filled in, shows the cert fingerprint and pins it, then joins the guild. After that, running `tincan` is enough.

**What you run to host:**

```sh
curl -fsSL https://raw.githubusercontent.com/jvitoroc/tincan/main/install.sh | sh -s -- --server
tincan-server --listen :7777 --data ~/.tincan-server
```

### Release pipeline (`.github/workflows/release.yml`)

It runs when you push a `v*` tag. The audio and camera libraries contain C code, so each OS has to build on its own runner. That's why this is a plain Actions matrix and not GoReleaser, whose free version can't merge builds from several runners.

| Job | Runner | Output |
|---|---|---|
| macOS client | `macos-latest` | Builds `darwin/arm64` natively and `darwin/amd64` with `CC="clang -arch x86_64"`, merges them with `lipo` into **one universal binary**, then ad-hoc signs it (`codesign -s -`). |
| Linux client | `ubuntu-22.04` + `ubuntu-22.04-arm` (the oldest LTS runners available) | `linux/amd64` and `linux/arm64`, linked against glibc 2.35, so they run on Ubuntu 22.04+, Debian 12+ and current Fedora/Arch. |
| Windows client | `windows-latest` (with MinGW gcc) | `windows/amd64`, best-effort |
| Server | `ubuntu-latest` | `CGO_ENABLED=0` cross-compile for linux/darwin/windows × amd64/arm64 |
| Publish | `ubuntu-latest`, after all of the above | Writes `checksums.txt` (SHA-256) and runs `gh release create <tag> --generate-notes` with every archive. |
| Smoke test | `macos-latest` + `ubuntu-22.04`, after publish | Runs `install.sh` against the new release on a clean runner and checks that `tincan --version` prints the tag, the same thing a friend does. |

Every build job runs `go test ./...` first and builds with `-trimpath`. Asset names:
- `tincan_<os>_<arch>.tar.gz` (`tincan_darwin_universal.tar.gz` on macOS, `.zip` on Windows)
- `tincan-server_<os>_<arch>.tar.gz`

The version needs no `-ldflags`: since Go 1.24, `go build` records the git tag in the binary's build info, and `tincan --version` reads it with `debug.ReadBuildInfo()`.

### Install script (`install.sh`)

- POSIX `sh`, for macOS and Linux.
- Detects the OS and CPU with `uname`, then downloads from `https://github.com/jvitoroc/tincan/releases/latest/download/<asset>`. That's GitHub's redirect to the newest release, so there are no API calls and no rate limits.
- Checks the download against `checksums.txt` and refuses to install on a mismatch.
- Installs to `~/.local/bin` by default (`TINCAN_INSTALL_DIR` overrides it) and prints a hint if that directory isn't on `PATH`.
- `TINCAN_VERSION=v0.2.0` pins a version. `--server` installs the server binary instead.
- **Gatekeeper:** files downloaded with curl don't get macOS's quarantine flag, so the ad-hoc-signed binary runs without a "can't be opened" dialog. That's why the README sends Mac friends to the script and not the Releases page. Apple signing and notarization ($99/yr) can come later if it's ever needed.
- **Windows:** download the `.zip` from the Releases page. It's best-effort, with no script.

### `go install` (secondary path)

Friends who have Go can run `go install github.com/jvitoroc/tincan/cmd/tincan@latest`. That path needs a C compiler: Xcode Command Line Tools on macOS, or gcc on Linux. The rules that keep it working also keep the release builds simple, so they stay:
- **No `replace` directives** in `go.mod`. `go install pkg@version` refuses modules that have them.
- **The `go` line stays as low as the dependencies allow** (currently 1.26, because quic-go needs it).
- **Every client dependency builds from source with only a C compiler**, so there's no system libopus or ffmpeg. This is also why the CI runners can build without installing any packages.
- **Device code is guarded by cgo build tags.** Files that use malgo or the camera driver have `//go:build cgo`, and matching `!cgo` stubs return "not supported in this build". So `CGO_ENABLED=0 go install …` still produces a working text-chat client.
- **Semver tags** (`v0.1.0`, `v0.2.0`, …). The same tag drives both the release pipeline and `@latest`. The protocol version only changes on breaking wire changes, and `hello` tells stale clients how to update.

### CI (`.github/workflows/ci.yml`, on every push)

- `go vet` and `go test -race ./...` on macOS and Ubuntu.
- `CGO_ENABLED=0 go build` for both binaries. The server must stay pure Go, and the client's stub path must compile.

## Error handling

| Situation | Behavior |
|---|---|
| Connection lost | The client shows a "reconnecting…" banner and retries with backoff (1s → 30s), then sends `auth.resume` and reloads the snapshot. Voice isn't rejoined automatically; you get a toast. |
| Client disconnects (server side) | The server removes the client from its voice room, updates presence, and broadcasts both changes. |
| Cert fingerprint mismatch | The client refuses to connect and shows both fingerprints (SSH-style warning). |
| Packet loss / reordering (voice) | The jitter buffer reorders, Opus concealment fills gaps, and late packets are dropped. |
| Slow subscriber (video) | The latest-wins mailbox drops stale frames and never blocks. |
| No camera / permission denied | A toast explains the fix (for example, allow camera access for your terminal app in System Settings → Privacy). Voice keeps working. |
| Client built without cgo (only possible via `go install`) | Text chat works. Voice and camera show "this build has no audio/video support; install a C compiler and re-run `go install`, or use the install script". |
| Client too old for server | `hello` fails, and the client prints the command to update: the curl install command, or `go install …@latest` if build info shows it was installed that way. |
| Audio device failure | A toast; you stay in the channel muted, as a listener if playback works. |
| Bad input / authz failure | An error response with a readable message, shown inline. Every request is authorized on the server, and the client is never trusted. |
| Oversized frames / messages | Rejected at the framing layer (control 64 KiB, video frame 256 KiB, message 2000 characters). |
| Spam | A per-connection token bucket on requests (for example 10/s with a burst of 20). |

## Testing

- **`proto`**: round-trip encode/decode tests for every message and header type, plus a fuzz test for the framing reader.
- **`store`**: tests against a temporary SQLite file covering schema, constraints, pagination and invites.
- **`hub` integration**: start a real server on `localhost` with in-process QUIC and connect 2–3 test clients. Check that a message reaches the right members only, presence updates, owner-only operations reject non-owners, and kicked members stop receiving events. Run with `-race`.
- **`voice` relay**: A's datagrams reach B and C with A's ssrc stamped on, and never reach A itself. A spoofed ssrc gets overwritten. The video mailbox drops frames for a stalled subscriber without blocking the others.
- **`audio`**: jitter buffer reordering, loss triggering concealment, overflow catch-up, mixer clipping, and noise-gate hangover, all pure and without a device.
- **`ascii`**: golden-file tests (a gradient image produces the expected text), aspect correction, and color escapes.
- **`client/state`**: applying events produces the expected cache.
- **`tui`**: model `Update` tests (key → state) for focus, slash-command parsing and grid layout math.
- **Manual end-to-end** per phase: two or three clients (using `--fake-mic` and `--fake-cam`) against a local server, then a real test with a friend over the internet.

## Build phases

Each phase ends in something usable and gets its own detailed implementation plan before coding.

### Phase 0 — Foundation
- `git init`, `go mod init github.com/jvitoroc/tincan`, the repo layout above, and a Makefile.
- `version`: build info and protocol version. `proto`: envelope, length-prefixed JSON framing, media headers, with tests.
- `transport`: QUIC listener, and self-signed cert generation saved to `--data` (default `./data`).
- `conn`: dial with TOFU pinning, `hello` handshake, request/response correlation, event channel.
- The CI and release workflows and `install.sh`, set up now while the build is still simple. Push to GitHub and tag `v0.0.1`.
- **Milestone:** the release job publishes binaries, and the curl install on a Mac gives a client that connects to a local server, pins the cert, and prints the round-trip time of a ping. `go install …@v0.0.1` works too.

### Phase 1 — Text chat (the core)
- `store` schema and queries; `auth.*` with bcrypt and tokens.
- `hub`: connection registry, `ready` snapshot, per-guild event fan-out, authz, rate limiting.
- Handlers for guilds, invites, channels, messages and history.
- `client/state` and the TUI: login, the main layout, the message view with history paging, input, slash commands, and presence dots.
- `tincan join host:port/CODE`, a README with the friend install steps, and tag `v0.1.0`.
- **Milestone:** a friend who has never seen the repo installs the client with the one-line curl command and joins with one invite string. You chat, and the history survives a server restart.

### Phase 2 — Voice
- **Codec spike (first task):** check gopus for voice quality at 24 kbps and for CPU use: encoding plus decoding 4 streams should stay under about 5% of one core. Keep it if it passes; otherwise vendor the libopus source. Either way, the choice sits behind a `Codec` interface.
- Server voice rooms: join/leave/move, ssrc assignment, `voice.state.updated` events, datagram relay.
- Client audio: malgo I/O (`//go:build cgo`, with a stub), noise gate, codec, jitter buffer, mixer, and `--fake-mic`.
- TUI: voice members in the sidebar, speaking indicator, voice bar, and mute/deafen/leave.
- This is the first cgo code, so check that the release builds still work: the universal macOS binary captures and plays audio, and so do the Linux binaries.
- **Milestone:** a three-person call over the internet that holds up under packet loss, with mouth-to-ear delay around 150 ms or better on a decent connection.

### Phase 3 — ASCII webcam
- `ascii` renderer (monochrome and color) with golden tests.
- **Camera spike (first task):** the mediadevices camera driver on macOS (Apple Silicon and the current macOS release) and on Linux. Confirm the permission prompt and frame delivery.
- `video`: mediadevices camera source (`//go:build cgo`, with a stub), `--fake-cam`, downscale, JPEG encoding, decode worker.
- Server video relay with per-subscriber latest-wins mailboxes, plus `video.subscribe`.
- TUI: grid layout, self-preview tile, `c` / `v` toggles, and a speaking highlight on tiles.
- **Milestone:** everyone in a voice channel sees everyone's live ASCII webcam at about 10 fps or more.

### Phase 4 — Polish (optional, pick and choose)
Focusing one tile full-screen, unread markers, message delete, a nicer reconnect UX, Windows support, and a `--tcp` fallback if UDP is blocked somewhere.

## Setup (what you'll need)

- Go 1.27 (installed) and Xcode Command Line Tools (installed). Nothing else.
- A **public** GitHub repo at `github.com/jvitoroc/tincan`. Releases, raw `install.sh` and Actions runners (including the arm64 Linux ones) are all free for public repos.
- Your terminal app (Terminal, iTerm or VS Code) will ask for microphone and camera permission the first time.
- On the server host: open **UDP** port 7777 in the firewall or router.

## Risks

- **Rendering throughput:** several ASCII tiles at 12 fps is a lot of terminal output. To keep it manageable: render ASCII off the UI goroutine, cap the frame rate, and rely on Bubble Tea v2's cell-diffing renderer. If it's still heavy, lower the fps or skip unchanged tiles.
- **Echo:** there's no echo cancellation, so recommend headphones. The noise gate helps a little.
- **Pure-Go Opus maturity:** gopus is new (v0.1.x). The Phase 2 spike decides, and vendored libopus source is the fallback, which still works with `go install`.
- **Unsigned binaries:** macOS blocks browser-downloaded binaries that aren't notarized. The curl script avoids that, and the README says to use it. If friends keep downloading from the Releases page anyway, the fix is Apple signing and notarization ($99/yr).
- **cgo in release builds:** each OS needs its own runner, and cross-arch macOS builds depend on `clang -arch`. The Phase 2 check catches breakage the first time cgo appears. The Intel half of the universal macOS binary can be smoke-tested on an Apple Silicon machine under Rosetta (`arch -x86_64 tincan --version`; verified in Phase 0). Real audio and camera on Intel still need a friend with an Intel Mac.
- **Linux glibc:** binaries built on Ubuntu 22.04 won't run on older distros or on musl (Alpine). That's acceptable for desktop friends, who can use `go install` if needed.
- **Camera driver on current macOS:** mediadevices' AVFoundation binding is an older Objective-C bridge. The Phase 3 spike checks it early. The fallback is our own small AVFoundation cgo shim, still with no installs.
- **Capture/playback clock drift:** the jitter buffer absorbs it by dropping or concealing, with occasional tiny glitches accepted.
- **UDP-hostile networks:** QUIC won't get through some corporate networks. That's acceptable for friends, with a TCP fallback as a Phase 4 option.

## Dependencies (latest versions checked 2026-09-30)

| Module | Version | Used by |
|---|---|---|
| `charm.land/bubbletea/v2` | v2.0.10 | client TUI |
| `charm.land/bubbles/v2` | v2.2.1 | text input, viewport, lists |
| `charm.land/lipgloss/v2` | v2.0.6 | styling/layout |
| `github.com/quic-go/quic-go` | v0.63.0 | transport (both sides) |
| `modernc.org/sqlite` | v1.60.1 | server storage |
| `golang.org/x/crypto` | v0.57.0 | bcrypt |
| `github.com/gen2brain/malgo` | v0.11.26 | audio I/O (cgo, self-contained) |
| `github.com/thesyncim/gopus` | v0.1.2 | Opus codec, pure Go (vendored libopus if the spike fails) |
| `github.com/pion/mediadevices` | v0.10.0 | camera driver only (`pkg/driver/camera`) |
| `golang.org/x/image` | v0.46.0 | frame downscaling |

No runtime binaries and no system libraries. Everything builds with `go install` plus a C compiler.
