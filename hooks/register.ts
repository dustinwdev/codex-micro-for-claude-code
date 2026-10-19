import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Hint, Reported, State } from '../types'

type DeviceInfo = {
  connected: boolean
  battery?: number
  charging?: boolean
  /** Why the daemon could not open the pad. */
  problem?: string
}

/** What the daemon sends this session, one JSON object per line. */
/** Where the daemon listens: a Unix socket, or on Windows a loopback URL and its secret. */
type Endpoint = { socket: string } | { url: string; token: string }

type Message =
  | ({ type: 'attached'; session: string } & Endpoint)
  | { type: 'status'; slot: number | null; device: DeviceInfo; waiting: number }
  | { type: 'state'; state: State }
  | ({ type: 'action' } & Action)
  | { type: 'notice'; text: string }
  | { type: 'error'; message: string }

/** A control on the pad, carried out through Claude Code. */
type Action =
  | { action: 'command'; command: string; args: string }
  | { action: 'text'; text: string; submit: boolean }
  | { action: 'submit' | 'interrupt' | 'fork' | 'effort-up' | 'effort-down' }

const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max']
const DEFAULT_EFFORT = 'high'
const LOW_BATTERY = 15

const session: {
  isActive: boolean
  hasReported: boolean
  state: State
  turnId: string | undefined
  effort: string | undefined
  /** Where the daemon takes this session's state, once attached. */
  daemon: { session: string; endpoint: Endpoint } | undefined
  /** The pad problem last shown, so each is shown once. */
  problem: string | undefined
  sent: Promise<unknown>
} = {
  isActive: false,
  hasReported: false,
  state: 'idle',
  turnId: undefined,
  effort: undefined,
  daemon: undefined,
  problem: undefined,
  sent: Promise.resolve(),
}

const hint = atom({ plugin: 'codex-micro', key: 'hint' } as const, null as Hint)
const reported = atom({ plugin: 'codex-micro', key: 'reported' } as const, null as Reported | null)

let windowsProgram: Promise<string> | undefined

/**
 * The program's path. On Windows the launcher can't hand its process over,
 * so it is asked for the path, fetching the program first if need be.
 */
function program($: EngineInterface): Promise<string> {
  const root = $.plugin.root
  if (!/^[A-Za-z]:[\\/]/.test(root)) {
    return Promise.resolve(`${root}/bin/codex-micro`)
  }
  const launcher = `${root}\\bin\\codex-micro.ps1`
  windowsProgram ??= $.process
    .run(['powershell.exe', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', launcher, 'path'], {
      timeoutMs: 120000,
    })
    .then(({ exitCode, stdout, stderr }) => {
      if (exitCode !== 0) {
        throw new Error(stderr.trim())
      }
      return stdout.trim()
    })
  windowsProgram.catch(() => (windowsProgram = undefined))
  return windowsProgram
}

function show($: EngineInterface, line: Hint) {
  // Rejected once the module has unloaded, when there is nothing to draw.
  update($, hint, () => line).catch(() => undefined)
}

function remember($: EngineInterface) {
  const value: Reported = { state: session.state, turnId: session.turnId ?? null, effort: session.effort ?? null }
  update($, reported, () => value).catch(() => undefined)
}

/** Tell the daemon this session's state. In order, and never awaited by a turn. */
function report($: EngineInterface, state: State, force = false) {
  if (!session.isActive || (session.state === state && !force)) {
    return
  }
  session.state = state
  session.hasReported = true
  remember($)
  const daemon = session.daemon
  if (daemon === undefined) {
    return // sent on attaching
  }
  session.sent = session.sent.then(async () => {
    // The id goes along each time: /clear changes it.
    const body = JSON.stringify({ state, claude_session: await $.session.id() })
    await call($, daemon, 'state', body).catch(() => undefined)
  })
}

/** POSTs to one of this session's endpoints on the daemon. */
function call($: EngineInterface, daemon: { session: string; endpoint: Endpoint }, what: string, body: string) {
  const path = `/sessions/${daemon.session}/${what}`
  const e = daemon.endpoint
  return 'socket' in e
    ? $.http.fetch(`http://codex-micro${path}`, { method: 'POST', body, socketPath: e.socket })
    : $.http.fetch(`${e.url}${path}`, { method: 'POST', body, headers: { authorization: `Bearer ${e.token}` } })
}

/**
 * Waits for the pad to answer a permission prompt. The dialog stays up and
 * answers from the keyboard meanwhile; the daemon stops waiting then. A
 * dropped connection (the daemon restarting) asks again.
 */
async function askPad($: EngineInterface): Promise<'allow' | 'deny' | null> {
  // After the state, so the prompt is not taken for one already answered.
  await session.sent
  while (session.state === 'awaiting') {
    const daemon = session.daemon
    const reply = daemon === undefined ? undefined : await call($, daemon, 'ask', '').catch(() => undefined)
    if (reply?.ok) {
      return (JSON.parse(reply.text) as { answer: 'allow' | 'deny' | null }).answer ?? null
    }
    await $.clock.sleep(1000)
  }
  return null
}

/** On (re)connecting: send the state again, or after a reload, the one before it. */
async function resync($: EngineInterface) {
  if (session.hasReported) {
    report($, session.state, true)
    return
  }
  const before = await read($, reported).catch(() => null)
  if (before !== null && !session.hasReported) {
    session.turnId = before.turnId ?? undefined
    session.effort = before.effort ?? undefined
    report($, before.state, true)
  }
}

function statusLine(m: { slot: number | null; device: DeviceInfo; waiting: number }) {
  if (!m.device.connected) {
    return m.device.problem === undefined ? 'pad asleep' : 'pad unavailable: run /codex-micro'
  }
  const parts = [m.slot === null ? 'no free key' : `key ${m.slot + 1}`]
  const { battery, charging } = m.device
  if (battery !== undefined) {
    parts.push(`${charging ? '⚡' : battery <= LOW_BATTERY ? '🪫' : '🔋'}${battery}%`)
  }
  if (m.waiting > 0) {
    parts.push(`${m.waiting} waiting`)
  }
  return parts.join(' · ')
}

function stepEffort(current: string | undefined, step: number) {
  const at = EFFORTS.indexOf(current ?? DEFAULT_EFFORT)
  const from = at === -1 ? EFFORTS.indexOf(DEFAULT_EFFORT) : at
  return EFFORTS[Math.min(EFFORTS.length - 1, Math.max(0, from + step))] ?? DEFAULT_EFFORT
}

async function act($: EngineInterface, m: Action) {
  switch (m.action) {
    case 'command':
      await $.command.run({ command: m.command, args: m.args })
      return
    case 'text':
      if (m.submit) {
        void $.prompt.submit({ text: m.text })
      } else {
        await $.prompt.fill({ text: m.text, mode: 'insert' })
      }
      return
    case 'submit': {
      const { text } = await $.prompt.read()
      if (text.trim() !== '') {
        await $.prompt.fill({ text: '', mode: 'replace' })
        void $.prompt.submit({ text })
      }
      return
    }
    case 'interrupt':
      if (session.turnId !== undefined) {
        await $.turn.abort({ turnId: session.turnId })
      }
      return
    case 'effort-up':
    case 'effort-down': {
      const level = stepEffort(session.effort, m.action === 'effort-up' ? 1 : -1)
      session.effort = level
      remember($)
      await $.command.run({ command: 'effort', args: level })
      $.ui.toast(`Effort: ${level}`)
      return
    }
    case 'fork': {
      // A copy of the conversation in a terminal of its own, so it gets a key.
      const id = await $.session.id()
      const cwd = await $.session.cwd()
      await $.process.run([await program($), 'launch', cwd, 'claude', '--resume', id, '--fork-session'])
      $.ui.toast('Forked into a new terminal')
      return
    }
  }
}

async function receive($: EngineInterface, m: Message) {
  switch (m.type) {
    case 'attached': {
      const { type: _, session: id, ...endpoint } = m
      session.daemon = { session: id, endpoint }
      await resync($)
      return
    }
    case 'status':
      show($, statusLine(m))
      if (m.device.problem !== undefined && m.device.problem !== session.problem) {
        $.ui.toast(`Codex Micro: ${m.device.problem}`)
      }
      session.problem = m.device.problem
      return
    case 'state':
      // The daemon moved this session itself: its answer was read on screen.
      session.state = m.state
      remember($)
      return
    case 'action':
      await act($, m)
      return
    case 'notice':
      $.ui.toast(m.text)
      return
    case 'error':
      $.ui.log(`codex-micro: ${m.message}`, { to: 'debug' })
      return
  }
}

/** Gives the session a key, for as long as it lives. */
function start($: EngineInterface) {
  if (!session.isActive) {
    session.isActive = true
    // Ending the loop (session exit, plugin reload) ends attach, which frees the key.
    void follow($)
  }
}

/** Runs the session's attach process and handles what it relays, for the session's life. */
async function follow($: EngineInterface) {
  let buffered = ''
  let failure = ''
  let argv: string[]
  try {
    argv = [await program($), 'attach']
  } catch (e) {
    $.ui.toast(String(e instanceof Error ? e.message : e))
    return
  }
  const attach = $.process.spawn({ argv, env: { CODEX_MICRO_CLAUDE_SESSION: await $.session.id() } })
  for await (const { stream, text } of attach) {
    if (stream === 'stderr') {
      $.ui.log(`codex-micro: ${text}`, { to: 'debug' })
      failure += text
      continue
    }
    buffered += text
    const lines = buffered.split('\n')
    buffered = lines.pop() ?? ''
    for (const line of lines) {
      try {
        await receive($, JSON.parse(line) as Message)
      } catch (e) {
        $.ui.log(`codex-micro: ${line}: ${e}`, { to: 'debug' })
      }
    }
  }
  session.daemon = undefined
  show($, null)
  if (failure.trim() !== '') {
    // attach stopped for good: the download failed, or the plugin needs a restart.
    $.ui.toast(failure.trim().split('\n').pop() ?? failure)
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    $.command
      .register({ name: 'codex-micro', description: 'Check the Codex Micro setup and show its status' })
      .catch(() => undefined)
    if (e.isInteractive) {
      start($)
    }
    return started
  })

  // The desktop app runs sessions through the SDK, which start without a
  // person at them: one is there once the app attaches to draw.
  on('session.attach', async ($, e, next) => {
    const attached = await next(e)
    if (e.surface === 'desktop') {
      start($)
    }
    return attached
  })

  on('command.run', { command: 'codex-micro' }, async $ => {
    const { stdout, stderr } = await $.process.run([await program($), 'doctor'], { timeoutMs: 120000 })
    return { text: (stdout || stderr).trimEnd() }
  })

  on('turn.start', ($, e, next) => {
    if (e.agentId === undefined) {
      session.turnId = e.turnId
      report($, 'working')
    }
    return next(e)
  })

  // The pad's check and cross answer the prompt here, in any terminal or app.
  on('classic.PermissionRequest', async ($, e, next) => {
    report($, 'awaiting')
    const answer = await askPad($).catch(() => null)
    if (answer === 'allow') {
      report($, 'working')
      return { decision: { behavior: 'allow' } }
    }
    if (answer === 'deny') {
      return { decision: { behavior: 'deny', message: 'Denied from the Codex Micro', interrupt: true } }
    }
    return next(e)
  }).catch(($, e, next) => next(e))

  // A tool call resolves once its permission prompt is answered.
  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    if (session.state === 'awaiting') {
      report($, 'working')
    }
    return result
  }).catch(($, e, next) => next(e))

  on('turn.complete', ($, e, next) => {
    if (e.agentId === undefined) {
      session.turnId = undefined
      report($, e.reason === 'answer' ? 'unread' : e.reason === 'aborted' ? 'idle' : 'error')
    }
    return next(e)
  })

  // The effort each turn ran at, so the dial steps from the current level.
  on('classic.Stop', ($, e, next) => {
    const level = (e as { effort?: { level?: string } }).effort?.level
    if (level !== undefined) {
      session.effort = level
      remember($)
    }
    return next(e)
  }).catch(($, e, next) => next(e))

  // Typing in the prompt box means the last answer has been read.
  on('prompt.edit', ($, e, next) => {
    if (session.state === 'unread' || session.state === 'error') {
      report($, 'idle')
    }
    return next(e)
  })

  on('ui.render', { component: 'PromptHint' }, async ($, e, next) => {
    const line = await read($, hint)
    return line === null ? next(e) : next({ ...e, props: { ...e.props, tail: line } })
  })
}
