# Codex Micro for Claude Code

Use the OpenAI Codex Micro with [Claude Code](https://claude.com/claude-code).

Each Claude Code session gets one of the pad's six agent keys, in the terminal or in the Claude app's Code tab. The key shows what the session is doing, and pressing it brings the session forward. Approve or deny permission prompts from the pad, hold the mic key to dictate with Wispr Flow or a similar app, step reasoning effort with the dial, and move between sessions with the joystick. Every control can be remapped.

It also works with the Work Louder Creator Micro 2, which runs the same firmware. It is an independent project, not affiliated with OpenAI or Work Louder.

## Requirements

- Claude Code **2.1.303** or newer (`claude --version`). The plugin is built on Claude Code's plugin hooks, which are new.
- macOS or Linux. Windows support is experimental: built and tested in CI, not yet on hardware.
- A Codex Micro or Creator Micro 2, paired over Bluetooth or connected over USB-C. Quit the ChatGPT app first if it's running: only one app can use the pad at a time.

Lights, approving and denying from the pad, and every Claude Code action work in any terminal. Switching to a session with its agent key needs a terminal or app the plugin can bring forward:

| | Supported |
| --- | --- |
| macOS | Ghostty, iTerm2, Terminal, and the Claude app's Code tab |
| Linux | any terminal under Hyprland |
| Windows | any terminal (the window, not the tab) |
| Anywhere | sessions inside tmux, WezTerm, or kitty with remote control on |

## Install

```sh
claude plugin install codex-micro --marketplace dustinwdev/codex-micro-for-claude-code
```

Or, at the Claude Code prompt:

```
/plugin install codex-micro --marketplace dustinwdev/codex-micro-for-claude-code
```

Then start a new Claude Code session and run `/codex-micro`. It checks the setup and prints the fix for anything missing:

- **macOS** asks you to allow your terminal app under **Input Monitoring** the first time. Allow it, then quit and reopen the terminal app.
- **Linux** may need a udev rule so you can open the pad without root. `/codex-micro` prints the commands.

On first use the plugin downloads its program for your platform from this repository's [releases](https://github.com/dustinwdev/codex-micro-for-claude-code/releases), and checks it against the checksum in [`bin/checksums.txt`](bin/checksums.txt) before running it.

### The Claude app

The Claude app's Code tab runs sessions on a Claude Code of its own, which runs plugins like this one only with a setting turned on. Add it to `~/.claude/settings.json`, then quit and reopen the app:

```json
{
  "env": { "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS": "1" }
}
```

A Code tab chat takes a key once you send its first message, when the app starts its session. On macOS, also allow **Claude** under **Input Monitoring**, so the pad works when no terminal is open.

### Install with an agent

Paste this into Claude Code:

```
Install the codex-micro plugin for my Codex Micro macropad (https://github.com/dustinwdev/codex-micro-for-claude-code).
1. Check that `claude --version` is 2.1.303 or newer. If it isn't, stop and tell me.
2. Run `claude plugin install codex-micro --marketplace dustinwdev/codex-micro-for-claude-code`.
3. Find the installed copy with `find ~/.claude/plugins -path '*codex-micro*/bin/codex-micro' -type f`
   and run `<that path> doctor`.
4. For each line marked ✗, follow the fix it prints. Ask me before running anything with sudo,
   and walk me through any macOS permission it needs.
5. If I use the Claude app's Code tab, add "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS": "1" under "env"
   in ~/.claude/settings.json, keeping everything else in the file.
6. Ask me whether I use a dictation app such as Wispr Flow, and if so set up the mic key for it
   with the codex-micro-config skill.
7. Run doctor again, and tell me to start a new Claude Code session when every line passes.
```

## Using it

**Agent keys** show each session's state:

| Color | Meaning |
| --- | --- |
| White | idle |
| Blue | working |
| Amber, pulsing | waiting for you to approve something |
| Green | finished; turns white once you look at it |
| Red | the last turn ended in an error |
| Off | no session |

Press a key to bring its session forward. A new session takes the first free key, and a closed one frees its key at once. To put a session on a key of your choice, hold the Codex logo key and press that agent key: the session you're in moves there, swapping with whatever was on it.

**The other controls**, by default:

| Control | Action |
| --- | --- |
| Lightning | toggle fast mode |
| Check | approve the permission prompt waiting on you |
| Cross | deny it; with none waiting, stop the running turn |
| Branch | fork the conversation into a new terminal window |
| Microphone | toggle Claude Code's voice mode |
| Codex logo | send what is typed in the prompt |
| Dial | turn to step reasoning effort, press to open `/effort` |
| Joystick | up: cycle permission modes; down: go to the session that needs you; left and right: previous and next session |

Approve and deny go to the session waiting on you, the focused one first, and answer its prompt directly: the keyboard still works alongside. Everything else acts on the session whose terminal has focus.

Under the prompt, each session shows its key and the pad's battery, such as `key 2 · 🔋61% · 1 waiting`.

## Dictation

To dictate with Wispr Flow, Superwhisper, macOS Dictation or a similar app, have the mic key hold down your app's shortcut key for as long as you hold the mic key:

```toml
# ~/.config/codex-micro/config.toml
[keys]
mic = { hold = "fn" }
```

Use your app's key: `fn` for Wispr Flow's default, or a chord such as `"right-alt"` or `"ctrl+space"`. Or ask Claude to *"set up my mic key for Wispr Flow"*.

On macOS, holding keys needs your terminal app allowed under **Accessibility**; `/codex-micro` checks it. Windows has no `fn` to press: use your app's other shortcut, such as `ctrl+win` for Wispr Flow. Linux can't hold keys yet.

## Remapping

Ask Claude: *"make the dial press switch to Sonnet"* or *"make the Codex key type 'continue' and send it"*. The bundled `codex-micro-config` skill edits the config for you.

To edit it by hand, copy [`config.example.toml`](config.example.toml) to `~/.config/codex-micro/config.toml`. A control can run a built-in action, any slash command, text for the prompt, a key chord, a held key or a program:

```toml
[dial]
press = "/model sonnet"

[keys]
send = { text = "continue", submit = true }
branch = { keys = "ctrl+r" }
```

Changes apply on the next press. `/codex-micro` points out any mistake in the file.

## How it works

A small daemon owns the pad and talks to it over HID. Each Claude Code session runs a helper that holds the session's place with the daemon for the life of the session, and the plugin reports the session's state to the daemon over a local socket. Permission prompts wait on the daemon for the pad's answer. The daemon starts on its own with the first session and logs to `~/Library/Logs/codex-micro/daemon.log` on macOS, `~/.local/state/codex-micro/daemon.log` on Linux, and `%LOCALAPPDATA%\codex-micro\daemon.log` on Windows.

The program is written in Go, with no dependencies to install. Nothing is written to the pad's memory: its keymap stays as it is.

## Troubleshooting

- **`pad asleep` under the prompt**: the pad is asleep, out of range or paired with another computer. Press any key to wake it.
- **`pad unavailable` under the prompt**: macOS kept the pad from the app the daemon started under. `/codex-micro` names the app to allow under **Input Monitoring**.
- **Agent keys don't switch windows**: on Linux the window is found by its process, so terminals that serve several windows from one process (such as `footclient`) can't be told apart; start each terminal as its own process. On macOS, the first switch may ask to let your terminal app control itself or another terminal app; allow it.
- **Keys never turn amber**: in organizations whose managed settings restrict plugin hooks, the hook that sees permission prompts doesn't run. Everything else works.
- Anything else: run `/codex-micro`, then check the daemon log.

## Building from source

With Go installed, `go build -o build/codex-micro ./cmd/codex-micro` builds the program. The plugin uses a build in `build/` over the downloaded one. `go test ./...` runs the program's tests, and `claude plugin test .` the plugin's.

Releases are reproducible: `scripts/build-release 0.8.0 dist` rebuilds a version's programs byte for byte, so you can check them against [`bin/checksums.txt`](bin/checksums.txt) yourself. The release workflow publishes a version only when its own build matches.

## Credits

The device protocol builds on the reverse-engineering notes of [creator-micro-agent-status](https://github.com/coeckmath-maker/creator-micro-agent-status) by Mathieu Coeck.

## License

MIT. See [LICENSE](LICENSE).
